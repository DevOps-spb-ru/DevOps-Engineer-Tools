package store

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// RetainOptions — политика хранения из storage.keep_last и storage.keep_days.
type RetainOptions struct {
	// KeepLast — сколько последних бэкапов каждой базы хранить (0 — без ограничения).
	KeepLast int
	// KeepDays — сколько дней хранить бэкап (0 — без ограничения по возрасту).
	KeepDays int
	// Now — текущее время (подменяется в тестах).
	Now time.Time
}

// enabled отвечает, задана ли политика хотя бы одним ограничением.
func (o RetainOptions) enabled() bool { return o.KeepLast > 0 || o.KeepDays > 0 }

// maxAge возвращает предельный возраст бэкапа. Ограничение 1..3650 дней проверяет
// конфиг, поэтому отрицательные значения сюда не попадают.
func (o RetainOptions) maxAge() time.Duration {
	return time.Duration(o.KeepDays) * 24 * time.Hour
}

// Plan делит бэкапы на те, что остаются, и те, что нужно удалить. Функция чистая:
// работает по метаданным и ничего не удаляет, поэтому проверяется табличным
// тестом, а не прогоном с реальными файлами.
//
// Бэкап остаётся, если он входит в последние KeepLast и снят не раньше KeepDays
// дней назад: оба ограничения из конфига действуют вместе («последние 7 бэкапов,
// но не старше 14 дней»). Бэкапы с неразобранной меткой времени не удаляются
// никогда: неизвестно ни их место в очереди, ни возраст.
func Plan(backups []Backup, opts RetainOptions) (keep, remove []Backup) {
	if !opts.enabled() {
		// Политика не задана: удалять всё при пустом конфиге нельзя.
		return append([]Backup(nil), backups...), nil
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	ordered := append([]Backup(nil), backups...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].CreatedAt.After(ordered[j].CreatedAt)
	})
	for index, backup := range ordered {
		age := now.Sub(backup.CreatedAt)
		switch {
		case backup.CreatedAt.IsZero():
			// Время неизвестно: под политику хранения такой бэкап не подпадает.
			keep = append(keep, backup)
		case opts.KeepLast > 0 && index >= opts.KeepLast:
			remove = append(remove, backup)
		case opts.KeepDays > 0 && age > opts.maxAge():
			remove = append(remove, backup)
		default:
			keep = append(keep, backup)
		}
	}
	return keep, remove
}

// ApplyRetention применяет политику к каталогу базы: удаляет архивы и их файлы
// метаданных, возвращая список удалённого. Ошибки удаления собираются целиком и
// не прерывают уборку — иначе один нечитаемый файл блокировал бы всю ротацию.
func (s *Store) ApplyRetention(database string, opts RetainOptions) ([]Backup, error) {
	backups, err := s.List(database)
	if err != nil {
		return nil, err
	}
	if !opts.enabled() {
		return nil, nil
	}
	_, remove := Plan(backups, opts)
	removed := make([]Backup, 0, len(remove))
	problems := make([]string, 0, len(remove))
	for _, backup := range remove {
		failed := false
		for _, path := range backup.Files() {
			if err := s.fs.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				problems = append(problems, fmt.Sprintf("%s: %v", path, err))
				failed = true
			}
		}
		if !failed {
			removed = append(removed, backup)
		}
	}
	if len(problems) > 0 {
		return removed, fmt.Errorf("часть старых бэкапов удалить не удалось: %s",
			strings.Join(problems, "; "))
	}
	return removed, nil
}

// Remove удаляет бэкап вместе с файлом метаданных. Удаление отдельного бэкапа
// нужно CLI (`backups prune` удаляет по политике через ApplyRetention).
func (s *Store) Remove(backup Backup) error {
	problems := make([]string, 0, 2)
	for _, path := range backup.Files() {
		if err := s.fs.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("не удалось удалить бэкап %s: %s", backup.ID, strings.Join(problems, "; "))
	}
	return nil
}
