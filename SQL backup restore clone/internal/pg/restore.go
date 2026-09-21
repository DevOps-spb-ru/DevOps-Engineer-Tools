package pg

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// RestoreOptions — параметры восстановления базы из архива.
type RestoreOptions struct {
	// Database — база, в которую разворачивается архив.
	Database string
	// Pattern — шаблон имён из databases.pattern (пусто — DefaultAllowPattern).
	Pattern string
	// Archive — путь к архиву. Обязателен при параллельном восстановлении:
	// pg_restore не умеет читать архив из stdin, когда работает несколькими
	// процессами, поэтому файл передаётся позиционным аргументом.
	Archive string
	// Jobs — число параллельных процессов pg_restore (0 и 1 — без параллелизма).
	Jobs int
	// Clean — удалить объекты перед восстановлением (--clean --if-exists).
	Clean bool
	// IfExists — не считать ошибкой отсутствие объекта при Clean.
	IfExists bool
	// ExitOnError — останавливаться на первой ошибке, а не собирать предупреждения.
	ExitOnError bool
	// Verbose — подробный вывод pg_restore в журнал задачи.
	Verbose bool
	// Extra — дополнительные аргументы из CLI (--pg-restore-arg).
	Extra []string
}

// Restore выполняет pg_restore, читая архив из src. Дамп передаётся по stdin:
// копия файла не нужна, а архив — это тот же файл, что лежит в каталоге бэкапов.
func (c *Client) Restore(ctx context.Context, opts RestoreOptions, src io.Reader) error {
	args := RestoreArgs{
		DB:          opts.Database,
		Pattern:     opts.Pattern,
		Connection:  c.cfg.Prefix().ConnectionArgs(c.cfg.connConfig()),
		Jobs:        opts.Jobs,
		Clean:       opts.Clean,
		IfExists:    opts.IfExists,
		ExitOnError: opts.ExitOnError,
		Verbose:     opts.Verbose,
		Extra:       opts.Extra,
	}
	if opts.Jobs > 1 {
		if opts.Archive == "" {
			return errors.New("для параллельного восстановления нужен путь к архиву: pg_restore не читает архив из stdin при --jobs")
		}
		args.Archive = opts.Archive
		// Архив читает сам pg_restore: поток stdin при этом не используется.
		src = nil
	}
	argv, err := RestoreArgv(args)
	if err != nil {
		return err
	}
	if err := c.Exec(ctx, "pg_restore", argv, src, nil, nil); err != nil {
		return fmt.Errorf("не удалось восстановить базу %s из %s: %w",
			opts.Database, describeArchive(opts.Archive), err)
	}
	return nil
}

// ArchiveSummary — сводка по архиву: сколько объектов и какие типы внутри.
type ArchiveSummary struct {
	// Entries — число объектов в оглавлении архива.
	Entries int
	// ByType — сводка по типам объектов («TABLE 12», «INDEX 30»).
	ByType []string
}

// ArchiveSummary читает оглавление архива (pg_restore --list) и возвращает
// сводку. Сервер для этого не нужен: сводка читается даже когда PostgreSQL
// недоступен, и попадает в метаданные бэкапа.
func (c *Client) ArchiveSummary(ctx context.Context, archive string) (ArchiveSummary, error) {
	entries, err := c.ListArchive(ctx, archive)
	if err != nil {
		return ArchiveSummary{}, err
	}
	return ArchiveSummary{Entries: len(entries), ByType: SummarizeTOC(entries)}, nil
}

// describeArchive возвращает путь к архиву для сообщения об ошибке.
func describeArchive(archive string) string {
	if archive == "" {
		return "архива"
	}
	return archive
}
