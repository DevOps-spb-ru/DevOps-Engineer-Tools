package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
)

// newJobsCommand показывает журнал задач: что сервис делал и чем это закончилось.
func newJobsCommand(a *appState) *cobra.Command {
	var limit int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Показать журнал задач",
		Long: `jobs читает журнал задач (<storage.state_dir>/jobs.jsonl): по записям видно,
кто и когда снимал бэкап, восстанавливал базу или убирал старые архивы, и чем
операция закончилась. Каждая задача записывается дважды — при запуске и по
завершении, поэтому в журнале различаются состояния running, done и failed.

В журнал не попадают пароли и содержимое дампов: только имена баз, время,
идентификаторы бэкапов и сообщения об ошибках.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			service, err := a.service()
			if err != nil {
				return err
			}
			stored, err := service.Jobs(limit)
			if err != nil {
				return err
			}
			if stored == nil {
				stored = []jobs.Job{}
			}
			if asJSON {
				encoder := json.NewEncoder(a.stdout)
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				return encoder.Encode(stored)
			}
			return writeJobs(a.stdout, stored)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "сколько последних записей показать (0 — все)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "машинный формат вывода")
	return cmd
}

// writeJobs печатает записи журнала таблицей.
func writeJobs(w io.Writer, stored []jobs.Job) error {
	if len(stored) == 0 {
		_, err := fmt.Fprintln(w, "журнал задач пуст")
		return err
	}
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "ЗАДАЧА\tВИД\tБАЗА\tСОСТОЯНИЕ\tНАЧАЛО\tРЕЗУЛЬТАТ"); err != nil {
		return err
	}
	for _, job := range stored {
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n",
			job.ID,
			string(job.Kind),
			databaseText(job),
			jobStatusText(job),
			job.StartedAt.UTC().Format(time.RFC3339),
			resultText(job),
		); err != nil {
			return err
		}
	}
	return table.Flush()
}

// databaseText печатает базу задачи: у клонирования видно и приёмник.
func databaseText(job jobs.Job) string {
	if job.Target != "" && job.Target != job.Database {
		return job.Database + " → " + job.Target
	}
	return job.Database
}

// jobStatusText печатает состояние задачи словами.
func jobStatusText(job jobs.Job) string {
	switch job.Status {
	case jobs.StatusDone:
		return "выполнено"
	case jobs.StatusFailed:
		return "ошибка"
	case jobs.StatusRunning:
		return "выполняется"
	default:
		return string(job.Status)
	}
}

// resultText печатает сообщение задачи или её ошибку.
func resultText(job jobs.Job) string {
	if job.Status == jobs.StatusRunning {
		// Незавершённая запись означает сбой процесса: операция не вернула итог.
		return "задача не завершена: процесс остановился"
	}
	if job.Error != "" {
		return strings.Join(strings.Fields(job.Error), " ")
	}
	return job.Message
}
