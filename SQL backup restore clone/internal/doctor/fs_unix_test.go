//go:build !windows

package doctor

import (
	"math"
	"strings"
	"testing"
)

// TestBlocksToBytes проверяет перевод блоков в байты: без проверки границы
// приведение uint64 к int64 даёт отрицательное значение, и проверка свободного
// места начинает пропускать дампы, которые не поместятся на диск.
func TestBlocksToBytes(t *testing.T) {
	tests := []struct {
		name      string
		blocks    uint64
		blockSize int64
		want      int64
		wantErr   bool
	}{
		{name: "обычный случай", blocks: 1000, blockSize: 4096, want: 4_096_000},
		{name: "нет места", blocks: 0, blockSize: 4096, want: 0},
		{name: "нулевой размер блока", blocks: 10, blockSize: 0, wantErr: true},
		{name: "отрицательный размер блока", blocks: 10, blockSize: -4096, wantErr: true},
		{name: "переполнение", blocks: math.MaxUint64, blockSize: 4096, want: math.MaxInt64},
		{name: "граница без переполнения", blocks: uint64(math.MaxInt64) / 4096, blockSize: 4096, want: math.MaxInt64 / 4096 * 4096},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := blocksToBytes(test.blocks, test.blockSize)
			if test.wantErr {
				if err == nil {
					t.Fatalf("blocksToBytes(%d, %d) = %d, ожидалась ошибка", test.blocks, test.blockSize, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("blocksToBytes(%d, %d): неожиданная ошибка: %v", test.blocks, test.blockSize, err)
			}
			if got != test.want {
				t.Errorf("blocksToBytes(%d, %d) = %d, ожидалось %d", test.blocks, test.blockSize, got, test.want)
			}
			if got < 0 {
				t.Errorf("отрицательное значение %d для %d блоков", got, test.blocks)
			}
		})
	}
}

// TestOSFSFreeSpace проверяет расчёт места на реальном каталоге: тест выполняется
// только на Linux (целевая платформа сервиса) и проверяет, что значения неотрицательны
// и согласованы между собой.
func TestOSFSFreeSpace(t *testing.T) {
	dir := t.TempDir()
	free, total, err := (OSFS{}).FreeSpace(dir)
	if err != nil {
		t.Fatalf("FreeSpace(%q): %v", dir, err)
	}
	if free < 0 || total < 0 {
		t.Errorf("отрицательные значения: свободно %d, всего %d", free, total)
	}
	if total > 0 && free > total {
		t.Errorf("свободного места (%d) больше общего (%d)", free, total)
	}
}

// TestOSFSOwner проверяет определение владельца файла: в сообщениях проверок
// видно, принадлежит ли каталог пользователю сервиса.
func TestOSFSOwner(t *testing.T) {
	dir := t.TempDir()
	info, err := (OSFS{}).Stat(dir)
	if err != nil {
		t.Fatalf("Stat(%q): %v", dir, err)
	}
	owner := (OSFS{}).Owner(info)
	if owner == "" {
		t.Skip("имя владельца не определено: в контейнере может не быть /etc/passwd")
	}
	if strings.ContainsAny(owner, " \t\n") {
		t.Errorf("имя владельца содержит пробелы: %q", owner)
	}
}

// TestOSFSWriteProbe проверяет проверку записи: файл создаётся и удаляется,
// каталог остаётся без мусора.
func TestOSFSWriteProbe(t *testing.T) {
	dir := t.TempDir()
	if err := (OSFS{}).WriteProbe(dir); err != nil {
		t.Fatalf("WriteProbe(%q): %v", dir, err)
	}
	entries, err := (OSFS{}).ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("в каталоге остались файлы: %+v", entries)
	}

	missing := dir + "/нет-такого-каталога"
	if err := (OSFS{}).WriteProbe(missing); err == nil {
		t.Errorf("WriteProbe(%q) вернула nil для отсутствующего каталога", missing)
	}
}
