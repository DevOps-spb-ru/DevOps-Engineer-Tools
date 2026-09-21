package web

import (
	"context"
	"sync"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// fakeOperations заменяет сервис операций: страницы и API проверяются без
// PostgreSQL, каталога бэкапов и очереди задач. Поля last* фиксируют параметры,
// с которыми пришла операция, — так тест видит, что интерфейс передал именно то,
// что ввёл пользователь.
type fakeOperations struct {
	mu sync.Mutex

	databases []string
	backups   map[string][]store.Backup
	jobs      []jobs.Job

	lastBackup  backup.BackupOptions
	lastRestore backup.RestoreOptions
	lastClone   backup.CloneOptions
	lastPrune   backup.PruneOptions

	operationErr error
	listErr      error
	backupResult backup.Result
	pruneResults []backup.PruneResult
}

func newFakeOperations() *fakeOperations {
	return &fakeOperations{backups: map[string][]store.Backup{}}
}

func (f *fakeOperations) Backup(_ context.Context, opts backup.BackupOptions) (backup.Result, error) {
	f.mu.Lock()
	f.lastBackup = opts
	f.mu.Unlock()
	if f.operationErr != nil {
		return backup.Result{}, f.operationErr
	}
	result := f.backupResult
	result.Job.Kind = jobs.KindBackup
	result.Job.Database = opts.Database
	result.Job.Operator = opts.Operator
	if result.Job.ID == "" {
		result.Job.ID = "20260921T120000Z-00000001"
	}
	return result, nil
}

func (f *fakeOperations) Restore(_ context.Context, opts backup.RestoreOptions) (backup.Result, error) {
	f.mu.Lock()
	f.lastRestore = opts
	f.mu.Unlock()
	if f.operationErr != nil {
		return backup.Result{}, f.operationErr
	}
	result := f.backupResult
	result.Job.Kind = jobs.KindRestore
	result.Job.Database = opts.Database
	result.Job.Operator = opts.Operator
	if result.Job.ID == "" {
		result.Job.ID = "20260921T120000Z-00000002"
	}
	return result, nil
}

func (f *fakeOperations) Clone(_ context.Context, opts backup.CloneOptions) (backup.Result, error) {
	f.mu.Lock()
	f.lastClone = opts
	f.mu.Unlock()
	if f.operationErr != nil {
		return backup.Result{}, f.operationErr
	}
	result := f.backupResult
	result.Job.Kind = jobs.KindClone
	result.Job.Database = opts.From
	result.Job.Target = opts.To
	result.Job.Operator = opts.Operator
	if result.Job.ID == "" {
		result.Job.ID = "20260921T120000Z-00000003"
	}
	return result, nil
}

func (f *fakeOperations) Prune(_ context.Context, opts backup.PruneOptions) ([]backup.PruneResult, error) {
	f.mu.Lock()
	f.lastPrune = opts
	f.mu.Unlock()
	if f.operationErr != nil {
		return nil, f.operationErr
	}
	return f.pruneResults, nil
}

func (f *fakeOperations) Backups(database string) ([]store.Backup, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if database != "" {
		return f.backups[database], nil
	}
	all := make([]store.Backup, 0)
	for _, database := range f.databases {
		all = append(all, f.backups[database]...)
	}
	return all, nil
}

func (f *fakeOperations) Databases() ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.databases, nil
}

func (f *fakeOperations) Jobs(limit int) ([]jobs.Job, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if limit > 0 && len(f.jobs) > limit {
		return f.jobs[:limit], nil
	}
	return f.jobs, nil
}
