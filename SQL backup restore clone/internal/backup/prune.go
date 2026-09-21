package backup

import (
	"context"
	"fmt"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// PruneOptions — параметры уборки старых бэкапов по политике хранения.
type PruneOptions struct {
	// Database — база; пустое значение означает все базы хранилища.
	Database string
	// DryRun — только показать, что будет удалено, ничего не удаляя.
	DryRun bool
	// Operator — кто запустил задачу (для журнала).
	Operator string
}

// PruneResult — итог уборки по одной базе.
type PruneResult struct {
	// Job — запись журнала задач (пустая при DryRun: уборки не было).
	Job jobs.Job
	// Database — база, к которой применена политика.
	Database string
	// Removed — удалённые бэкапы, а при DryRun — те, что были бы удалены.
	Removed []store.Backup
	// DryRun — уборка выполнялась «на бумаге».
	DryRun bool
}

// Prune применяет политику хранения: удаляет старые бэкапы и незавершённые
// записи. Каждая база — отдельная задача очереди: уборка большой базы не должна
// задерживать бэкап по другой.
func (s *Service) Prune(ctx context.Context, opts PruneOptions) ([]PruneResult, error) {
	databases, err := s.pruneTargets(opts.Database)
	if err != nil {
		return nil, err
	}
	results := make([]PruneResult, 0, len(databases))
	for _, database := range databases {
		result, err := s.pruneDatabase(ctx, database, opts)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

// pruneTargets возвращает базы, к которым применяется политика хранения.
func (s *Service) pruneTargets(database string) ([]string, error) {
	if database != "" {
		if err := s.checkOperation(database); err != nil {
			return nil, err
		}
		return []string{database}, nil
	}
	// Уборка без указания базы тоже меняет файлы: в режиме «только просмотр»
	// она запрещена, а базы берутся из каталога хранилища.
	if s.cfg.Server.ReadOnly {
		return nil, Rejected("сервис в режиме «только просмотр» (server.read_only): операции изменения запрещены")
	}
	return s.store.ListDatabases()
}

// pruneDatabase применяет политику к одной базе. В режиме DryRun план строится
// той же функцией, что и настоящая уборка (store.Plan), поэтому «на бумаге»
// удаляется ровно то же, что и при запуске без флага.
func (s *Service) pruneDatabase(ctx context.Context, database string, opts PruneOptions) (PruneResult, error) {
	backups, err := s.store.List(database)
	if err != nil {
		return PruneResult{}, err
	}
	if opts.DryRun {
		_, remove := store.Plan(backups, s.retentionOptions())
		return PruneResult{Database: database, Removed: remove, DryRun: true}, nil
	}
	job := jobs.Job{Kind: jobs.KindPrune, Database: database, Operator: opts.Operator}
	removed := make([]store.Backup, 0, len(backups))
	job, err = s.queue.Submit(ctx, job, func(ctx context.Context, _ jobs.Job) (string, error) {
		deleted, deleteErr := s.store.ApplyRetention(database, s.retentionOptions())
		removed = deleted
		message := fmt.Sprintf("удалено старых бэкапов: %d", len(deleted))
		partials, sweepErr := s.store.SweepPartials(database, partialGrace)
		switch {
		case sweepErr != nil:
			message = joinMessage(message, sweepErr.Error())
		case len(partials) > 0:
			message += fmt.Sprintf("; убрано незавершённых записей: %d", len(partials))
		}
		return message, deleteErr
	})
	return PruneResult{Job: job, Database: database, Removed: removed}, err
}
