package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultFileName — имя файла журнала в служебном каталоге сервиса
// (storage.state_dir). Журнал лежит отдельно от каталога бэкапов: политика
// хранения удаляет файлы в storage.dir, и записи журнала туда попадать не должны.
const DefaultFileName = "jobs.jsonl"

// journalPerm — права файла журнала: записи содержат имена баз и команды, поэтому
// файл доступен только пользователю сервиса и root.
const journalPerm os.FileMode = 0o600

// Log — журнал задач. Интерфейс отделяет очередь от способа хранения: сервис
// пишет журнал в файл, тесты — в память.
type Log interface {
	// Write добавляет запись в журнал.
	Write(job Job) error
	// List возвращает последние записи, начиная с новой.
	List(limit int) ([]Job, error)
}

// MemoryLog — журнал в памяти: нужен тестам сервиса и CLI, а также случаю, когда
// журнал на диске недоступен (state_dir не смонтирован) — операция из-за этого
// прерываться не должна.
type MemoryLog struct {
	mu     sync.Mutex
	retain int
	jobs   []Job
}

// NewMemoryLog создаёт журнал в памяти с пределом записей.
func NewMemoryLog(retain int) *MemoryLog { return &MemoryLog{retain: retain} }

// Write добавляет запись.
func (l *MemoryLog) Write(job Job) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jobs = append(l.jobs, job)
	if l.retain > 0 && len(l.jobs) > l.retain {
		l.jobs = append([]Job(nil), l.jobs[len(l.jobs)-l.retain:]...)
	}
	return nil
}

// List возвращает последние записи, начиная с новой.
func (l *MemoryLog) List(limit int) ([]Job, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return lastJobs(l.jobs, limit), nil
}

// FileLog — журнал задач в файле формата JSONL: одна задача — одна строка JSON.
// Формат выбран потому, что дописывание строки не требует перечитывать файл, а
// повреждённая строка не ломает остальные записи.
type FileLog struct {
	path   string
	retain int
	mu     sync.Mutex
}

// NewFileLog создаёт журнал в файле path. retain — сколько последних записей
// хранить (jobs.retain): файл журнала не должен расти бесконечно.
func NewFileLog(path string, retain int) *FileLog {
	return &FileLog{path: path, retain: retain}
}

// Path возвращает путь к файлу журнала.
func (l *FileLog) Path() string { return l.path }

// Write добавляет запись в журнал. Запись перезаписывает файл целиком, когда
// число записей превышает предел хранения: так файл не растёт, а журнал
// остаётся читаемым одним списком.
func (l *FileLog) Write(job Job) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	existing, err := l.read()
	if err != nil {
		return err
	}
	existing = append(existing, job)
	if l.retain > 0 && len(existing) > l.retain {
		existing = append([]Job(nil), existing[len(existing)-l.retain:]...)
	}
	return l.rewrite(existing)
}

// List возвращает последние записи журнала, начиная с новой. limit <= 0
// означает «все записи».
func (l *FileLog) List(limit int) ([]Job, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	stored, err := l.read()
	if err != nil {
		return nil, err
	}
	return lastJobs(stored, limit), nil
}

// read читает журнал. Повреждённые строки пропускаются: журнал может быть
// дописан не до конца, если процесс упал во время записи, и из-за одной битой
// строки терять историю нельзя.
func (l *FileLog) read() ([]Job, error) {
	raw, err := os.ReadFile(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("не удалось прочитать журнал задач %s: %w", l.path, err)
	}
	lines := strings.Split(string(raw), "\n")
	stored := make([]Job, 0, len(lines))
	for _, line := range lines {
		if job, ok := ParseJobLine(line); ok {
			stored = append(stored, job)
		}
	}
	return stored, nil
}

// rewrite записывает журнал целиком через временный файл: обрыв записи не
// оставляет от журнала половину, а готовый файл появляется одним переименованием.
func (l *FileLog) rewrite(stored []Job) error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("не удалось создать каталог журнала задач: %w", err)
	}
	temp := l.path + ".tmp"
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, journalPerm)
	if err != nil {
		return fmt.Errorf("не удалось записать журнал задач %s: %w", l.path, err)
	}
	writer := json.NewEncoder(file)
	for _, job := range stored {
		if err := writer.Encode(job); err != nil {
			_ = file.Close()
			_ = os.Remove(temp)
			return fmt.Errorf("не удалось записать журнал задач %s: %w", l.path, err)
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("не удалось записать журнал задач %s: %w", l.path, err)
	}
	if err := os.Rename(temp, l.path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("не удалось заменить журнал задач %s: %w", l.path, err)
	}
	return nil
}

// ParseJobLine разбирает строку журнала. Пустые и повреждённые строки дают
// признак отказа: журнал дописывается построчно, и последняя строка может
// остаться недописанной.
//
// Функция работает с внешними данными (файл мог быть исправлен вручную), на
// любом вводе она обязана вернуть запись или отказ (см. FuzzParseJobLine).
func ParseJobLine(line string) (Job, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return Job{}, false
	}
	job := Job{}
	if err := json.Unmarshal([]byte(trimmed), &job); err != nil {
		return Job{}, false
	}
	// Запись без идентификатора или с неизвестным видом задачи бесполезна: по ней
	// нельзя понять, что делал сервис. Вид проверяется по списку: значение приходит
	// из файла, который мог быть исправлен вручную.
	if job.ID == "" || !IsKnownKind(job.Kind) {
		return Job{}, false
	}
	return job, true
}

// lastJobs возвращает последние записи, начиная с новой. limit <= 0 — все записи.
func lastJobs(stored []Job, limit int) []Job {
	if limit <= 0 || limit > len(stored) {
		limit = len(stored)
	}
	result := make([]Job, 0, limit)
	for index := len(stored) - 1; index >= len(stored)-limit; index-- {
		result = append(result, stored[index])
	}
	return result
}
