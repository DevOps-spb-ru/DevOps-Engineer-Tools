package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Partials возвращает незавершённые записи базы: файлы <имя>.dump.part,
// оставшиеся после сбоя дампа или процесса. В список бэкапов они не попадают,
// но место занимают, поэтому их убирает SweepPartials.
func (s *Store) Partials(database string) ([]string, error) {
	dir, err := s.BackupDir(database)
	if err != nil {
		return nil, err
	}
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("не удалось прочитать каталог бэкапов %s: %w", dir, err)
	}
	partials := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), PartialExt) {
			continue
		}
		partials = append(partials, filepath.Join(dir, entry.Name()))
	}
	return partials, nil
}

// SweepPartials удаляет незавершённые записи старше olderThan и возвращает их
// список. Возраст берётся по времени изменения файла, а не по имени: активная
// запись моложе указанного срока не трогается — дамп большого стенда идёт
// минутами, и уборка не должна отбирать файл у работающего pg_dump.
func (s *Store) SweepPartials(database string, olderThan time.Duration) ([]string, error) {
	partials, err := s.Partials(database)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	removed := make([]string, 0, len(partials))
	problems := make([]string, 0, len(partials))
	for _, path := range partials {
		info, err := s.fs.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if olderThan > 0 && now.Sub(info.ModTime()) < olderThan {
			continue
		}
		if err := s.fs.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		removed = append(removed, path)
	}
	if len(problems) > 0 {
		return removed, fmt.Errorf("часть незавершённых записей убрать не удалось: %s",
			strings.Join(problems, "; "))
	}
	return removed, nil
}
