package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
)

// newRestoreCommand восстанавливает базу из бэкапа.
func newRestoreCommand(a *appState) *cobra.Command {
	var database, backupID string
	var noClean bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Восстановить базу из бэкапа",
		Long: `restore разворачивает базу из бэкапа:

  * по умолчанию берётся последний бэкап базы; конкретный бэкап задаётся
    идентификатором (sqlbrc backups): --backup fse-1234/20260921T020000Z;
  * если включено databases.auto_backup_before_restore, сначала снимается
    служебный бэкап текущего состояния с меткой pre-restore;
  * при databases.terminate_on_restore активные подключения завершаются
    (pg_terminate_backend), иначе pg_restore не сможет удалить объекты;
  * отсутствующая база создаётся, существующая очищается (--clean --if-exists);
    флаг --no-clean оставляет объекты, которых нет в дампе;
  * контрольная сумма архива сверяется с метаданными до первой команды,
    меняющей базу.

Ход и итог операции попадают в журнал задач: sqlbrc jobs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDatabase(database); err != nil {
				return err
			}
			service, err := a.service()
			if err != nil {
				return err
			}
			result, err := service.Restore(cmd.Context(), backup.RestoreOptions{
				Database:  database,
				BackupID:  backupID,
				SkipClean: noClean,
				Operator:  operator(),
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(a.stdout, result.Job.Message)
			return err
		},
	}
	cmd.Flags().StringVar(&database, "db", "", "восстанавливаемая база (обязательно)")
	cmd.Flags().StringVar(&backupID, "backup", "", "идентификатор бэкапа (по умолчанию — последний бэкап базы)")
	cmd.Flags().BoolVar(&noClean, "no-clean", false, "не удалять объекты существующей базы перед восстановлением")
	return cmd
}
