package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// RestoreOptions — параметры восстановления базы из бэкапа.
type RestoreOptions struct {
	// Database — восстанавливаемая база.
	Database string
	// BackupID — идентификатор бэкапа; пустое значение означает последний бэкап
	// базы (обычный случай «вернуть стенд к свежему состоянию»).
	BackupID string
	// SkipClean — не очищать объекты существующей базы. По умолчанию объекты
	// удаляются (pg_restore --clean --if-exists): иначе в базе останутся таблицы,
	// которых нет в источнике, и стенд будет не тем, что в бэкапе.
	SkipClean bool
	// Operator — кто запустил задачу (для журнала).
	Operator string
}

// target — куда разворачивается бэкап.
type target struct {
	// Database — база-приёмник.
	Database string
	// Source — база-образец для локали при создании приёмника (пусто — локаль по
	// умолчанию сервера).
	Source string
	// SkipClean — не очищать объекты существующей базы.
	SkipClean bool
}

// Restore восстанавливает базу из бэкапа: отсутствующая база создаётся,
// существующая очищается, активные подключения завершаются. Перед этим (если
// включено `databases.auto_backup_before_restore`) снимается служебный бэкап
// текущего состояния: чистый restore без страховки — это потеря данных стенда.
func (s *Service) Restore(ctx context.Context, opts RestoreOptions) (Result, error) {
	if err := s.checkOperation(opts.Database); err != nil {
		return Result{}, err
	}
	backup, err := s.resolveBackup(opts.Database, opts.BackupID)
	if err != nil {
		return Result{}, err
	}
	if err := s.verifyArchive(backup); err != nil {
		return Result{}, err
	}
	// Служебный бэкап снимается до того, как задача restore займёт место в
	// очереди: это вторая задача по той же базе, и при jobs.max_per_db = 1 она
	// не прошла бы, пока restore держит слот.
	if err := s.autoBackupBeforeRestore(ctx, opts, backup); err != nil {
		return Result{}, err
	}
	if err := s.ensureSpace(); err != nil {
		return Result{}, err
	}
	job := jobs.Job{
		Kind:     jobs.KindRestore,
		Database: backup.DB,
		Target:   opts.Database,
		BackupID: backup.ID,
		Operator: opts.Operator,
	}
	job, err = s.queue.Submit(ctx, job, func(ctx context.Context, _ jobs.Job) (string, error) {
		return s.restoreInto(ctx, backup, target{
			Database:  opts.Database,
			SkipClean: opts.SkipClean,
		})
	})
	return Result{Job: job, Backup: backup}, err
}

// autoBackupBeforeRestore снимает страховочный бэкап текущего состояния базы.
// Бэкап делается только для существующей базы: у отсутствующей терять нечего.
func (s *Service) autoBackupBeforeRestore(ctx context.Context, opts RestoreOptions, backup store.Backup) error {
	if !s.cfg.Databases.AutoBackupBeforeRestore {
		return nil
	}
	exists, err := s.pg.DatabaseExists(ctx, opts.Database, s.pattern())
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err = s.Backup(ctx, BackupOptions{
		Database: opts.Database,
		Tag:      PreRestoreTag,
		Note:     "перед восстановлением " + backup.ID,
		Operator: opts.Operator,
	})
	if err != nil {
		// Без страховочного бэкапа восстановление не начинается: иначе сбой на
		// середине restore оставил бы базу без возможности вернуться к прежнему
		// состоянию.
		return fmt.Errorf("служебный бэкап перед восстановлением не создан: %w", err)
	}
	return nil
}

// restoreInto разворачивает бэкап в базу: создаёт её при необходимости, завершает
// активные подключения, выполняет pg_restore и собирает сообщение задачи.
func (s *Service) restoreInto(ctx context.Context, backup store.Backup, dst target) (string, error) {
	exists, err := s.pg.DatabaseExists(ctx, dst.Database, s.pattern())
	if err != nil {
		return "", err
	}
	created := false
	if !exists {
		if err := s.createDatabase(ctx, dst.Database, dst.Source); err != nil {
			return "", err
		}
		created = true
	}
	terminated := 0
	if s.cfg.Databases.TerminateOnRestore {
		// Число активных подключений запоминается до завершения: после вызова
		// pg_terminate_backend их уже не посчитать.
		if sessions, err := s.pg.Sessions(ctx, dst.Database, s.pattern()); err == nil {
			terminated = sessions
		}
		killed, err := s.pg.TerminateBackends(ctx, dst.Database, s.pattern())
		if err != nil {
			return "", err
		}
		if killed > terminated {
			terminated = killed
		}
	}
	if err := s.runRestore(ctx, backup, dst); err != nil {
		return "", err
	}
	// Владельца меняют только у существующей базы: у созданной он задан сразу.
	if owner := s.cfg.Databases.Owner; owner != "" && !created {
		if err := s.pg.AlterDatabaseOwner(ctx, dst.Database, owner, s.pattern()); err != nil {
			return "", err
		}
	}
	message := fmt.Sprintf("база %s восстановлена из %s (%s)",
		dst.Database, backup.ID, bytesize.HumanSize(backup.SizeBytes))
	if created {
		message += "; база создана"
	}
	if terminated > 0 {
		message += fmt.Sprintf("; завершено подключений: %d", terminated)
	}
	return message, nil
}

// runRestore выполняет pg_restore: архив передаётся потоком, а при параллельном
// восстановлении — путём к файлу (pg_restore не читает архив из stdin с --jobs).
func (s *Service) runRestore(ctx context.Context, backup store.Backup, dst target) error {
	opts := pg.RestoreOptions{
		Database:    dst.Database,
		Pattern:     s.pattern(),
		Jobs:        s.cfg.Postgres.Jobs,
		Clean:       !dst.SkipClean,
		IfExists:    !dst.SkipClean,
		ExitOnError: true,
	}
	if opts.Jobs > 1 {
		opts.Archive = backup.Path
		return s.pg.Restore(ctx, opts, nil)
	}
	// Архив открывается только для чтения: восстановление не должно менять файл
	// бэкапа, он нужен и другим операциям.
	file, err := os.Open(backup.Path)
	if err != nil {
		return fmt.Errorf("не удалось открыть архив %s: %w", backup.Path, err)
	}
	defer func() { _ = file.Close() }()
	return s.pg.Restore(ctx, opts, file)
}

// createDatabase создаёт базу-приёмник. Локаль берётся у базы-образца (при
// клонировании — у источника): иначе стенд получил бы другую сортировку, чем
// оригинал. Если образца нет, база создаётся с локалью по умолчанию сервера —
// это видно в отчёте `sqlbrc doctor` (проверка локалей).
func (s *Service) createDatabase(ctx context.Context, database, source string) error {
	opts := pg.CreateDatabaseOptions{
		Database: database,
		Pattern:  s.pattern(),
		Owner:    s.cfg.Databases.Owner,
		Template: "template0",
	}
	if source != "" {
		locale, err := s.pg.DatabaseLocale(ctx, source, s.pattern())
		if err != nil {
			return fmt.Errorf("не удалось получить локаль базы-образца %s: %w", source, err)
		}
		opts.Encoding, opts.Collate, opts.Ctype = locale.Encoding, locale.Collate, locale.Ctype
	}
	return s.pg.CreateDatabase(ctx, opts)
}

// resolveBackup находит бэкап для восстановления: указанный или последний.
func (s *Service) resolveBackup(database, id string) (store.Backup, error) {
	if id == "" {
		backup, err := s.store.Latest(database)
		if err != nil {
			return store.Backup{}, Rejected(
				"у базы %s нет бэкапов: сначала снимите бэкап (sqlbrc backup --db %s)", database, database)
		}
		return backup, nil
	}
	backup, err := s.store.Find(id)
	if err != nil {
		return store.Backup{}, Rejected("%v", err)
	}
	return backup, nil
}

// verifyArchive сверяет контрольную сумму архива с сохранённой в метаданных:
// восстановление из повреждённого файла приводит базу в неопределённое
// состояние, поэтому проверка идёт до первой команды, меняющей базу.
func (s *Service) verifyArchive(backup store.Backup) error {
	if backup.SHA256 == "" {
		// Метаданных нет (файл появился в каталоге не из сервиса): сверять нечего,
		// но и отказываться от восстановления нельзя — это осознанный выбор
		// администратора.
		return nil
	}
	sum, err := fileSHA256(backup.Path)
	if err != nil {
		return err
	}
	if sum != backup.SHA256 {
		return Rejected("архив %s повреждён: контрольная сумма %s не совпадает с сохранённой %s",
			backup.ID, sum, backup.SHA256)
	}
	return nil
}

// fileSHA256 считает контрольную сумму файла.
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("не удалось прочитать архив %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("не удалось прочитать архив %s: %w", path, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
