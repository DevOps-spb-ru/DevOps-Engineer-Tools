package store

import (
	"io"
	"testing"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// testNow — момент времени, от которого строятся метки бэкапов в тестах:
// строка «20260921T020000Z» в проверках читается глазами, поэтому время задано
// явно, а не берётся из time.Now.
var testNow = time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC)

// daysAgo возвращает время теста, сдвинутое на указанное число дней назад.
func daysAgo(days int) time.Time {
	return testNow.Add(-time.Duration(days) * 24 * time.Hour)
}

// fakeFS — подмена файловой системы. Реальная нужна почти везде (раскладка
// каталога и права проверяются на файлах), но нехватку места и отказ записи
// метаданных на живом разделе не воспроизвести, поэтому в этих тестах
// файловые операции делегируются OSFS, а нужные вызывают ошибку.
type fakeFS struct {
	FS
	free    int64
	total   int64
	freeErr error
	// failCreate возвращает ошибку для выбранных путей (например, только для
	// файла метаданных): так проверяется «архив записан, метаданные — нет».
	failCreate func(path string) error
	// removeErr подменяет ошибку удаления: политика хранения не должна
	// прерывать уборку из-за одного нечитаемого файла.
	removeErr error
}

// FreeSpace возвращает заданные значения вместо statfs.
func (f *fakeFS) FreeSpace(string) (int64, int64, error) { return f.free, f.total, f.freeErr }

// Create создаёт файл, если для пути не задан отказ.
func (f *fakeFS) Create(path string) (io.WriteCloser, error) {
	if f.failCreate != nil {
		if err := f.failCreate(path); err != nil {
			return nil, err
		}
	}
	return f.FS.Create(path)
}

// Remove удаляет файл, если ошибка удаления не подменена.
func (f *fakeFS) Remove(path string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	return f.FS.Remove(path)
}

// newTestStore создаёт хранилище в отдельном каталоге. Права и раскладка
// проверяются на настоящей файловой системе: так тест ловит и ошибки путей,
// и случайную запись мимо каталога сервиса.
func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return New(dir, pg.DefaultAllowPattern, nil), dir
}

// newFakeStore создаёт хранилище с подменённой файловой системой.
func newFakeStore(t *testing.T, fs *fakeFS) *Store {
	t.Helper()
	if fs.FS == nil {
		fs.FS = OSFS{}
	}
	return New(t.TempDir(), pg.DefaultAllowPattern, fs)
}
