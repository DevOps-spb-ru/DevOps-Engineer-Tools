package backup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
)

// TestCloneCreatesStand проверяет клонирование: бэкап источника разворачивается
// в новый стенд, стенд получает локаль источника и владельца из конфига,
// источник при этом не меняется.
func TestCloneCreatesStand(t *testing.T) {
	env := newEnv(t)
	source := context.Background()
	created, err := env.service.Backup(source, BackupOptions{Database: "fse-1234"})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	env.advance(time.Minute)

	result, err := env.service.Clone(source, CloneOptions{
		From:     "fse-1234",
		To:       "fssd-7",
		Operator: "admin",
	})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if result.Job.Kind != jobs.KindClone || result.Job.Database != "fse-1234" || result.Job.Target != "fssd-7" {
		t.Errorf("задача журнала: %+v", result.Job)
	}
	if result.Job.BackupID != created.Backup.ID {
		t.Errorf("в задаче указан бэкап %q, ожидался %q", result.Job.BackupID, created.Backup.ID)
	}
	if !strings.Contains(result.Job.Message, "стенд fssd-7 создан из "+created.Backup.ID) {
		t.Errorf("сообщение задачи: %q", result.Job.Message)
	}

	if len(env.pg.created) != 1 {
		t.Fatalf("база-приёмник создавалась %d раз", len(env.pg.created))
	}
	createdDB := env.pg.created[0]
	if createdDB.Database != "fssd-7" || createdDB.Template != "template0" || createdDB.Owner != "postgres" {
		t.Errorf("параметры создания стенда: %+v", createdDB)
	}
	// Локаль стенда берётся у источника: сортировка данных должна совпадать.
	if createdDB.Collate != env.pg.locale.Collate || createdDB.Ctype != env.pg.locale.Ctype {
		t.Errorf("локаль стенда %+v, ожидалась локаль источника %+v", createdDB, env.pg.locale)
	}
	// Источник не бэкапился повторно и не менялся.
	if len(env.pg.dumped) != 1 {
		t.Errorf("pg_dump вызван %d раз: клонирование не должно снимать дамп", len(env.pg.dumped))
	}
	if env.pg.called("terminate fse-1234") {
		t.Errorf("подключения источника завершены: %v", env.pg.calls)
	}
	if !env.pg.called("terminate fssd-7") {
		t.Errorf("подключения стенда не завершены: %v", env.pg.calls)
	}
	restored := env.pg.restored[len(env.pg.restored)-1]
	if restored.Database != "fssd-7" {
		t.Errorf("восстановление выполнено в %q, ожидался стенд fssd-7", restored.Database)
	}
}

// TestCloneRejectsBeforeStart проверяет отказы до начала операции: чужие базы,
// существующий приёмник, совпадение источника и приёмника, отсутствие бэкапа.
// Существующий приёмник — главный случай: сервис не перезаписывает чужие базы.
func TestCloneRejectsBeforeStart(t *testing.T) {
	source := context.Background()
	cases := []struct {
		name    string
		tune    func(*config.Configuration)
		prepare func(t *testing.T, env *env)
		opts    CloneOptions
		reason  string
	}{
		{
			name:   "чужой источник",
			opts:   CloneOptions{From: "prod", To: "fssd-7"},
			reason: "не входит в список обслуживаемых",
		},
		{
			name:   "чужой приёмник",
			opts:   CloneOptions{From: "fse-1234", To: "postgres"},
			reason: "защищена",
		},
		{
			name:   "источник и приёмник совпадают",
			opts:   CloneOptions{From: "fse-1234", To: "fse-1234"},
			reason: "одна и та же база",
		},
		{
			name: "приёмник уже существует",
			prepare: func(t *testing.T, env *env) {
				t.Helper()
				if _, err := env.service.Backup(context.Background(), BackupOptions{Database: "fse-1234"}); err != nil {
					t.Fatalf("Backup: %v", err)
				}
				env.pg.existing["fssd-7"] = true
			},
			opts:   CloneOptions{From: "fse-1234", To: "fssd-7"},
			reason: "уже есть в кластере",
		},
		{
			name:   "у источника нет бэкапов",
			opts:   CloneOptions{From: "fssd-7", To: "dops-fix-42"},
			reason: "нет бэкапов",
		},
		{
			name:   "режим только просмотр",
			tune:   func(cfg *config.Configuration) { cfg.Server.ReadOnly = true },
			opts:   CloneOptions{From: "fse-1234", To: "fssd-7"},
			reason: "только просмотр",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			env := newEnvTuned(t, nil, testCase.tune)
			if testCase.prepare != nil {
				testCase.prepare(t, env)
			}
			before := len(env.pg.calls)
			_, err := env.service.Clone(source, testCase.opts)
			if !IsRejected(err) {
				t.Fatalf("ошибка %v: ожидался отказ операции", err)
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Errorf("сообщение об отказе %q не содержит %q", err, testCase.reason)
			}
			if len(env.pg.calls) != before {
				t.Errorf("после отказа выполнены команды: %v", env.pg.calls[before:])
			}
		})
	}
}
