// Package backup выполняет операции сервиса: снятие бэкапа, восстановление базы
// и клонирование стенда.
//
// Операция — это задача очереди (см. internal/jobs): она ограничивает
// параллелизм, а ход и итог задачи попадают в журнал. Файлы бэкапов ведёт
// хранилище (см. internal/store), работу с PostgreSQL — пакет pg. Пакет собирает
// эти части вместе и отвечает за порядок: можно ли трогать базу, хватает ли
// места, нужен ли служебный бэкап до восстановления.
package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// PreRestoreTag — метка служебного бэкапа, который снимается перед
// восстановлением: метка видна в списке бэкапов, поэтому страховочные копии
// отличаются от обычных.
const PreRestoreTag = "pre-restore"

// PG — возможности pg.Client, нужные операциям. Интерфейс позволяет проверять
// сценарии операций без PostgreSQL: тесты подменяют клиент.
type PG interface {
	Dump(ctx context.Context, opts pg.DumpOptions, dst io.Writer) error
	Restore(ctx context.Context, opts pg.RestoreOptions, src io.Reader) error
	ServerVersion(ctx context.Context) (pg.Version, error)
	DatabaseExists(ctx context.Context, database, pattern string) (bool, error)
	DatabaseLocale(ctx context.Context, database, pattern string) (pg.Locale, error)
	DatabaseSize(ctx context.Context, database, pattern string) (int64, error)
	Sessions(ctx context.Context, database, pattern string) (int, error)
	TerminateBackends(ctx context.Context, database, pattern string) (int, error)
	CreateDatabase(ctx context.Context, opts pg.CreateDatabaseOptions) error
	AlterDatabaseOwner(ctx context.Context, database, owner, pattern string) error
}

// Result — итог операции: запись журнала и бэкап, с которым работали.
type Result struct {
	// Job — запись журнала задач с состоянием и сообщением.
	Job jobs.Job
	// Backup — созданный или использованный бэкап.
	Backup store.Backup
}

// Options — зависимости сервиса. Все внешние эффекты идут через интерфейсы,
// поэтому операции проверяются без PostgreSQL, без sudo и без реальных каталогов.
type Options struct {
	// Config — конфигурация сервиса.
	Config config.Configuration
	// PG — клиент PostgreSQL.
	PG PG
	// Store — каталог бэкапов.
	Store *store.Store
	// Queue — очередь задач; если не задана, создаётся по конфигу (jobs.*).
	Queue *jobs.Queue
	// Journal — журнал задач; если не задан, журнал пишется в файл
	// <storage.state_dir>/jobs.jsonl, а при пустом state_dir — в память.
	Journal jobs.Log
	// Now подменяет текущее время в тестах.
	Now func() time.Time
}

// Service — операции сервиса.
type Service struct {
	cfg   config.Configuration
	pg    PG
	store *store.Store
	queue *jobs.Queue
	now   func() time.Time
}

// New создаёт сервис. Ошибка означает, что операции не смогут выполняться
// предсказуемо: не задан клиент PostgreSQL или каталог бэкапов.
func New(opts Options) (*Service, error) {
	if opts.PG == nil {
		return nil, errors.New("не задан клиент PostgreSQL")
	}
	if opts.Store == nil {
		return nil, errors.New("не задан каталог бэкапов")
	}
	journal := opts.Journal
	if journal == nil {
		journal = newJournal(opts.Config)
	}
	queue := opts.Queue
	if queue == nil {
		queue = jobs.NewQueue(opts.Config.Jobs.MaxParallel, opts.Config.Jobs.MaxPerDB, journal)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{cfg: opts.Config, pg: opts.PG, store: opts.Store, queue: queue, now: now}, nil
}

// newJournal выбирает журнал задач: файл в служебном каталоге сервиса, а если
// каталог не задан — журнал в памяти. Операции из-за отсутствующего журнала не
// прерываются: важнее выполнить бэкап, чем записать о нём строку.
func newJournal(cfg config.Configuration) jobs.Log {
	if cfg.Storage.StateDir == "" {
		return jobs.NewMemoryLog(cfg.Jobs.Retain)
	}
	return jobs.NewFileLog(filepath.Join(cfg.Storage.StateDir, jobs.DefaultFileName), cfg.Jobs.Retain)
}

// Journal возвращает журнал задач: команда `sqlbrc jobs` показывает его записи.
func (s *Service) Journal() jobs.Log { return s.queue.Journal() }

// Config возвращает конфигурацию сервиса.
func (s *Service) Config() config.Configuration { return s.cfg }

// Jobs возвращает последние записи журнала задач, начиная с новой.
func (s *Service) Jobs(limit int) ([]jobs.Job, error) {
	journal := s.queue.Journal()
	if journal == nil {
		return nil, nil
	}
	return journal.List(limit)
}

// Backups возвращает бэкапы базы, начиная со свежего. Пустое имя базы означает
// все базы хранилища: так работает команда `sqlbrc backups`.
func (s *Service) Backups(database string) ([]store.Backup, error) {
	if database != "" {
		return s.store.List(database)
	}
	databases, err := s.store.ListDatabases()
	if err != nil {
		return nil, err
	}
	backups := make([]store.Backup, 0, len(databases))
	for _, name := range databases {
		listed, err := s.store.List(name)
		if err != nil {
			return nil, err
		}
		backups = append(backups, listed...)
	}
	return backups, nil
}

// RejectedError — операция отвергнута до её начала: сервис не изменил ни базу,
// ни каталог бэкапов. CLI возвращает по такой ошибке код 2, чтобы скрипты
// отличали «отказано по правилам» от «операция не удалась».
type RejectedError struct {
	// Reason — почему операция не начата.
	Reason string
}

// Error реализует error.
func (e *RejectedError) Error() string { return e.Reason }

// Rejected создаёт отказ операции.
func Rejected(format string, args ...any) error {
	return &RejectedError{Reason: fmt.Sprintf(format, args...)}
}

// IsRejected отвечает, отвергнута ли операция до начала.
func IsRejected(err error) bool {
	var rejected *RejectedError
	return errors.As(err, &rejected)
}

// checkOperation проверяет общие условия операции изменения: режим «только
// просмотр» и имя базы. Обе проверки выполняются до первой команды, которая
// может что-то изменить.
func (s *Service) checkOperation(database string) error {
	if s.cfg.Server.ReadOnly {
		return Rejected("сервис в режиме «только просмотр» (server.read_only): операции изменения запрещены")
	}
	return s.checkAllowed(database)
}

// checkAllowed проверяет, что база входит в список обслуживаемых: шаблон имён и
// защищённый список — последняя преграда перед тем, как сервис тронет базу.
func (s *Service) checkAllowed(database string) error {
	if s.cfg.Databases.IsProtected(database) {
		return Rejected("база %s защищена (databases.protected): сервис её не трогает", database)
	}
	if !s.cfg.Databases.Allowed(database) {
		return Rejected("база %s не входит в список обслуживаемых (databases.pattern %s)",
			database, s.cfg.Databases.Pattern)
	}
	return nil
}

// pattern возвращает шаблон имён обслуживаемых баз: он передаётся в argv утилит
// и в служебные запросы, поэтому сужение шаблона в конфиге действует везде.
func (s *Service) pattern() string { return s.cfg.Databases.Pattern }

// retentionOptions собирает политику хранения из конфига.
func (s *Service) retentionOptions() store.RetainOptions {
	return store.RetainOptions{
		KeepLast: s.cfg.Storage.KeepLast,
		KeepDays: s.cfg.Storage.KeepDays,
		Now:      s.now(),
	}
}

// ensureSpace проверяет свободное место перед операцией, которая создаёт файлы.
// Отказ возвращается до запуска pg_dump: дамп, оборванный на середине, оставляет
// в каталоге бесполезный файл. Нехватка места оформляется как отказ операции —
// CLI сообщает о ней кодом 2.
func (s *Service) ensureSpace() error {
	err := s.store.EnsureSpace(s.cfg.Storage.MinFreeSpace, 0)
	if errors.Is(err, store.ErrInsufficientSpace) {
		return Rejected("%v", err)
	}
	return err
}

// joinMessage добавляет замечание к сообщению задачи, не теряя уже сказанное.
func joinMessage(message, note string) string {
	if message == "" {
		return note
	}
	return message + "; " + note
}
