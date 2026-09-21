//go:build windows

package store

// FreeSpace на Windows не реализуется: сервис работает на Debian 12, а разработка
// и тесты идут и на Windows. Проверка места отмечается как недоступная
// (ErrUnsupported), а не мешает операции: см. Store.EnsureSpace.
func (OSFS) FreeSpace(string) (int64, int64, error) {
	return 0, 0, ErrUnsupported
}
