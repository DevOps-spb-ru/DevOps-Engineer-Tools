package pg

import (
	"context"
	"fmt"
	"io"
)

// DumpOptions — параметры снятия дампа.
type DumpOptions struct {
	// Database — имя обслуживаемой базы.
	Database string
	// Pattern — шаблон имён из databases.pattern (пусто — DefaultAllowPattern).
	Pattern string
	// Format — формат архива (пусто — FormatCustom).
	Format string
	// Compression — уровень сжатия 0..9. Нулевое значение означает «без сжатия»:
	// вызывающий код, которому нужно сжатие, передаёт pg.DefaultCompression
	// явно, и подмены значения по умолчанию здесь нет.
	Compression int
	// Verbose — подробный вывод pg_dump: он попадает в журнал задачи.
	Verbose bool
	// Extra — дополнительные аргументы из CLI (--pg-dump-arg).
	Extra []string
}

// Dump выполняет pg_dump и пишет архив в dst. Дамп идёт потоком: файл открывает
// вызывающий код (каталог бэкапов), поэтому сервис не держит копию архива в
// памяти и не считает размер заранее.
func (c *Client) Dump(ctx context.Context, opts DumpOptions, dst io.Writer) error {
	args, err := DumpArgv(DumpArgs{
		DB:          opts.Database,
		Pattern:     opts.Pattern,
		Connection:  c.cfg.Prefix().ConnectionArgs(c.cfg.connConfig()),
		Format:      opts.Format,
		Compression: opts.Compression,
		Verbose:     opts.Verbose,
		Extra:       opts.Extra,
	})
	if err != nil {
		return err
	}
	if err := c.Exec(ctx, "pg_dump", args, nil, dst, nil); err != nil {
		return fmt.Errorf("не удалось снять дамп базы %s: %w", opts.Database, err)
	}
	return nil
}
