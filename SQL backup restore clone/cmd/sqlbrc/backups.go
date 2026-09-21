package main

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// newBackupsCommand показывает бэкапы и применяет политику хранения.
func newBackupsCommand(a *appState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backups",
		Short: "Показать бэкапы и применить политику хранения",
		Long: `backups работает с каталогом сервиса (storage.dir):

  * list — что уже снято: идентификатор, время, размер, метка и состояние;
  * prune — уборка по политике хранения (storage.keep_last, storage.keep_days):
    удаляются старые архивы вместе с их файлами метаданных, а также незавершённые
    записи (.part), оставшиеся после сбоя. Флаг --dry-run показывает план уборки.

Файлы, которых сервис не создавал, не удаляются: уборка работает только с
архивами вида <имя базы>/<метка времени>.dump.`,
	}
	cmd.AddCommand(newBackupsListCommand(a), newBackupsPruneCommand(a))
	return cmd
}

// newBackupsListCommand печатает список бэкапов.
func newBackupsListCommand(a *appState) *cobra.Command {
	var database string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Показать бэкапы базы или всех баз",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			service, err := a.service()
			if err != nil {
				return err
			}
			listed, err := service.Backups(database)
			if err != nil {
				return err
			}
			return writeBackups(a.stdout, listed, service.Config().Storage)
		},
	}
	cmd.Flags().StringVar(&database, "db", "", "база (по умолчанию — все базы каталога сервиса)")
	return cmd
}

// writeBackups печатает список бэкапов таблицей и напоминает политику хранения:
// по строкам видно, что именно попадёт под уборку.
func writeBackups(w io.Writer, backups []store.Backup, storage config.StorageConfig) error {
	if len(backups) == 0 {
		_, err := fmt.Fprintln(w, "бэкапов нет")
		return err
	}
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "БЭКАП\tСОЗДАН\tРАЗМЕР\tМЕТКА\tСОСТОЯНИЕ"); err != nil {
		return err
	}
	for _, backup := range backups {
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
			backup.ID,
			backup.CreatedAt.UTC().Format(time.RFC3339),
			bytesize.HumanSize(backup.SizeBytes),
			tagText(backup.Tag),
			statusText(backup.Status),
		); err != nil {
			return err
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\nвсего %d; политика хранения: последние %d бэкапов и не старше %d дней\n",
		len(backups), storage.KeepLast, storage.KeepDays)
	return err
}

// tagText печатает метку бэкапа: у обычного бэкапа метки нет.
func tagText(tag string) string {
	if tag == "" {
		return "—"
	}
	return tag
}

// statusText печатает состояние бэкапа словами.
func statusText(status store.Status) string {
	switch status {
	case store.StatusComplete:
		return "готов"
	case store.StatusUnknown:
		return "без метаданных"
	default:
		return string(status)
	}
}

// newBackupsPruneCommand применяет политику хранения.
func newBackupsPruneCommand(a *appState) *cobra.Command {
	var database string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Удалить старые бэкапы по политике хранения",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := a.service()
			if err != nil {
				return err
			}
			results, err := service.Prune(cmd.Context(), backup.PruneOptions{
				Database: database,
				DryRun:   dryRun,
				Operator: operator(),
			})
			if err != nil {
				return err
			}
			return writePruneResults(a.stdout, results, dryRun)
		},
	}
	cmd.Flags().StringVar(&database, "db", "", "база (по умолчанию — все базы каталога сервиса)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "показать, что будет удалено, и ничего не удалять")
	return cmd
}

// writePruneResults печатает итог уборки: по каждой базе список бэкапов, которые
// удалены или были бы удалены.
func writePruneResults(w io.Writer, results []backup.PruneResult, dryRun bool) error {
	total := 0
	for _, result := range results {
		total += len(result.Removed)
		if _, err := fmt.Fprintf(w, "%s: %s\n", result.Database, removalText(dryRun, len(result.Removed))); err != nil {
			return err
		}
		for _, removed := range result.Removed {
			if _, err := fmt.Fprintf(w, "  %s\n", removed.ID); err != nil {
				return err
			}
		}
	}
	switch {
	case total == 0:
		_, err := fmt.Fprintln(w, "удалять нечего: политика хранения соблюдена")
		return err
	case dryRun:
		_, err := fmt.Fprintf(w, "\nвсего к удалению: %d; повторите без --dry-run, чтобы удалить\n", total)
		return err
	default:
		_, err := fmt.Fprintf(w, "\nудалено бэкапов: %d\n", total)
		return err
	}
}

// removalText описывает результат уборки по одной базе.
func removalText(dryRun bool, count int) string {
	if dryRun {
		return fmt.Sprintf("будет удалено: %d", count)
	}
	return fmt.Sprintf("удалено: %d", count)
}
