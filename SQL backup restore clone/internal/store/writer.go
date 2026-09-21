package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// BeginOptions — параметры новой записи бэкапа. Отдельная структура вместо
// длинного списка аргументов: она один раз описывает, что за бэкап снимается,
// и её же заполняет вызывающий код (CLI, позже веб-интерфейс).
type BeginOptions struct {
	// Database — имя обслуживаемой базы.
	Database string
	// Tag — метка бэкапа: pre-restore и подобные. Пустая у обычного бэкапа.
	Tag string
	// Note — примечание, которое сохраняется вместе с бэкапом.
	Note string
	// Format — формат pg_dump (по умолчанию custom).
	Format string
	// Compression — уровень сжатия pg_dump (по умолчанию pg.DefaultCompression).
	Compression int
	// ServerVersion — версия сервера на момент дампа.
	ServerVersion string
	// DatabaseSizeBytes — размер базы до дампа: сравнение с размером архива
	// показывает, насколько база изменилась.
	DatabaseSizeBytes int64
	// JobID — задача журнала, в рамках которой снимается бэкап.
	JobID string
	// Now — момент начала записи: из него получается имя файла. Пустое значение
	// означает текущее время.
	Now time.Time
}

// Writer — запись архива. Данные идут в файл <имя>.dump.part, рядом считается
// sha256, а имя архива файл получает только в Commit: прерванный дамп (Abort,
// сбой процесса) не выглядит как полноценный бэкап и не попадает в List.
type Writer struct {
	store        *Store
	opts         BeginOptions
	name         string
	createdAt    time.Time
	startedAt    time.Time
	path         string
	partPath     string
	metadataPath string
	file         io.WriteCloser
	hasher       hash.Hash
	size         int64
	finished     bool
}

// Begin начинает запись бэкапа: создаёт каталог базы и файл <имя>.dump.part.
// Существующий бэкап с тем же именем не перезаписывается: имя содержит метку
// времени с точностью до секунды, и второй дамп той же секунды — ошибка
// вызывающего кода, а не повод потерять готовый архив.
func (s *Store) Begin(opts BeginOptions) (*Writer, error) {
	if err := s.validateDatabase(opts.Database); err != nil {
		return nil, err
	}
	if opts.Tag != "" {
		if err := pg.ValidateTag(opts.Tag); err != nil {
			return nil, err
		}
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	dir, err := s.BackupDir(opts.Database)
	if err != nil {
		return nil, err
	}
	if err := s.fs.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("не удалось создать каталог бэкапов %s: %w", dir, err)
	}
	fileName, err := FormatArchiveName(now, opts.Tag)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fileName)
	if _, err := s.fs.Stat(path); err == nil {
		return nil, fmt.Errorf("бэкап %s/%s уже есть в каталоге: повторите операцию",
			opts.Database, fileName)
	}
	partPath := path + PartialExt
	file, err := s.fs.Create(partPath)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать файл бэкапа %s: %w", partPath, err)
	}
	if opts.Format == "" {
		opts.Format = pg.FormatCustom
	}
	if opts.Compression == 0 {
		opts.Compression = pg.DefaultCompression
	}
	return &Writer{
		store:        s,
		opts:         opts,
		name:         fileName,
		createdAt:    now.UTC(),
		startedAt:    time.Now(),
		path:         path,
		partPath:     partPath,
		metadataPath: filepath.Join(dir, strings.TrimSuffix(fileName, ArchiveExt)+MetadataExt),
		file:         file,
		hasher:       sha256.New(),
	}, nil
}

// Name возвращает имя файла бэкапа, например 20260921T020000Z.dump.
func (w *Writer) Name() string { return w.name }

// Path возвращает путь, по которому появится готовый архив: до Commit файла по
// этому пути нет, данные пишутся в <имя>.dump.part.
func (w *Writer) Path() string { return w.path }

// PartialPath возвращает путь к файлу незавершённой записи.
func (w *Writer) PartialPath() string { return w.partPath }

// Write пишет данные дампа: вывод pg_dump передаётся сюда потоком, поэтому дамп
// не занимает память и не требует временного файла.
func (w *Writer) Write(chunk []byte) (int, error) {
	written, err := w.file.Write(chunk)
	if written > 0 {
		_, _ = w.hasher.Write(chunk[:written])
		w.size += int64(written)
	}
	if err != nil {
		return written, fmt.Errorf("не удалось записать бэкап %s: %w", w.partPath, err)
	}
	return written, nil
}

// Size возвращает объём записанных данных.
func (w *Writer) Size() int64 { return w.size }

// SHA256 возвращает контрольную сумму записанных данных.
func (w *Writer) SHA256() string { return hex.EncodeToString(w.hasher.Sum(nil)) }

// Commit завершает запись: файл одним переименованием получает имя архива, и
// рядом появляется файл метаданных. Ошибка записи метаданных не отменяет
// готовый архив: восстановление возможно и без них (бэкап будет StatusUnknown),
// поэтому вызывающий код получает и бэкап, и ошибку.
func (w *Writer) Commit() (Backup, error) {
	if w.finished {
		return Backup{}, fmt.Errorf("запись бэкапа %s уже завершена", w.partPath)
	}
	w.finished = true
	if err := w.file.Close(); err != nil {
		_ = w.store.fs.Remove(w.partPath)
		return Backup{}, fmt.Errorf("не удалось закрыть файл бэкапа %s: %w", w.partPath, err)
	}
	if err := w.store.fs.Rename(w.partPath, w.path); err != nil {
		_ = w.store.fs.Remove(w.partPath)
		return Backup{}, fmt.Errorf("не удалось переименовать %s в %s: %w", w.partPath, w.path, err)
	}
	name := strings.TrimSuffix(w.name, ArchiveExt)
	backup := Backup{
		ID:                w.opts.Database + "/" + name,
		DB:                w.opts.Database,
		Name:              name,
		Tag:               w.opts.Tag,
		CreatedAt:         w.createdAt,
		Path:              w.path,
		MetadataPath:      w.metadataPath,
		SizeBytes:         w.size,
		SHA256:            w.SHA256(),
		Note:              w.opts.Note,
		Format:            w.opts.Format,
		Compression:       w.opts.Compression,
		ServerVersion:     w.opts.ServerVersion,
		DatabaseSizeBytes: w.opts.DatabaseSizeBytes,
		Duration:          time.Since(w.startedAt),
		Status:            StatusComplete,
		JobID:             w.opts.JobID,
	}
	raw, err := formatMetadata(backup)
	if err != nil {
		return backup, err
	}
	if err := w.writeMetadata(raw); err != nil {
		return backup, err
	}
	return backup, nil
}

// writeMetadata записывает файл метаданных рядом с готовым архивом.
func (w *Writer) writeMetadata(raw []byte) error {
	file, err := w.store.fs.Create(w.metadataPath)
	if err != nil {
		return fmt.Errorf("архив %s готов, но метаданные записать не удалось: %w", w.path, err)
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		_ = w.store.fs.Remove(w.metadataPath)
		return fmt.Errorf("архив %s готов, но метаданные записать не удалось: %w", w.path, err)
	}
	if err := file.Close(); err != nil {
		_ = w.store.fs.Remove(w.metadataPath)
		return fmt.Errorf("архив %s готов, но метаданные записать не удалось: %w", w.path, err)
	}
	return nil
}

// Abort прекращает запись и убирает незавершённый файл: после сбоя дампа в
// каталоге не должно оставаться файлов, которые выглядят как бэкап.
func (w *Writer) Abort() error {
	if w.finished {
		return nil
	}
	w.finished = true
	closeErr := w.file.Close()
	removeErr := w.store.fs.Remove(w.partPath)
	switch {
	case removeErr != nil && !errors.Is(removeErr, os.ErrNotExist):
		return fmt.Errorf("не удалось убрать незавершённый бэкап %s: %w", w.partPath, removeErr)
	case closeErr != nil:
		return fmt.Errorf("не удалось закрыть файл бэкапа %s: %w", w.partPath, closeErr)
	default:
		return nil
	}
}
