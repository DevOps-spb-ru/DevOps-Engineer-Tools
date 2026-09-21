//go:build !windows

package doctor

import (
	"errors"
	"math"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// FreeSpace считает свободное и общее место на разделе через statfs: сервис
// не должен начинать дамп, если места меньше порога storage.min_free_space.
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
// проверки даёт отрицательное значение, а «отрицательное свободное место» в
// сравнении с порогом выглядело бы как «места не хватает» или наоборот «хватает»:
// поэтому граница проверяется явно, а переполнение трактуется как очень большой объём.
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

// Owner возвращает имя владельца файла: в сообщениях проверок видно, кому
// принадлежит каталог бэкапов (root и пользователь сервиса — разные случаи).
func (OSFS) Owner(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	owner, err := user.LookupId(strconv.FormatUint(uint64(stat.Uid), 10))
	if err != nil {
		return ""
	}
	return owner.Username
}
