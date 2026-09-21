package backup

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// testNow — момент времени, от которого строятся метки бэкапов в тестах.
var testNow = time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC)

// fakePG — подмена клиента PostgreSQL: тесты проверяют порядок и параметры
// вызовов операций, не поднимая сервер. Тесты не должны зависеть от
// установленного PostgreSQL — это требование правил проекта.
type fakePG struct {
	// calls — выполненные действия в порядке вызова.
	calls []string
	// existing — базы, которые «есть» в кластере.
	existing map[string]bool
	// locale — параметры баз (отдаются для любой базы).
	locale pg.Locale
	// size — размер базы в байтах.
	size int64
	// sessions — число активных подключений к базе.
	sessions int
	// dumpData — что «снимает» pg_dump.
	dumpData string
	// dumped, restored, created — параметры выполненных операций.
	dumped   []pg.DumpOptions
	restored []pg.RestoreOptions
	created  []pg.CreateDatabaseOptions
	// restoreInput — данные, которые pg_restore прочитал из stdin.
	restoreInput string
	// dumpErr, restoreErr, createErr, terminateErr — подмена сбоев.
	dumpErr      error
	restoreErr   error
	createErr    error
	terminateErr error
	// databases, listErr — базы кластера для команды `backup --all`.
	databases []pg.Database
	listErr   error
}

// ListDatabases «возвращает» базы кластера: список нужен `backup --all`.
func (f *fakePG) ListDatabases(_ context.Context) ([]pg.Database, error) {
	f.record("list-databases")
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.databases, nil
}

// newFakePG создаёт подмену с подготовленной базой fse-1234.
func newFakePG() *fakePG {
	return &fakePG{
		existing: map[string]bool{"fse-1234": true},
		locale:   pg.Locale{Encoding: "UTF8", Collate: "ru_RU.UTF-8", Ctype: "ru_RU.UTF-8", Owner: "postgres"},
		size:     1 << 20,
		dumpData: "PGDMP-тестовый архив",
	}
}

// record сохраняет вызов.
func (f *fakePG) record(action string) { f.calls = append(f.calls, action) }

// Dump «снимает» дамп в переданный writer.
func (f *fakePG) Dump(_ context.Context, opts pg.DumpOptions, dst io.Writer) error {
	f.record("dump " + opts.Database)
	f.dumped = append(f.dumped, opts)
	if f.dumpErr != nil {
		return f.dumpErr
	}
	_, err := io.WriteString(dst, f.dumpData)
	return err
}

// Restore «разворачивает» архив: читает stdin, как это делает pg_restore.
func (f *fakePG) Restore(_ context.Context, opts pg.RestoreOptions, src io.Reader) error {
	f.record("restore " + opts.Database)
	f.restored = append(f.restored, opts)
	if f.restoreErr != nil {
		return f.restoreErr
	}
	if src == nil {
		return nil
	}
	raw, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	f.restoreInput = string(raw)
	return nil
}

// ServerVersion возвращает версию сервера.
func (f *fakePG) ServerVersion(context.Context) (pg.Version, error) {
	return pg.Version{Major: 15, Minor: 14, Raw: "15.14"}, nil
}

// DatabaseExists отвечает, есть ли база в кластере.
func (f *fakePG) DatabaseExists(_ context.Context, database, _ string) (bool, error) {
	return f.existing[database], nil
}

// DatabaseLocale возвращает параметры базы.
func (f *fakePG) DatabaseLocale(context.Context, string, string) (pg.Locale, error) {
	return f.locale, nil
}

// DatabaseSize возвращает размер базы.
func (f *fakePG) DatabaseSize(context.Context, string, string) (int64, error) {
	return f.size, nil
}

// Sessions возвращает число активных подключений.
func (f *fakePG) Sessions(context.Context, string, string) (int, error) { return f.sessions, nil }

// TerminateBackends «завершает» подключения.
func (f *fakePG) TerminateBackends(_ context.Context, database, _ string) (int, error) {
	f.record("terminate " + database)
	if f.terminateErr != nil {
		return 0, f.terminateErr
	}
	return f.sessions, nil
}

// CreateDatabase «создаёт» базу.
func (f *fakePG) CreateDatabase(_ context.Context, opts pg.CreateDatabaseOptions) error {
	f.record("create " + opts.Database)
	f.created = append(f.created, opts)
	if f.createErr != nil {
		return f.createErr
	}
	f.existing[opts.Database] = true
	return nil
}

// AlterDatabaseOwner «меняет» владельца базы.
func (f *fakePG) AlterDatabaseOwner(_ context.Context, database, owner, _ string) error {
	f.record("alter " + database + " owner " + owner)
	return nil
}

// called отвечает, выполнялось ли действие (по началу записи).
func (f *fakePG) called(action string) bool {
	for _, call := range f.calls {
		if strings.HasPrefix(call, action) {
			return true
		}
	}
	return false
}

// fakeFS — подмена файловой системы: проверку свободного места на живом разделе
// не воспроизвести, а она стоит на пути каждой операции. Остальные операции
// делегируются OSFS, поэтому в поле FS нужно передать store.OSFS{}.
type fakeFS struct {
	store.FS
	free    int64
	freeErr error
}

// FreeSpace возвращает заданные значения вместо statfs.
func (f *fakeFS) FreeSpace(string) (int64, int64, error) { return f.free, f.free, f.freeErr }

// readFile читает файл: проверяется, что архив действительно записан.
func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s: %v", path, err)
	}
	return string(raw)
}

// env — окружение теста: сервис на подменах, хранилище во временном каталоге и
// журнал в памяти (файл журнала проверяется в internal/jobs).
type env struct {
	service *Service
	cfg     config.Configuration
	pg      *fakePG
	store   *store.Store
	journal *jobs.MemoryLog
	now     time.Time
}

// newEnv собирает сервис на подменах с настройками по умолчанию (7 бэкапов,
// 14 дней, две задачи, режим sudo без режима «только просмотр»).
func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvTuned(t, nil, nil)
}

// newEnvWithFS собирает сервис с подменённой файловой системой: так проверяются
// отказы по свободному месту.
func newEnvWithFS(t *testing.T, fs store.FS) *env {
	t.Helper()
	return newEnvTuned(t, fs, nil)
}

// newEnvTuned собирает сервис, давая тесту поправить конфиг до создания: так
// проверяются режим «только просмотр», политика хранения и запреты операций.
func newEnvTuned(t *testing.T, fs store.FS, tune func(*config.Configuration)) *env {
	t.Helper()
	cfg := config.Default()
	cfg.Storage.Dir = t.TempDir()
	cfg.Storage.StateDir = ""
	if tune != nil {
		tune(&cfg)
	}

	pgClient := newFakePG()
	journal := jobs.NewMemoryLog(cfg.Jobs.Retain)
	storage := store.New(cfg.Storage.Dir, cfg.Databases.Pattern, fs)
	env := &env{cfg: cfg, pg: pgClient, store: storage, journal: journal, now: testNow}
	service, err := New(Options{
		Config:  cfg,
		PG:      pgClient,
		Store:   storage,
		Journal: journal,
		Now:     func() time.Time { return env.now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env.service = service
	return env
}

// advance сдвигает время теста: имя бэкапа содержит метку времени с точностью до
// секунды, поэтому две операции подряд должны идти в разные секунды.
func (e *env) advance(d time.Duration) { e.now = e.now.Add(d) }

// backups возвращает бэкапы базы из хранилища.
func (e *env) backups(t *testing.T, database string) []store.Backup {
	t.Helper()
	listed, err := e.store.List(database)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return listed
}

// jobsList возвращает записи журнала, начиная с новой.
func (e *env) jobsList(t *testing.T) []jobs.Job {
	t.Helper()
	stored, err := e.journal.List(0)
	if err != nil {
		t.Fatalf("List журнала: %v", err)
	}
	return stored
}

// findBackup возвращает бэкап по метке.
func (e *env) findBackup(t *testing.T, database, tag string) store.Backup {
	t.Helper()
	for _, backup := range e.backups(t, database) {
		if backup.Tag == tag {
			return backup
		}
	}
	t.Fatalf("бэкап с меткой %q не найден среди %+v", tag, e.backups(t, database))
	return store.Backup{}
}
