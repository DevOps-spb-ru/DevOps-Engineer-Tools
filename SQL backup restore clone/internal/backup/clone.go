package backup

import (
	"context"
	"fmt"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
)

// CloneOptions — параметры клонирования базы в стенд.
type CloneOptions struct {
	// From — база-источник.
	From string
	// To — новая база-приёмник: стенд вида <префикс>-<номер>.
	To string
	// BackupID — бэкап источника; пусто означает последний бэкап источника.
	BackupID string
	// Operator — кто запустил задачу (для журнала).
	Operator string
}

// Clone создаёт копию базы в новом стенде: бэкап источника разворачивается в
// базу-приёмник. Источник при этом только читается, а существующая база-приёмник
// не перезаписывается — её нужно удалить или выбрать другое имя: под видом
// клонирования нельзя потерять чужую работу.
func (s *Service) Clone(ctx context.Context, opts CloneOptions) (Result, error) {
	if err := s.checkOperation(opts.From); err != nil {
		return Result{}, err
	}
	if err := s.checkOperation(opts.To); err != nil {
		return Result{}, err
	}
	if opts.From == opts.To {
		return Result{}, Rejected("источник и приёмник — одна и та же база: %s", opts.To)
	}
	backup, err := s.resolveBackup(opts.From, opts.BackupID)
	if err != nil {
		return Result{}, err
	}
	if err := s.verifyArchive(backup); err != nil {
		return Result{}, err
	}
	exists, err := s.pg.DatabaseExists(ctx, opts.To, s.pattern())
	if err != nil {
		return Result{}, err
	}
	if exists {
		return Result{}, Rejected(
			"база-приёмник %s уже есть в кластере: сервис не перезаписывает существующие базы (удалите её или выберите другое имя)",
			opts.To)
	}
	if err := s.ensureSpace(); err != nil {
		return Result{}, err
	}
	job := jobs.Job{
		Kind:     jobs.KindClone,
		Database: opts.From,
		Target:   opts.To,
		BackupID: backup.ID,
		Operator: opts.Operator,
	}
	job, err = s.queue.Submit(ctx, job, func(ctx context.Context, _ jobs.Job) (string, error) {
		// Локаль приёмника берётся у источника: стенд должен сортировать данные
		// так же, как оригинал.
		if _, err := s.restoreInto(ctx, backup, target{Database: opts.To, Source: opts.From}); err != nil {
			return "", err
		}
		return fmt.Sprintf("стенд %s создан из %s (%s)",
			opts.To, backup.ID, bytesize.HumanSize(backup.SizeBytes)), nil
	})
	return Result{Job: job, Backup: backup}, err
}
