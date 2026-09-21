package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
)

// newBackupCommand снимает бэкап базы: дамп идёт в каталог сервиса, а после
// успешного снятия применяется политика хранения.
func newBackupCommand(a *appState) *cobra.Command {
	var database, tag, note string
	var all bool
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Снять бэкап базы (или всех обслуживаемых баз) в каталог сервиса",
		Long: `backup снимает дамп базы (pg_dump --format custom) в каталог storage.dir:

  * база должна входить в список обслуживаемых (databases.pattern) и не быть
    защищённой (databases.protected);
  * при остатке места меньше storage.min_free_space дамп не начинается;
  * файл получает имя архива только после успешного дампа: прерванная запись
    остаётся файлом .part и не считается бэкапом;
  * после снятия применяется политика хранения (storage.keep_last, storage.keep_days).

Флаг --all снимает бэкапы всех обслуживаемых баз кластера: так работает расписание
(systemd-таймер или cron), которому список баз заранее неизвестен. Каждая база —
отдельная задача журнала.

Ход и итог операции попадают в журнал задач: sqlbrc jobs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch {
			case all && strings.TrimSpace(database) != "":
				return errors.New("укажите либо --db <имя>, либо --all: одновременно нельзя")
			case all:
				return backupAll(cmd.Context(), a, tag, note)
			}
			if err := requireDatabase(database); err != nil {
				return err
			}
			service, err := a.service()
			if err != nil {
				return err
			}
			result, err := service.Backup(cmd.Context(), backup.BackupOptions{
				Database: database,
				Tag:      tag,
				Note:     note,
				Operator: operator(),
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(a.stdout, result.Job.Message)
			return err
		},
	}
	cmd.Flags().StringVar(&database, "db", "", "имя обслуживаемой базы (обязательно, если не указан --all)")
	cmd.Flags().BoolVar(&all, "all", false, "снять бэкапы всех обслуживаемых баз кластера")
	cmd.Flags().StringVar(&tag, "tag", "", "метка бэкапа (например pre-restore)")
	cmd.Flags().StringVar(&note, "note", "", "примечание к бэкапу")
	return cmd
}

// backupAll снимает бэкапы всех обслуживаемых баз. Одна недоступная база не
// отменяет остальные: расписание должно снять то, что может, и вернуть код
// ошибки, чтобы сбой был виден в мониторинге. Отказ по правилам (чужая база,
// режим «только просмотр», мало места) и сбой считаются отдельно.
func backupAll(ctx context.Context, a *appState, tag, note string) error {
	service, err := a.service()
	if err != nil {
		return err
	}
	databases, err := service.ManagedDatabases(ctx)
	if err != nil {
		return err
	}
	if len(databases) == 0 {
		_, err := fmt.Fprintln(a.stdout,
			"обслуживаемых баз нет: проверьте databases.pattern и databases.protected")
		return err
	}
	var rejected, failed int
	for _, database := range databases {
		result, err := service.Backup(ctx, backup.BackupOptions{
			Database: database,
			Tag:      tag,
			Note:     note,
			Operator: operator(),
		})
		switch {
		case err == nil:
			_, _ = fmt.Fprintf(a.stdout, "%s: %s\n", database, result.Job.Message)
		case backup.IsRejected(err):
			rejected++
			_, _ = fmt.Fprintf(a.stderr, "отказано (%s): %v\n", database, err)
		default:
			failed++
			_, _ = fmt.Fprintf(a.stderr, "ошибка (%s): %v\n", database, err)
		}
	}
	_, _ = fmt.Fprintf(a.stdout, "баз: %d, успешно: %d, отказано: %d, ошибок: %d\n",
		len(databases), len(databases)-rejected-failed, rejected, failed)
	switch {
	case failed > 0:
		return &codedError{code: exitError, err: fmt.Errorf("бэкап не снят для %d баз", failed)}
	case rejected == len(databases):
		return backup.Rejected("ни одна база не обслуживается: бэкапы не сняты")
	default:
		return nil
	}
}
