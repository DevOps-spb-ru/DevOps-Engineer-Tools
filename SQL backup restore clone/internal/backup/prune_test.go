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
)

// newPruneService собирает второй сервис на том же хранилище и журнале, но с
// другой политикой хранения: так проверяется уборка без пересборки окружения —
// бэкапы накапливаются по одной политике, а убираются по другой.
func newPruneService(t *testing.T, env *env, keepLast int) *Service {
	t.Helper()
	cfg := env.cfg
	cfg.Storage.KeepLast = keepLast
	service, err := New(Options{
		Config:  cfg,
		PG:      env.pg,
		Store:   env.store,
		Journal: env.journal,
		Now:     func() time.Time { return env.now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return service
}

// TestPruneDryRunAndApply проверяет уборку: «на бумаге» удаляется ровно то же,
// что и при настоящем запуске, а при запуске файлы исчезают и появляется задача.
func TestPruneDryRunAndApply(t *testing.T) {
	env := newEnv(t)
	source := context.Background()
	for index := 0; index < 3; index++ {
		if _, err := env.service.Backup(source, BackupOptions{Database: "fse-1234"}); err != nil {
			t.Fatalf("Backup: %v", err)
		}
		env.advance(24 * time.Hour)
	}
	before := env.backups(t, "fse-1234")
	oldest := before[len(before)-1]
	prune := newPruneService(t, env, 1)
	jobsBefore := len(env.jobsList(t))

	planned, err := prune.Prune(source, PruneOptions{Database: "fse-1234", DryRun: true})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(planned) != 1 || len(planned[0].Removed) != 2 || !planned[0].DryRun {
		t.Fatalf("план уборки: %+v", planned)
	}
	if len(env.jobsList(t)) != jobsBefore {
		t.Error("проверка «на бумаге» попала в журнал задач")
	}
	for _, backup := range planned[0].Removed {
		if _, err := os.Stat(backup.Path); err != nil {
			t.Errorf("файл %s удалён при проверке «на бумаге»: %v", backup.Path, err)
		}
	}

	applied, err := prune.Prune(source, PruneOptions{Database: "fse-1234", Operator: "admin"})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(applied) != 1 || len(applied[0].Removed) != 2 || applied[0].DryRun {
		t.Fatalf("итог уборки: %+v", applied)
	}
	if applied[0].Job.Kind != jobs.KindPrune || !strings.Contains(applied[0].Job.Message, "удалено старых бэкапов: 2") {
		t.Errorf("задача уборки: %+v", applied[0].Job)
	}
	for _, backup := range applied[0].Removed {
		if _, err := os.Stat(backup.Path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("файл %s не удалён", backup.Path)
		}
	}
	left := env.backups(t, "fse-1234")
	if len(left) != 1 {
		t.Fatalf("после уборки осталось %d бэкапов, ожидался один", len(left))
	}
	if left[0].ID == oldest.ID {
		t.Errorf("оставлен самый старый бэкап: %s", left[0].ID)
	}
}

// TestPruneAllDatabases проверяет уборку без указания базы: в неё попадают все
// базы каталога хранилища, каждая — отдельной задачей.
func TestPruneAllDatabases(t *testing.T) {
	env := newEnv(t)
	source := context.Background()
	for _, database := range []string{"fse-1234", "fssd-7"} {
		if _, err := env.service.Backup(source, BackupOptions{Database: database}); err != nil {
			t.Fatalf("Backup %s: %v", database, err)
		}
		env.advance(time.Second)
	}
	results, err := env.service.Prune(source, PruneOptions{})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("уборка затронула %d баз, ожидалось 2: %+v", len(results), results)
	}
	databases := map[string]bool{results[0].Database: true, results[1].Database: true}
	if !databases["fse-1234"] || !databases["fssd-7"] {
		t.Errorf("базы уборки: %+v", results)
	}
}

// TestPruneRejectsBeforeStart проверяет отказы уборки: чужая база и режим «только
// просмотр» — до того, как будет удалён хотя бы один файл.
func TestPruneRejectsBeforeStart(t *testing.T) {
	source := context.Background()
	cases := []struct {
		name   string
		tune   func(*config.Configuration)
		opts   PruneOptions
		reason string
	}{
		{"чужая база", nil, PruneOptions{Database: "prod"}, "не входит в список обслуживаемых"},
		{"режим только просмотр", func(cfg *config.Configuration) { cfg.Server.ReadOnly = true }, PruneOptions{}, "только просмотр"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			env := newEnvTuned(t, nil, testCase.tune)
			// В режиме «только просмотр» бэкап снять нельзя: проверяем отказ
			// уборки на пустом каталоге, а сохранность файлов — в первом случае.
			var backupPath string
			if testCase.tune == nil {
				if _, err := env.service.Backup(source, BackupOptions{Database: "fse-1234"}); err != nil {
					t.Fatalf("Backup: %v", err)
				}
				backupPath = env.backups(t, "fse-1234")[0].Path
			}
			_, err := env.service.Prune(source, testCase.opts)
			if !IsRejected(err) {
				t.Fatalf("ошибка %v: ожидался отказ операции", err)
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Errorf("сообщение об отказе %q не содержит %q", err, testCase.reason)
			}
			if backupPath != "" {
				if _, statErr := os.Stat(backupPath); statErr != nil {
					t.Errorf("файл бэкапа пострадал до отказа: %v", statErr)
				}
			}
		})
	}
}

// TestBackupsAndJobs проверяет сводные запросы CLI: список бэкапов всех баз и
// последние записи журнала.
func TestBackupsAndJobs(t *testing.T) {
	env := newEnv(t)
	source := context.Background()
	for _, database := range []string{"fse-1234", "fssd-7"} {
		if _, err := env.service.Backup(source, BackupOptions{Database: database}); err != nil {
			t.Fatalf("Backup %s: %v", database, err)
		}
		env.advance(time.Second)
	}
	all, err := env.service.Backups("")
	if err != nil {
		t.Fatalf("Backups: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("список всех бэкапов содержит %d записей, ожидалось 2", len(all))
	}
	one, err := env.service.Backups("fse-1234")
	if err != nil {
		t.Fatalf("Backups: %v", err)
	}
	if len(one) != 1 || one[0].DB != "fse-1234" {
		t.Errorf("список бэкапов базы: %+v", one)
	}
	recent, err := env.service.Jobs(2)
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(recent) != 2 {
		t.Errorf("журнал вернул %d записей, ожидалось 2", len(recent))
	}
	if recent[0].Status != jobs.StatusDone {
		t.Errorf("последняя запись журнала: %+v", recent[0])
	}
}
