// Package jobs описывает фоновые задачи сервиса: очередь с ограничением
// параллелизма и журнал задач в формате JSONL.
//
// Задача (бэкап, восстановление, клонирование) выполняется через Queue: она
// следит, чтобы одновременно шло не больше jobs.max_parallel задач и не больше
// jobs.max_per_db задач по одной базе, и записывает ход выполнения в журнал.
// Журнал отвечает на вопрос «что сервис делал и чем это закончилось»: он нужен
// администратору, а позже — веб-интерфейсу.
package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Kind — вид задачи.
type Kind string

const (
	// KindBackup — снятие бэкапа.
	KindBackup Kind = "backup"
	// KindRestore — восстановление базы из бэкапа.
	KindRestore Kind = "restore"
	// KindClone — клонирование базы из бэкапа в новый стенд.
	KindClone Kind = "clone"
	// KindPrune — уборка старых бэкапов по политике хранения.
	KindPrune Kind = "prune"
)

// Status — состояние задачи.
type Status string

const (
	// StatusRunning — задача выполняется.
	StatusRunning Status = "running"
	// StatusDone — задача завершена успешно.
	StatusDone Status = "done"
	// StatusFailed — задача завершилась ошибкой.
	StatusFailed Status = "failed"
)

// Job — запись журнала задач: что делали, с какой базой и чем закончилось.
type Job struct {
	// ID — идентификатор задачи вида 20260921T020000Z-a1b2c3d4.
	ID string `json:"id"`
	// Kind — вид задачи.
	Kind Kind `json:"kind"`
	// Database — база, с которой работает задача (для клонирования — источник).
	Database string `json:"database"`
	// Target — база-приёмник (заполняется при клонировании и восстановлении).
	Target string `json:"target,omitempty"`
	// BackupID — бэкап, из которого восстанавливают или клонируют.
	BackupID string `json:"backup_id,omitempty"`
	// Tag — метка снимаемого бэкапа (pre-restore и подобные).
	Tag string `json:"tag,omitempty"`
	// Note — примечание к задаче.
	Note string `json:"note,omitempty"`
	// Operator — кто запустил задачу (CLI, веб-пользователь).
	Operator string `json:"operator,omitempty"`
	// Status — состояние задачи.
	Status Status `json:"status"`
	// StartedAt — время начала.
	StartedAt time.Time `json:"started_at"`
	// FinishedAt — время завершения (пустое у незавершённой задачи).
	FinishedAt time.Time `json:"finished_at,omitempty"`
	// DurationMS — сколько выполнялась задача, в миллисекундах.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// Message — итог: путь к созданному бэкапу, число завершённых подключений
	// и другой результат, который полезно видеть в списке задач.
	Message string `json:"message,omitempty"`
	// Error — текст ошибки, если задача не выполнена.
	Error string `json:"error,omitempty"`
}

// Duration возвращает длительность задачи.
func (j Job) Duration() time.Duration { return time.Duration(j.DurationMS) * time.Millisecond }

// IsKnownKind отвечает, поддерживается ли вид задачи. Проверка нужна разбору
// журнала: запись пишет сам сервис, поэтому неизвестный вид означает испорченную
// строку, а не новую операцию.
func IsKnownKind(kind Kind) bool {
	switch kind {
	case KindBackup, KindRestore, KindClone, KindPrune:
		return true
	default:
		return false
	}
}

// Running отвечает, выполняется ли задача прямо сейчас.
func (j Job) Running() bool { return j.Status == StatusRunning }

// SetResult заполняет итог задачи: состояние, время завершения и сообщение или
// ошибку. Метод один на все виды задач, чтобы журнал заполнялся одинаково.
func (j *Job) SetResult(doneAt time.Time, message string, err error) {
	j.Status = StatusDone
	j.Message = message
	j.Error = ""
	if err != nil {
		j.Status = StatusFailed
		j.Error = err.Error()
	}
	j.FinishedAt = doneAt
	j.DurationMS = doneAt.Sub(j.StartedAt).Milliseconds()
}

// NewID собирает идентификатор задачи: метка времени в UTC плюс случайный
// суффикс. Времени в имени мало — две задачи, начатые в одну секунду, должны
// различаться, иначе в журнале их не отличить друг от друга.
func NewID(startedAt time.Time) string {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		// Без случайного суффикса задача всё равно остаётся опознаваемой по
		// времени: идентификатор нужен для журнала, а не для безопасности.
		return startedAt.UTC().Format("20060102T150405Z") + "-00000000"
	}
	return startedAt.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix)
}
