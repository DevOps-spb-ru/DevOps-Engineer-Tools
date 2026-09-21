package doctor

import (
	"errors"
	"os"
)

// ErrUnsupported возвращается на платформах, где проверку выполнить нельзя:
// сервис работает на Debian, а разработка и тесты идут и на Windows.
var ErrUnsupported = errors.New("проверка не поддерживается на этой платформе")

// FS — файловые операции, нужные проверкам. Интерфейс позволяет прогонять
// `sqlbrc doctor` в тестах без реальных каталогов, прав root и PostgreSQL.
type FS interface {
	Stat(path string) (os.FileInfo, error)
	ReadDir(path string) ([]os.DirEntry, error)
	// WriteProbe проверяет, что в каталоге можно создать и удалить файл:
	// наличие прав в stat ещё не значит, что запись работает (ACL, монтирование ro).
	WriteProbe(dir string) error
	// FreeSpace возвращает свободное и общее место на разделе с указанным путём.
	FreeSpace(path string) (free, total int64, err error)
	// Owner возвращает имя владельца файла; пусто, если определить нельзя.
	Owner(info os.FileInfo) string
}

// OSFS — реализация FS поверх os.
type OSFS struct{}

// Stat возвращает информацию о файле.
func (OSFS) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

// ReadDir читает содержимое каталога.
func (OSFS) ReadDir(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }

// WriteProbe создаёт и сразу удаляет служебный файл в каталоге.
func (OSFS) WriteProbe(dir string) error {
	// #nosec G304 -- каталог берётся из конфига (storage.dir), файл создаётся
	// функцией os.CreateTemp со случайным именем и удаляется сразу после проверки.
	file, err := os.CreateTemp(dir, ".sqlbrc-doctor-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}
