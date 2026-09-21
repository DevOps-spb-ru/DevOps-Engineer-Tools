package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
)

// newBackupCommand снимает бэкап базы: дамп идёт в каталог сервиса, а после
// успешного снятия применяется политика хранения.
func newBackupCommand(a *appState) *cobra.Command {
	var database, tag, note string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Снять бэкап базы в каталог сервиса",
		Long: `backup снимает дамп базы (pg_dump --format custom) в каталог storage.dir:

  * база должна входить в список обслуживаемых (databases.pattern) и не быть
    защищённой (databases.protected);
  * при остатке места меньше storage.min_free_space дамп не начинается;
  * файл получает имя архива только после успешного дампа: прерванная запись
    остаётся файлом .part и не считается бэкапом;
  * после снятия применяется политика хранения (storage.keep_last, storage.keep_days).

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
	cmd.Flags().StringVar(&database, "db", "", "имя обслуживаемой базы (обязательно)")
	cmd.Flags().StringVar(&tag, "tag", "", "метка бэкапа (например pre-restore)")
	cmd.Flags().StringVar(&note, "note", "", "примечание к бэкапу")
	return cmd
}
