package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
)

// newCloneCommand клонирует базу в новый стенд.
func newCloneCommand(a *appState) *cobra.Command {
	var from, to, backupID string
	cmd := &cobra.Command{
		Use:   "clone",
		Short: "Создать стенд из бэкапа другой базы",
		Long: `clone разворачивает бэкап базы-источника в новый стенд вида <префикс>-<номер>:

  * источник только читается: дамп берётся из его бэкапа (по умолчанию — свежего);
  * стенд создаётся с локалью источника — сортировка данных совпадает с оригиналом;
  * существующая база-приёмник не перезаписывается: её нужно удалить или выбрать
    другое имя;
  * оба имени проверяются по databases.pattern и databases.protected.

Перед клонированием убедитесь, что у источника есть свежий бэкап: sqlbrc backups --db <источник>.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDatabase(from); err != nil {
				return fmt.Errorf("не задан источник: укажите --from <имя>")
			}
			if err := requireDatabase(to); err != nil {
				return fmt.Errorf("не задан приёмник: укажите --to <имя>")
			}
			service, err := a.service()
			if err != nil {
				return err
			}
			result, err := service.Clone(cmd.Context(), backup.CloneOptions{
				From:     from,
				To:       to,
				BackupID: backupID,
				Operator: operator(),
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(a.stdout, result.Job.Message)
			return err
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "база-источник (обязательно)")
	cmd.Flags().StringVar(&to, "to", "", "база-приёмник, стенд (обязательно)")
	cmd.Flags().StringVar(&backupID, "backup", "", "бэкап источника (по умолчанию — последний)")
	return cmd
}
