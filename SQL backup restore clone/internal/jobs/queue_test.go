package jobs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// barrier — подмена работы задачи в тестах: задача сообщает о входе и ждёт
// разрешения продолжить. Так проверяется параллелизм без пауз и «магических»
// задержек: о лимите судят по числу вошедших задач, а не по времени.
type barrier struct {
	entered chan string
	proceed chan struct{}
}

// newBarrier создаёт барьер на ожидаемое число одновременных задач.
func newBarrier(size int) *barrier {
	return &barrier{entered: make(chan string, size), proceed: make(chan struct{})}
}

// task возвращает функцию задачи для Queue.Submit.
func (b *barrier) task(database string) func(context.Context, Job) (string, error) {
	return func(context.Context, Job) (string, error) {
		b.entered <- database
		<-b.proceed
		return "готово", nil
	}
}

// release пропускает все задачи к завершению.
func (b *barrier) release() { close(b.proceed) }

// TestQueueLimitsParallelism проверяет общий предел: при двух одновременных
// задачах третья не входит в работу, пока одна из первых не завершится.
func TestQueueLimitsParallelism(t *testing.T) {
	queue := NewQueue(2, 2, nil)
	gate := newBarrier(3)

	var wg sync.WaitGroup
	for index := 0; index < 3; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := queue.Submit(context.Background(),
				Job{Kind: KindBackup, Database: "fse-1234"}, gate.task("fse-1234")); err != nil {
				t.Errorf("Submit: %v", err)
			}
		}()
	}
	// Две задачи вошли в работу — обе могут это сделать, потому что предел равен двум.
	for index := 0; index < 2; index++ {
		select {
		case <-gate.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("задача не дождалась очереди")
		}
	}
	if queue.Running() != 2 {
		t.Errorf("очередь сообщает о %d активных задачах, ожидалось 2", queue.Running())
	}
	// Третья не может войти: слоты освободятся только после завершения первых двух.
	select {
	case database := <-gate.entered:
		t.Fatalf("третья задача вошла в работу раньше времени: %s", database)
	default:
	}
	gate.release()
	wg.Wait()
	if queue.Running() != 0 {
		t.Errorf("после завершения задач в очереди осталось %d активных", queue.Running())
	}
}

// TestQueueLimitsPerDatabase проверяет ограничение по базе: задачи по разным базам
// идут параллельно, а вторая задача по той же базе ждёт.
func TestQueueLimitsPerDatabase(t *testing.T) {
	queue := NewQueue(3, 1, nil)
	gate := newBarrier(3)

	var wg sync.WaitGroup
	for _, database := range []string{"fse-1234", "fse-1234", "fssd-7"} {
		wg.Add(1)
		go func(database string) {
			defer wg.Done()
			if _, err := queue.Submit(context.Background(),
				Job{Kind: KindBackup, Database: database}, gate.task(database)); err != nil {
				t.Errorf("Submit: %v", err)
			}
		}(database)
	}

	entered := make(map[string]int, 2)
	for index := 0; index < 2; index++ {
		select {
		case database := <-gate.entered:
			entered[database]++
		case <-time.After(2 * time.Second):
			t.Fatal("задачи не вошли в работу")
		}
	}
	if entered["fse-1234"] != 1 || entered["fssd-7"] != 1 {
		t.Fatalf("в работе одновременно %v, ожидались задачи по разным базам", entered)
	}
	select {
	case database := <-gate.entered:
		t.Fatalf("вторая задача по базе %s вошла одновременно с первой", database)
	default:
	}
	gate.release()
	wg.Wait()
}

// TestQueueWritesJournal проверяет записи журнала: начало задачи и её итог
// различаются состоянием, а результат попадает в сообщение.
func TestQueueWritesJournal(t *testing.T) {
	journal := NewMemoryLog(0)
	queue := NewQueue(1, 1, journal)

	_, err := queue.Submit(context.Background(), Job{
		Kind:     KindBackup,
		Database: "fse-1234",
		Tag:      "pre-restore",
		Note:     "перед релизом",
	}, func(_ context.Context, job Job) (string, error) {
		if job.ID == "" {
			t.Error("задача выполняется без идентификатора")
		}
		return "бэкап создан: fse-1234/20260921T020000Z", nil
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	stored, err := journal.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("в журнале %d записей, ожидалось 2: %+v", len(stored), stored)
	}
	finished, started := stored[0], stored[1]
	if started.Status != StatusRunning {
		t.Errorf("первая запись о задаче в состоянии %q, ожидалось %q", started.Status, StatusRunning)
	}
	if finished.Status != StatusDone || finished.Message == "" {
		t.Errorf("итог задачи описан неверно: %+v", finished)
	}
	if finished.ID == "" || finished.ID != started.ID {
		t.Errorf("идентификаторы записей различаются: %q и %q", started.ID, finished.ID)
	}
	if finished.Tag != "pre-restore" || finished.Note != "перед релизом" {
		t.Errorf("в журнале потеряны параметры задачи: %+v", finished)
	}
	if finished.FinishedAt.IsZero() || finished.Duration() < 0 {
		t.Errorf("время завершения задачи: %+v", finished)
	}
}

// TestQueueWritesFailure проверяет, что сбой задачи попадает в журнал с текстом
// ошибки: по журналу должно быть видно, почему операция не удалась.
func TestQueueWritesFailure(t *testing.T) {
	journal := NewMemoryLog(0)
	queue := NewQueue(1, 1, journal)
	failure := errors.New("pg_dump: связь с сервером потеряна")

	_, err := queue.Submit(context.Background(), Job{Kind: KindRestore, Database: "fse-1234"},
		func(context.Context, Job) (string, error) {
			return "", failure
		})
	if !errors.Is(err, failure) {
		t.Fatalf("Submit вернула ошибку %v, ожидалась исходная", err)
	}
	stored, err := journal.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 2 || stored[0].Status != StatusFailed {
		t.Fatalf("в журнале нет записи о сбое: %+v", stored)
	}
	if !strings.Contains(stored[0].Error, "связь с сервером потеряна") {
		t.Errorf("текст ошибки в журнале: %q", stored[0].Error)
	}
}

// TestQueueWaitsForSlotWithContext проверяет ожидание места: пока слот занят,
// задача ждёт, а отмена контекста прекращает ожидание и работу не запускает.
func TestQueueWaitsForSlotWithContext(t *testing.T) {
	queue := NewQueue(1, 1, nil)
	gate := newBarrier(1)
	started := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := queue.Submit(context.Background(), Job{Kind: KindBackup, Database: "fse-1234"},
			gate.task("fse-1234")); err != nil {
			t.Errorf("Submit: %v", err)
		}
	}()
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("первая задача не вошла в работу")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := queue.Submit(ctx, Job{Kind: KindBackup, Database: "fssd-7"},
		func(context.Context, Job) (string, error) {
			close(started)
			return "", nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидание в очереди вернуло %v, ожидалась отмена контекста", err)
	}
	select {
	case <-started:
		t.Error("задача выполнена несмотря на отмену контекста")
	default:
	}
	gate.release()
	wg.Wait()
}

// TestQueueNormalizesLimits проверяет, что очередь не принимает бессмысленные
// пределы: нули заменяются значениями по умолчанию, а предел по базе не может
// быть больше общего предела.
func TestQueueNormalizesLimits(t *testing.T) {
	queue := NewQueue(0, 0, nil)
	if queue.MaxParallel() != DefaultMaxParallel || queue.MaxPerDB() != DefaultMaxPerDB {
		t.Errorf("пределы по умолчанию: %d/%d", queue.MaxParallel(), queue.MaxPerDB())
	}
	queue = NewQueue(2, 8, nil)
	if queue.MaxPerDB() != 2 {
		t.Errorf("предел по базе %d, ожидался общий предел 2", queue.MaxPerDB())
	}
}

// TestNewIDUnique проверяет идентификатор задачи: в одну секунду он должен
// различаться, иначе в журнале не отличить две задачи друг от друга.
func TestNewIDUnique(t *testing.T) {
	at := time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC)
	first, second := NewID(at), NewID(at)
	if first == second {
		t.Errorf("идентификаторы совпали: %s", first)
	}
	if !strings.HasPrefix(first, "20260921T020000Z-") {
		t.Errorf("идентификатор %q не содержит метку времени", first)
	}
}
