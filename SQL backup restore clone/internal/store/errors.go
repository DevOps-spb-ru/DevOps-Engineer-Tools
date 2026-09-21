package store

import (
	"errors"
	"fmt"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
)

// ErrUnsupported возвращается на платформах, где операцию выполнить нельзя:
// сервис работает на Debian, а разработка и тесты идут и на Windows.
var ErrUnsupported = errors.New("операция не поддерживается на этой платформе")

// ErrNotFound — в каталоге сервиса нет запрошенного бэкапа. Ошибка отделена от
// прочих, чтобы вызывающий код отличал «файла нет» от «файл не читается».
var ErrNotFound = errors.New("бэкап не найден в каталоге сервиса")

// ErrInsufficientSpace — на разделе с бэкапами меньше места, чем разрешает
// storage.min_free_space. Дамп в этом случае не начинается: иначе место кончится
// на середине записи и в каталоге останется битый файл.
var ErrInsufficientSpace = errors.New("недостаточно свободного места для бэкапа")

// InsufficientSpaceError — подробности отказа: сколько места осталось и сколько
// требуется. Значения печатаются человеку, поэтому хранятся в байтах.
type InsufficientSpaceError struct {
	// Free — свободно на разделе с каталогом бэкапов.
	Free int64
	// Need — порог из конфига плюс ожидаемый размер нового файла.
	Need int64
	// Path — каталог, на разделе которого считалось место.
	Path string
}

// Error реализует error.
func (e *InsufficientSpaceError) Error() string {
	return fmt.Sprintf("свободного места в %s недостаточно: свободно %s, требуется минимум %s",
		e.Path, bytesize.HumanSize(e.Free), bytesize.HumanSize(e.Need))
}

// Unwrap позволяет проверять отказ через errors.Is(err, ErrInsufficientSpace):
// вызывающий код различает «мало места» и прочие ошибки записи.
func (e *InsufficientSpaceError) Unwrap() error { return ErrInsufficientSpace }
