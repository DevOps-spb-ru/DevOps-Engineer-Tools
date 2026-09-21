package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// TestBackupCreatesArchive проверяет полный путь бэкапа: архив записан в каталог
// хранилища, метаданные связаны с задачей журнала, а временных файлов не остаётся.
func TestBackupCreatesArchive(t *testing.T) {
	env := newEnv(t)
	result, err := env.service.Backup(context.Background(), BackupOptions{
		Database: "fse-1234",
		Note:     "перед релизом",
		Operator: "admin",
	})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if result.Backup.ID != "fse-1234/20260921T020000Z" || result.Backup.Status != store.StatusComplete {
		t.Fatalf("бэкап описан неверно: %+v", result.Backup)
	}
	if got := readFile(t, result.Backup.Path); got != env.pg.dumpData {
		t.Errorf("в архиве %q, ожидался дамп %q", got, env.pg.dumpData)
	}
	if result.Job.Status != jobs.StatusDone || result.Job.Kind != jobs.KindBackup {
		t.Errorf("задача журнала: %+v", result.Job)
	}
	if !strings.Contains(result.Job.Message, "бэкап создан: fse-1234/20260921T020000Z") {
		t.Errorf("сообщение задачи: %q", result.Job.Message)
	}
	if result.Job.Operator != "admin" {
		t.Errorf("в задаче не сохранён автор операции: %+v", result.Job)
	}

	listed := env.backups(t, "fse-1234")
	if len(listed) != 1 {
		t.Fatalf("в хранилище %d бэкапов, ожидался один", len(listed))
	}
	if listed[0].JobID != result.Job.ID {
		t.Errorf("метаданные связаны с задачей %q, ожидалась %q", listed[0].JobID, result.Job.ID)
	}
	if listed[0].Note != "перед релизом" || listed[0].ServerVersion != "15.14" {
		t.Errorf("в метаданных потеряны параметры дампа: %+v", listed[0])
	}
	if listed[0].DatabaseSizeBytes != env.pg.size {
		t.Errorf("размер базы в метаданных %d, ожидался %d", listed[0].DatabaseSizeBytes, env.pg.size)
	}

	if len(env.pg.dumped) != 1 {
		t.Fatalf("pg_dump вызван %d раз", len(env.pg.dumped))
	}
	if env.pg.dumped[0].Compression != pg.DefaultCompression || env.pg.dumped[0].Pattern != env.cfg.Databases.Pattern {
		t.Errorf("параметры дампа: %+v", env.pg.dumped[0])
	}

	// В каталоге базы только архив и его метаданные: незавершённой записи нет.
	entries, err := os.ReadDir(filepath.Join(env.cfg.Storage.Dir, "fse-1234"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("в каталоге базы %d файлов, ожидалось 2 (архив и метаданные)", len(entries))
	}
	// Журнал содержит начало и итог задачи.
	stored := env.jobsList(t)
	if len(stored) != 2 || stored[1].Status != jobs.StatusRunning || stored[0].Status != jobs.StatusDone {
		t.Errorf("журнал задач: %+v", stored)
	}
}

// TestBackupRemovesPartialOnFailure проверяет, что сбой pg_dump не оставляет в
// каталоге файлов: незавершённый дамп убирается, а задача попадает в журнал.
func TestBackupRemovesPartialOnFailure(t *testing.T) {
	env := newEnv(t)
	env.pg.dumpErr = errors.New("pg_dump: связь с сервером потеряна")

	_, err := env.service.Backup(context.Background(), BackupOptions{Database: "fse-1234"})
	if err == nil {
		t.Fatal("сбой дампа не вернул ошибку")
	}
	if !strings.Contains(err.Error(), "связь с сервером потеряна") {
		t.Errorf("сообщение об ошибке %q", err)
	}
	if listed := env.backups(t, "fse-1234"); len(listed) != 0 {
		t.Errorf("после сбоя в хранилище остались бэкапы: %+v", listed)
	}
	entries, err := os.ReadDir(filepath.Join(env.cfg.Storage.Dir, "fse-1234"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("после сбоя в каталоге остались файлы: %v", entries)
	}
	stored := env.jobsList(t)
	if len(stored) != 2 || stored[0].Status != jobs.StatusFailed {
		t.Fatalf("журнал задач: %+v", stored)
	}
	if !strings.Contains(stored[0].Error, "связь с сервером потеряна") {
		t.Errorf("в задаче нет причины сбоя: %+v", stored[0])
	}
}

// TestBackupRejectsForeignDatabase проверяет отказы до начала операции: чужая и
// защищённая базы не бэкапятся, в режиме «только просмотр» операции запрещены.
// Ни одна команда при этом не выполняется.
func TestBackupRejectsForeignDatabase(t *testing.T) {
	cases := []struct {
		name   string
		tune   func(*config.Configuration)
		base   string
		reason string
	}{
		{"чужая база", nil, "prod", "не входит в список обслуживаемых"},
		{"служебная база", nil, "postgres", "защищена"},
		{"обход каталога", nil, "../../etc", "не входит в список обслуживаемых"},
		{"режим только просмотр", func(cfg *config.Configuration) { cfg.Server.ReadOnly = true }, "fse-1234", "только просмотр"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			env := newEnvTuned(t, nil, testCase.tune)
			_, err := env.service.Backup(context.Background(), BackupOptions{Database: testCase.base})
			if !IsRejected(err) {
				t.Fatalf("ошибка %v: ожидался отказ операции", err)
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Errorf("сообщение об отказе %q не содержит %q", err, testCase.reason)
			}
			if len(env.pg.calls) != 0 {
				t.Errorf("до отказа выполнены команды: %v", env.pg.calls)
			}
			if len(env.jobsList(t)) != 0 {
				t.Error("отказ попал в журнал задач: задача не начиналась")
			}
		})
	}
}

// TestBackupRejectsInsufficientSpace проверяет отказ по свободному месту: дамп не
// начинается, если остаток меньше storage.min_free_space.
func TestBackupRejectsInsufficientSpace(t *testing.T) {
	env := newEnvWithFS(t, &fakeFS{FS: store.OSFS{}, free: 1 << 20})
	_, err := env.service.Backup(context.Background(), BackupOptions{Database: "fse-1234"})
	if !IsRejected(err) {
		t.Fatalf("ошибка %v: ожидался отказ операции", err)
	}
	if !strings.Contains(err.Error(), "свободного места") {
		t.Errorf("сообщение об отказе %q", err)
	}
	if len(env.pg.calls) != 0 {
		t.Errorf("до отказа выполнены команды: %v", env.pg.calls)
	}
}

// TestBackupAppliesRetention проверяет, что политика хранения применяется сразу
// после дампа: иначе каталог рос бы до первой ручной уборки.
func TestBackupAppliesRetention(t *testing.T) {
	env := newEnvTuned(t, nil, func(cfg *config.Configuration) {
		cfg.Storage.KeepLast = 1
		cfg.Storage.KeepDays = 14
	})
	source := context.Background()
	if _, err := env.service.Backup(source, BackupOptions{Database: "fse-1234"}); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	old := env.backups(t, "fse-1234")[0]
	env.advance(24 * time.Hour)

	result, err := env.service.Backup(source, BackupOptions{Database: "fse-1234"})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if !strings.Contains(result.Job.Message, "удалено старых бэкапов: 1") {
		t.Errorf("в сообщении задачи нет уборки: %q", result.Job.Message)
	}
	if _, err := os.Stat(old.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("старый бэкап %s не удалён", old.Path)
	}
	if listed := env.backups(t, "fse-1234"); len(listed) != 1 {
		t.Errorf("после уборки в хранилище %d бэкапов, ожидался один", len(listed))
	}
}
