package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/doctor"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// newDoctorCommand проверяет готовность сервера к работе сервиса.
func newDoctorCommand(a *appState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Проверить готовность сервера: PostgreSQL, каталоги, права, локали, безопасность",
		Long: fmt.Sprintf(`doctor выполняет только чтение и проверяет по порядку:

  * доступ к утилитам PostgreSQL под postgres через sudo -n -u;
  * доступность сервера и совпадение версий клиента и сервера;
  * права роли: может ли она создавать базы данных;
  * каталоги бэкапов и служебный каталог: права, запись, свободное место;
  * локали существующих баз (восстановление в новую базу требует их наличия);
  * чтение архива пользователем postgres — ловит отказы AppArmor;
  * юнит systemd и безопасность веб-интерфейса;
  * свежесть бэкапов обслуживаемых баз.

Код возврата 1 означает, что есть ошибки: такую проверку удобно ставить в CI
и в мониторинг. Настройки конфига переопределяются переменными окружения: %s.`,
			strings.Join(config.EnvNames(), ", ")),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := doctor.ValidateFormat(format); err != nil {
				return err
			}
			cfg, path, err := a.readConfig()
			if err != nil {
				return err
			}
			client := pg.NewClient(cfg.Postgres.ClientConfig(), nil, a.stderr)
			report := doctor.Run(cmd.Context(), doctor.Options{
				Cfg:        cfg,
				ConfigPath: path,
				PG:         client,
				FS:         doctor.OSFS{},
				Runner:     pg.ExecRunner{},
			})
			if err := report.Write(a.stdout, format); err != nil {
				return err
			}
			if code := report.ExitCode(); code != 0 {
				return &codedError{code: code, err: fmt.Errorf("проверки не пройдены: %s", report.Summary())}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", doctor.FormatTable, "формат отчёта: table или json")
	return cmd
}
