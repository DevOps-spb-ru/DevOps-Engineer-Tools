// Package store отвечает за каталог бэкапов сервиса: раскладку
// <storage.dir>/<база>/<метка времени>[-<метка>].dump с файлом метаданных рядом,
// поиск бэкапа по идентификатору, проверку свободного места и политику хранения.
//
// Каталог — единственное место, где сервис создаёт и удаляет файлы, поэтому все
// проверки имени собраны здесь: имя базы задаёт подкаталог, а политика хранения
// удаляет файлы, и «чужие» данные в storage.dir удалены быть не должны.
package store

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// Права на создаваемые файлы и каталоги. Бэкап читает пользователь кластера
// PostgreSQL (проверка doctor про AppArmor), поэтому файлы не 0600, а 0640;
// каталог базы закрыт для посторонних (0750), как и сам storage.dir.
const (
	dirPerm  os.FileMode = 0o750
	filePerm os.FileMode = 0o640
)

// FS — файловые операции, нужные каталогу бэкапов. Интерфейс позволяет
// проверять нехватку места, сбой переименования и уборку мусора без реальных
// разделов и прав: тесты не зависят от платформы разработки.
type FS interface {
	MkdirAll(path string, perm os.FileMode) error
	Create(path string) (io.WriteCloser, error)
	Open(path string) (io.ReadCloser, error)
	ReadDir(path string) ([]os.DirEntry, error)
	Stat(path string) (os.FileInfo, error)
	Rename(oldPath, newPath string) error
	Remove(path string) error
	FreeSpace(path string) (free, total int64, err error)
}

// OSFS — реализация FS поверх os. FreeSpace живёт в fs_unix.go/fs_windows.go:
// на Windows места на разделе посчитать нельзя.
type OSFS struct{}

// MkdirAll создаёт каталог вместе с родителями.
func (OSFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }

// Create создаёт файл с правами 0640. Флаг O_EXCL оставлен намеренно: повторная
// запись по тому же имени означала бы перезапись готового бэкапа, а её быть не
// должно — имя содержит метку времени создания.
func (OSFS) Create(path string) (io.WriteCloser, error) {
	// #nosec G304 -- путь собирается внутри store из storage.dir и проверенного имени базы.
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
}

// Open открывает файл только на чтение: восстановление читает архив, а изменение
// готового бэкапа сервису не нужно.
func (OSFS) Open(path string) (io.ReadCloser, error) {
	// #nosec G304 -- путь собирается внутри store из storage.dir и проверенного имени базы.
	return os.Open(path)
}

// ReadDir читает каталог.
func (OSFS) ReadDir(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }

// Stat возвращает сведения о файле.
func (OSFS) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

// Rename переименовывает файл: готовый архив появляется в каталоге одним
// действием, поэтому прерванная запись не выглядит как полноценный бэкап.
func (OSFS) Rename(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }

// Remove удаляет файл.
func (OSFS) Remove(path string) error { return os.Remove(path) }

// Store — каталог бэкапов сервиса.
type Store struct {
	dir     string
	pattern string
	fs      FS
}

// New создаёт хранилище. allowPattern — шаблон имён обслуживаемых баз из
// databases.pattern: каталог базы создаётся только для имён, которые сервис
// вправе обслуживать. Пустой шаблон означает шаблон по умолчанию (pg).
func New(dir, allowPattern string, fs FS) *Store {
	if fs == nil {
		fs = OSFS{}
	}
	return &Store{dir: dir, pattern: allowPattern, fs: fs}
}

// Dir возвращает каталог бэкапов сервиса.
func (s *Store) Dir() string { return s.dir }

// BackupDir возвращает каталог бэкапов одной базы: <storage.dir>/<база>.
func (s *Store) BackupDir(database string) (string, error) {
	if err := s.validateDatabase(database); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, database), nil
}

// validateDatabase проверяет имя базы перед использованием в пути: то же правило,
// что и при сборке argv (pg.ValidateDBName), — имя из формы или скрипта не может
// выйти за пределы каталога сервиса.
func (s *Store) validateDatabase(database string) error {
	if err := pg.ValidateDBName(database, s.pattern); err != nil {
		return fmt.Errorf("хранилище: %w", err)
	}
	return nil
}

// List возвращает бэкапы базы, начиная со свежего. Файлы, которые сервис не
// создавал, и незавершённые записи (`.part`) в список не попадают: политика
// хранения удаляет только то, что перечислено здесь.
func (s *Store) List(database string) ([]Backup, error) {
	dir, err := s.BackupDir(database)
	if err != nil {
		return nil, err
	}
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// База ещё не бэкапилась: пустой список — не ошибка.
			return nil, nil
		}
		return nil, fmt.Errorf("не удалось прочитать каталог бэкапов %s: %w", dir, err)
	}
	backups := make([]Backup, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ArchiveExt) {
			continue
		}
		backups = append(backups, s.describe(database, dir, entry.Name()))
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].CreatedAt.Equal(backups[j].CreatedAt) {
			return backups[i].Name > backups[j].Name
		}
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})
	return backups, nil
}

// ListDatabases возвращает базы, у которых есть каталог в хранилище: подкаталоги
// с посторонними именами пропускаются, поэтому в список попадают только
// обслуживаемые базы.
func (s *Store) ListDatabases() ([]string, error) {
	entries, err := s.fs.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("не удалось прочитать каталог бэкапов %s: %w", s.dir, err)
	}
	databases := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || s.validateDatabase(entry.Name()) != nil {
			continue
		}
		databases = append(databases, entry.Name())
	}
	sort.Strings(databases)
	return databases, nil
}

// Find возвращает бэкап по идентификатору «<база>/<имя>». Идентификатор приходит
// из CLI и (в 0.3.0) из веб-формы, поэтому проверяется и по частям, и целиком.
func (s *Store) Find(id string) (Backup, error) {
	database, name, err := pg.SplitBackupID(id)
	if err != nil {
		return Backup{}, err
	}
	dir, err := s.BackupDir(database)
	if err != nil {
		return Backup{}, err
	}
	fileName := name + ArchiveExt
	path := filepath.Join(dir, fileName)
	if _, err := s.fs.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Backup{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return Backup{}, fmt.Errorf("не удалось прочитать бэкап %s: %w", path, err)
	}
	return s.describe(database, dir, fileName), nil
}

// Latest возвращает самый свежий бэкап базы.
func (s *Store) Latest(database string) (Backup, error) {
	backups, err := s.List(database)
	if err != nil {
		return Backup{}, err
	}
	if len(backups) == 0 {
		return Backup{}, fmt.Errorf("%w: у базы %s нет бэкапов", ErrNotFound, database)
	}
	return backups[0], nil
}

// FreeSpace возвращает свободное и общее место в байтах на разделе с каталогом
// бэкапов. На платформе разработки (Windows) возвращается ErrUnsupported.
func (s *Store) FreeSpace() (int64, int64, error) {
	if err := s.fs.MkdirAll(s.dir, dirPerm); err != nil {
		return 0, 0, fmt.Errorf("не удалось создать каталог бэкапов %s: %w", s.dir, err)
	}
	return s.fs.FreeSpace(s.dir)
}

// EnsureSpace проверяет, что новый бэкап поместится: свободного места должно
// хватать на порог storage.min_free_space и на сам файл (need — ожидаемый размер,
// 0 если он неизвестен). Отказ возвращается до запуска pg_dump, а не после:
// дамп, оборванный на середине, оставляет в каталоге бесполезный файл.
func (s *Store) EnsureSpace(minFree bytesize.Size, need int64) error {
	free, _, err := s.FreeSpace()
	switch {
	case errors.Is(err, ErrUnsupported):
		// Место на разделе посчитать нельзя (Windows): проверка пропускается,
		// чтобы разработка и тесты не зависели от платформы.
		return nil
	case err != nil:
		return err
	}
	required := int64(minFree)
	if need > 0 {
		if required > math.MaxInt64-need {
			required = math.MaxInt64
		} else {
			required += need
		}
	}
	if required > 0 && free < required {
		return &InsufficientSpaceError{Free: free, Need: required, Path: s.dir}
	}
	return nil
}
