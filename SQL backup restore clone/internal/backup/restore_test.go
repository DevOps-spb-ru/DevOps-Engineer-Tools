package backup

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// TestRestoreFromLatestBackup проверяет обычный разворот стенда: перед
// восстановлением снимается служебный бэкап, активные подключения завершаются,
// архив читается, после восстановления база принадлежит владельцу из конфига.
func TestRestoreFromLatestBackup(t *testing.T) {
	env := newEnv(t)
	source := context.Background()
	created, err := env.service.Backup(source, BackupOptions{Database: "fse-1234"})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	// Служебный бэкап снимается в другую секунду: имя бэкапа содержит метку времени.
	env.advance(time.Minute)
	env.pg.sessions = 2

	result, err := env.service.Restore(source, RestoreOptions{Database: "fse-1234"})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if result.Backup.ID != created.Backup.ID {
		t.Errorf("восстановление из %s, ожидался свежий бэкап %s", result.Backup.ID, created.Backup.ID)
	}
	if result.Job.Kind != jobs.KindRestore || result.Job.Target != "fse-1234" {
		t.Errorf("задача журнала: %+v", result.Job)
	}
	if !strings.Contains(result.Job.Message, "база fse-1234 восстановлена из "+created.Backup.ID) {
		t.Errorf("сообщение задачи: %q", result.Job.Message)
	}
	if !strings.Contains(result.Job.Message, "завершено подключений: 2") {
		t.Errorf("в сообщении нет завершённых подключений: %q", result.Job.Message)
	}

	// Служебный бэкап снят до восстановления и помечен меткой.
	preRestore := env.findBackup(t, "fse-1234", PreRestoreTag)
	if !strings.Contains(preRestore.Note, created.Backup.ID) {
		t.Errorf("в служебном бэкапе нет ссылки на восстановленный бэкап: %+v", preRestore)
	}
	if env.pg.calls[0] != "dump fse-1234" {
		t.Errorf("первым действием был не служебный бэкап: %v", env.pg.calls)
	}

	if len(env.pg.created) != 0 {
		t.Errorf("база создана, хотя она существует: %+v", env.pg.created)
	}
	if !env.pg.called("terminate fse-1234") {
		t.Errorf("подключения не завершены: %v", env.pg.calls)
	}
	if !env.pg.called("alter fse-1234 owner postgres") {
		t.Errorf("владелец базы не приведён к databases.owner: %v", env.pg.calls)
	}
	if len(env.pg.restored) != 1 {
		t.Fatalf("pg_restore вызван %d раз", len(env.pg.restored))
	}
	restored := env.pg.restored[0]
	if !restored.Clean || !restored.IfExists || !restored.ExitOnError {
		t.Errorf("параметры pg_restore: %+v", restored)
	}
	if restored.Jobs != env.cfg.Postgres.Jobs || restored.Archive != created.Backup.Path {
		t.Errorf("параллельное восстановление должно читать архив по пути: %+v", restored)
	}
	// Журнал: исходный бэкап, служебный бэкап и восстановление — по две записи
	// на задачу (начало и итог).
	if stored := env.jobsList(t); len(stored) != 6 {
		t.Errorf("в журнале %d записей, ожидалось 6", len(stored))
	}
}

// TestRestoreReadsArchiveFromStdin проверяет восстановление одним процессом:
// pg_restore получает архив потоком, а файл бэкапа только читается.
func TestRestoreReadsArchiveFromStdin(t *testing.T) {
	env := newEnvTuned(t, nil, func(cfg *config.Configuration) { cfg.Postgres.Jobs = 1 })
	source := context.Background()
	if _, err := env.service.Backup(source, BackupOptions{Database: "fse-1234"}); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	env.advance(time.Minute)
	if _, err := env.service.Restore(source, RestoreOptions{Database: "fse-1234"}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	restored := env.pg.restored[len(env.pg.restored)-1]
	if restored.Archive != "" {
		t.Errorf("архив передан путём, хотя процесс один: %+v", restored)
	}
	if env.pg.restoreInput != env.pg.dumpData {
		t.Errorf("pg_restore получил %q, ожидался дамп %q", env.pg.restoreInput, env.pg.dumpData)
	}
}

// TestRestoreRejectsCorruptArchive проверяет проверку контрольной суммы: битый
// архив не разворачивается, потому что восстановление привело бы базу в
// неопределённое состояние.
func TestRestoreRejectsCorruptArchive(t *testing.T) {
	env := newEnv(t)
	source := context.Background()
	created, err := env.service.Backup(source, BackupOptions{Database: "fse-1234"})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if err := os.WriteFile(created.Backup.Path, []byte("подменённый архив"), 0o640); err != nil {
		t.Fatalf("подмена архива: %v", err)
	}
	env.advance(time.Minute)

	_, err = env.service.Restore(source, RestoreOptions{Database: "fse-1234"})
	if !IsRejected(err) {
		t.Fatalf("ошибка %v: ожидался отказ операции", err)
	}
	if !strings.Contains(err.Error(), "повреждён") {
		t.Errorf("сообщение об отказе %q", err)
	}
	if env.pg.called("terminate") || len(env.pg.restored) != 0 {
		t.Errorf("база тронута до проверки архива: %v", env.pg.calls)
	}
}

// TestRestoreCreatesMissingDatabase проверяет разворот бэкапа в удалённую базу:
// база создаётся из template0 с владельцем из конфига, служебный бэкап не
// снимается (терять нечего), владелец не меняется повторно.
func TestRestoreCreatesMissingDatabase(t *testing.T) {
	env := newEnv(t)
	source := context.Background()
	if _, err := env.service.Backup(source, BackupOptions{Database: "fse-1234"}); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	env.advance(time.Minute)
	// База удалена после бэкапа: восстановление должно её создать.
	delete(env.pg.existing, "fse-1234")
	env.pg.dumped = nil

	result, err := env.service.Restore(source, RestoreOptions{Database: "fse-1234"})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !strings.Contains(result.Job.Message, "база создана") {
		t.Errorf("в сообщении нет создания базы: %q", result.Job.Message)
	}
	if len(env.pg.dumped) != 0 {
		t.Errorf("снят служебный бэкап отсутствующей базы: %v", env.pg.dumped)
	}
	if len(env.pg.created) != 1 {
		t.Fatalf("база создавалась %d раз", len(env.pg.created))
	}
	created := env.pg.created[0]
	if created.Database != "fse-1234" || created.Template != "template0" || created.Owner != "postgres" {
		t.Errorf("параметры создания базы: %+v", created)
	}
	if created.Pattern != env.cfg.Databases.Pattern {
		t.Errorf("создание базы не ограничено шаблоном обслуживаемых баз: %+v", created)
	}
	if created.Collate != "" {
		t.Errorf("для удалённой базы локаль не задана, ожидалась локаль по умолчанию: %+v", created)
	}
	if env.pg.called("alter") {
		t.Errorf("владелец менялся у только что созданной базы: %v", env.pg.calls)
	}
}

// TestRestoreSkipClean проверяет режим без очистки: pg_restore не получает
// --clean и --if-exists, объекты существующей базы не удаляются.
func TestRestoreSkipClean(t *testing.T) {
	env := newEnv(t)
	source := context.Background()
	if _, err := env.service.Backup(source, BackupOptions{Database: "fse-1234"}); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	env.advance(time.Minute)
	if _, err := env.service.Restore(source, RestoreOptions{Database: "fse-1234", SkipClean: true}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	restored := env.pg.restored[len(env.pg.restored)-1]
	if restored.Clean || restored.IfExists {
		t.Errorf("очистка объектов выполнялась несмотря на --no-clean: %+v", restored)
	}
}

// TestRestoreRejectsBeforeStart проверяет отказы до начала операции: база вне
// списка обслуживаемых, отсутствие бэкапов, чужой идентификатор бэкапа, обход
// пути и режим «только просмотр».
func TestRestoreRejectsBeforeStart(t *testing.T) {
	source := context.Background()
	cases := []struct {
		name   string
		tune   func(*config.Configuration)
		opts   RestoreOptions
		reason string
	}{
		{"чужая база", nil, RestoreOptions{Database: "prod"}, "не входит в список обслуживаемых"},
		{"нет бэкапов", nil, RestoreOptions{Database: "fssd-7"}, "нет бэкапов"},
		{"чужой бэкап", nil, RestoreOptions{Database: "fse-1234", BackupID: "prod/20260921T020000Z"}, "не соответствует шаблону"},
		{"неизвестный бэкап", nil, RestoreOptions{Database: "fse-1234", BackupID: "fse-1234/20200101T000000Z"}, "не найден"},
		{"обход пути", nil, RestoreOptions{Database: "fse-1234", BackupID: "fse-1234/../../etc/passwd"}, "должен иметь вид"},
		{"режим только просмотр", func(cfg *config.Configuration) { cfg.Server.ReadOnly = true }, RestoreOptions{Database: "fse-1234"}, "только просмотр"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			env := newEnvTuned(t, nil, testCase.tune)
			_, err := env.service.Restore(source, testCase.opts)
			if !IsRejected(err) {
				t.Fatalf("ошибка %v: ожидался отказ операции", err)
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Errorf("сообщение об отказе %q не содержит %q", err, testCase.reason)
			}
			if len(env.pg.calls) != 0 {
				t.Errorf("до отказа выполнены команды: %v", env.pg.calls)
			}
		})
	}
}

// TestRestoreStopsWhenPreRestoreBackupFails проверяет, что без страховочного
// бэкапа восстановление не начинается: иначе сбой на середине разворота оставил
// бы базу без возможности вернуться к прежнему состоянию.
func TestRestoreStopsWhenPreRestoreBackupFails(t *testing.T) {
	env := newEnv(t)
	source := context.Background()
	created, err := env.service.Backup(source, BackupOptions{Database: "fse-1234"})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	env.advance(time.Minute)
	env.pg.dumpErr = errors.New("pg_dump: связь с сервером потеряна")

	_, err = env.service.Restore(source, RestoreOptions{Database: "fse-1234"})
	if err == nil {
		t.Fatal("восстановление началось без служебного бэкапа")
	}
	if !strings.Contains(err.Error(), "служебный бэкап перед восстановлением не создан") {
		t.Errorf("сообщение об ошибке %q", err)
	}
	if len(env.pg.restored) != 0 {
		t.Errorf("pg_restore выполнен без страховочного бэкапа: %+v", env.pg.restored)
	}
	if listed := env.backups(t, "fse-1234"); len(listed) != 1 || listed[0].ID != created.Backup.ID {
		t.Errorf("исходный бэкап потерян: %+v", listed)
	}
}

// TestServiceRequiresDependencies проверяет, что сервис нельзя собрать без
// клиента PostgreSQL и каталога бэкапов: иначе ошибка выяснялась бы в момент
// операции, а не при запуске.
func TestServiceRequiresDependencies(t *testing.T) {
	cfg := config.Default()
	if _, err := New(Options{Config: cfg, Store: store.New(t.TempDir(), cfg.Databases.Pattern, nil)}); err == nil {
		t.Error("сервис создан без клиента PostgreSQL")
	}
	if _, err := New(Options{Config: cfg, PG: newFakePG()}); err == nil {
		t.Error("сервис создан без каталога бэкапов")
	}
}
