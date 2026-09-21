//go:build !windows

package jobs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileLogPermissions проверяет права файла журнала: записи содержат имена баз
// и команды, поэтому файл должен быть доступен только пользователю сервиса и root
// (0600). Проверка только для POSIX: Windows права из POSIX-бита не выводит.
func TestFileLogPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFileName)
	journal := NewFileLog(path, 10)
	if err := journal.Write(testJob("20260921T020000Z-aaaa1111")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != journalPerm {
		t.Errorf("права файла журнала %v, ожидались %v", info.Mode().Perm(), journalPerm)
	}
}
