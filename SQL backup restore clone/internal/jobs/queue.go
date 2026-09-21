package jobs

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Пределы параллелизма по умолчанию (jobs.max_parallel, jobs.max_per_db из
// конфига). Значения нужны, когда очередь создаётся без конфига — например, в
// проверках CLI.
const (
	DefaultMaxParallel = 2
	DefaultMaxPerDB    = 1
)

// Queue — очередь задач: ограничивает число одновременных задач и число задач по
// одной базе, а также записывает начало и итог каждой задачи в журнал.
//
// Ограничения нужны потому, что восстановление очищает объекты базы, а дамп
// читает её целиком: два процесса по одной базе мешают друг другу, а несколько
// больших дампов одновременно занимают диск и процессор.
type Queue struct {
	maxParallel int
	maxPerDB    int
	slots       chan struct{}
	journal     Log
	now         func() time.Time
	newID       func(time.Time) string

	mu      sync.Mutex
	dbSlots map[string]chan struct{}
}

// NewQueue создаёт очередь. maxParallel и maxPerDB берутся из конфига
// (jobs.max_parallel и jobs.max_per_db); значения меньше единицы заменяются
// значениями по умолчанию, а maxPerDB приводится к maxParallel — иначе
// ограничение по базе было бы слабее общего и ничего не ограничивало.
// journal может быть nil: тогда задачи выполняются без журнала.
func NewQueue(maxParallel, maxPerDB int, journal Log) *Queue {
	if maxParallel < 1 {
		maxParallel = DefaultMaxParallel
	}
	if maxPerDB < 1 {
		maxPerDB = DefaultMaxPerDB
	}
	if maxPerDB > maxParallel {
		maxPerDB = maxParallel
	}
	return &Queue{
		maxParallel: maxParallel,
		maxPerDB:    maxPerDB,
		slots:       make(chan struct{}, maxParallel),
		journal:     journal,
		now:         time.Now,
		newID:       NewID,
		dbSlots:     make(map[string]chan struct{}),
	}
}

// MaxParallel возвращает предел одновременных задач.
func (q *Queue) MaxParallel() int { return q.maxParallel }

// MaxPerDB возвращает предел задач по одной базе.
func (q *Queue) MaxPerDB() int { return q.maxPerDB }

// Journal возвращает журнал задач (может быть nil).
func (q *Queue) Journal() Log { return q.journal }

// Running возвращает число задач, выполняющихся прямо сейчас. Значение нужно
// отчётам и проверкам: по нему видно, что очередь действительно ограничивает
// параллелизм.
func (q *Queue) Running() int { return len(q.slots) }

// Acquire занимает место в очереди для базы database и возвращает функцию
// освобождения. Если места нет, вызов ждёт: ждать или не ждать, решает контекст,
// поэтому отменённый контекст останавливает ожидание с его же ошибкой.
func (q *Queue) Acquire(ctx context.Context, database string) (func(), error) {
	dbSlot := q.databaseSlot(database)
	// Порядок захвата (сначала база, затем общий слот) одинаков для всех задач,
	// поэтому взаимной блокировки не возникает.
	select {
	case dbSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("не удалось дождаться очереди для базы %s: %w", database, ctx.Err())
	}
	select {
	case q.slots <- struct{}{}:
	case <-ctx.Done():
		<-dbSlot
		return nil, fmt.Errorf("не удалось дождаться очереди для базы %s: %w", database, ctx.Err())
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-q.slots
			<-dbSlot
		})
	}, nil
}

// databaseSlot возвращает семафор задач по базе, создавая его при первом
// обращении: баз в кластере много, а очередь нужна только тем, по которым
// реально идут задачи.
func (q *Queue) databaseSlot(database string) chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	slot, ok := q.dbSlots[database]
	if !ok {
		slot = make(chan struct{}, q.maxPerDB)
		q.dbSlots[database] = slot
	}
	return slot
}

// Submit выполняет задачу: занимает место в очереди, пишет начало и итог в
// журнал и возвращает заполненную запись журнала. Работа задачи описана функцией
// fn, которая получает заполненную запись задачи (идентификатор нужен, например,
// чтобы связать файл бэкапа с записью журнала) и возвращает итоговое сообщение и
// ошибку.
func (q *Queue) Submit(ctx context.Context, job Job, fn func(context.Context, Job) (string, error)) (Job, error) {
	release, err := q.Acquire(ctx, job.Database)
	if err != nil {
		return job, err
	}
	defer release()

	startedAt := q.now()
	job.ID = q.newID(startedAt)
	job.Status = StatusRunning
	job.StartedAt = startedAt
	_ = q.write(job)

	message, runErr := fn(ctx, job)
	job.SetResult(q.now(), message, runErr)
	if logErr := q.write(job); logErr != nil {
		// Сбой журнала не отменяет саму операцию: результаты бэкапа важнее записи
		// о нём. Причина видна в итоговом сообщении задачи.
		job.Message = joinMessage(job.Message, "журнал задач недоступен: "+logErr.Error())
	}
	return job, runErr
}

// write записывает задачу в журнал.
func (q *Queue) write(job Job) error {
	if q.journal == nil {
		return nil
	}
	if err := q.journal.Write(job); err != nil {
		return err
	}
	return nil
}

// joinMessage добавляет замечание к сообщению, не теряя уже сказанное.
func joinMessage(message, note string) string {
	if message == "" {
		return note
	}
	return message + "; " + note
}
