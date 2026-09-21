package backup

import (
	"context"
	"fmt"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// partialGrace — сколько времени незавершённая запись считается активной. Дамп
// большого стенда идёт минутами, поэтому уборка не должна уносить файл
// работающего pg_dump.
const partialGrace = time.Hour

// BackupOptions — параметры снятия бэкапа.
type BackupOptions struct {
	// Database — обслуживаемая база.
	Database string
	// Tag — метка бэкапа (pre-restore и подобные).
	Tag string
	// Note — примечание к бэкапу.
	Note string
	// Operator — кто запустил задачу (для журнала).
	Operator string
}

// Backup снимает бэкап базы: дамп идёт потоком в новый файл каталога бэкапов, по
// завершении применяется политика хранения. Операция выполняется как задача
// очереди, поэтому её ход виден в журнале, а параллельные операции ограничены.
func (s *Service) Backup(ctx context.Context, opts BackupOptions) (Result, error) {
	if err := s.checkOperation(opts.Database); err != nil {
		return Result{}, err
	}
	if err := s.ensureSpace(); err != nil {
		return Result{}, err
	}
	job := jobs.Job{
		Kind:     jobs.KindBackup,
		Database: opts.Database,
		Tag:      opts.Tag,
		Note:     opts.Note,
		Operator: opts.Operator,
	}
	created := store.Backup{}
	job, err := s.queue.Submit(ctx, job, func(ctx context.Context, job jobs.Job) (string, error) {
		backup, dumpErr := s.dump(ctx, job)
		created = backup
		if dumpErr != nil {
			return "", dumpErr
		}
		return s.finishBackup(backup), nil
	})
	return Result{Job: job, Backup: created}, err
}

// finishBackup применяет политику хранения после успешного дампа и собирает
// сообщение задачи. Сбой уборки не отменяет созданный бэкап: сообщение о нём
// попадает в журнал, а сам архив остаётся на месте.
func (s *Service) finishBackup(backup store.Backup) string {
	message := fmt.Sprintf("бэкап создан: %s (%s)", backup.ID, bytesize.HumanSize(backup.SizeBytes))
	removed, err := s.store.ApplyRetention(backup.DB, s.retentionOptions())
	if err != nil {
		message = joinMessage(message, err.Error())
	}
	if len(removed) > 0 {
		message += fmt.Sprintf("; удалено старых бэкапов: %d", len(removed))
	}
	partials, err := s.store.SweepPartials(backup.DB, partialGrace)
	if err != nil {
		message = joinMessage(message, err.Error())
	} else if len(partials) > 0 {
		message += fmt.Sprintf("; убрано незавершённых записей: %d", len(partials))
	}
	return message
}

// dump снимает дамп базы в новый файл хранилища: запись идёт в <имя>.dump.part и
// получает имя архива только после успешного pg_dump, поэтому прерванный дамп не
// выглядит как готовый бэкап.
func (s *Service) dump(ctx context.Context, job jobs.Job) (store.Backup, error) {
	// Версия сервера и размер базы попадают в метаданные: по ним видно, чем снят
	// дамп и насколько архив отличается от базы. Сбой этих запросов дамп не
	// отменяет — они справочные.
	serverVersion := ""
	if version, err := s.pg.ServerVersion(ctx); err == nil {
		serverVersion = version.String()
	}
	databaseSize, err := s.pg.DatabaseSize(ctx, job.Database, s.pattern())
	if err != nil {
		databaseSize = 0
	}
	writer, err := s.store.Begin(store.BeginOptions{
		Database:          job.Database,
		Tag:               job.Tag,
		Note:              job.Note,
		JobID:             job.ID,
		ServerVersion:     serverVersion,
		DatabaseSizeBytes: databaseSize,
		Compression:       pg.DefaultCompression,
		Now:               s.now(),
	})
	if err != nil {
		return store.Backup{}, err
	}
	dumpErr := s.pg.Dump(ctx, pg.DumpOptions{
		Database:    job.Database,
		Pattern:     s.pattern(),
		Compression: pg.DefaultCompression,
	}, writer)
	if dumpErr != nil {
		if abortErr := writer.Abort(); abortErr != nil {
			return store.Backup{}, fmt.Errorf("%w (не удалось убрать незавершённый файл: %w)", dumpErr, abortErr)
		}
		return store.Backup{}, dumpErr
	}
	backup, err := writer.Commit()
	if err != nil {
		// Архив может быть готов (сбой на записи метаданных): возвращаем его и
		// ошибку, чтобы вызывающий код показал и то, и другое.
		return backup, err
	}
	return backup, nil
}
