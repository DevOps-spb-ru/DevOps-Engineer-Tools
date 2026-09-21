//go:build windows

package doctor

import "os"

// FreeSpace на Windows не реализуется: сервис работает на Debian (Debian 12),
// а разработка и тесты идут и на Windows. Проверка отмечается как недоступная,
// а не падает и не мешает остальным проверкам.
func (OSFS) FreeSpace(path string) (int64, int64, error) {
	return 0, 0, ErrUnsupported
}

// Owner на Windows не определяется: имена владельцев приходят из ACL,
// а сообщения проверок рассчитаны на POSIX-права сервера.
func (OSFS) Owner(os.FileInfo) string { return "" }
