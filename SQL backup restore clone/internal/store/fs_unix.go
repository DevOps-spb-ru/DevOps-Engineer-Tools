//go:build !windows

package store

import (
	"errors"
	"math"
	"syscall"
)

// FreeSpace считает свободное и общее место на разделе с каталогом бэкапов
// через statfs: без этой проверки дамп начинается при остатке меньше
// storage.min_free_space и обрывается на середине.
func (OSFS) FreeSpace(path string) (int64, int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	// Bsize на разных платформах разного типа, поэтому приведение к int64 обязательно.
	free, err := blocksToBytes(uint64(stat.Bavail), int64(stat.Bsize))
	if err != nil {
		return 0, 0, err
	}
	total, err := blocksToBytes(uint64(stat.Blocks), int64(stat.Bsize))
	if err != nil {
		return 0, 0, err
	}
	return free, total, nil
}

// blocksToBytes переводит число блоков в байты. Приведение uint64 к int64 без
// проверки даёт отрицательное значение, и «отрицательное свободное место» прошло
// бы сравнение с порогом как достаточное: поэтому граница проверяется явно,
// а переполнение трактуется как очень большой объём.
func blocksToBytes(blocks uint64, blockSize int64) (int64, error) {
	if blockSize <= 0 {
		return 0, errors.New("некорректный размер блока файловой системы")
	}
	if blocks > uint64(math.MaxInt64)/uint64(blockSize) {
		return math.MaxInt64, nil
	}
	// #nosec G115 -- значение заведомо не больше MaxInt64/blockSize, проверка строкой выше.
	return int64(blocks) * blockSize, nil
}
