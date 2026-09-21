// Package render выводит отчёт в человекочитаемом виде или в JSON.
package render

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
)

const (
	separator      = "--------------------------------------------------------------------------------"
	maxCommandSize = 96
	maxErrorSize   = 220
)

// Table печатает человекочитаемый отчёт.
func Table(w io.Writer, report *analyze.Report) error {
	buf := &strings.Builder{}
	fmt.Fprintf(buf, "Container image optimizer — отчёт по образу\n%s\n", separator)
	writeImageSummary(buf, report)
	writeLayerTable(buf, report)
	writeFindings(buf, report)
	writeScan(buf, report)
	_, err := io.WriteString(w, buf.String())
	return err
}

func writeImageSummary(buf *strings.Builder, report *analyze.Report) {
	fmt.Fprintf(buf, "\nОБРАЗ\n")
	fmt.Fprintf(buf, "  Ссылка:      %s\n", report.Image.Ref)
	fmt.Fprintf(buf, "  ID:          %s\n", analyze.ShortID(report.Image.ID))
	if len(report.Image.Tags) > 0 {
		fmt.Fprintf(buf, "  Теги:        %s\n", strings.Join(report.Image.Tags, ", "))
	}
	fmt.Fprintf(buf, "  Платформа:   %s\n", platform(report))
	fmt.Fprintf(buf, "  Создан:      %s\n", formatCreated(report.Image.Created))
	fmt.Fprintf(buf, "  Размер:      %s\n", report.Image.TotalSizeHuman)
	fmt.Fprintf(buf, "  Слоёв:       %d (непустых: %d)\n", report.Image.LayerCount, report.Image.NonEmptyLayers)
}

func writeLayerTable(buf *strings.Builder, report *analyze.Report) {
	fmt.Fprintf(buf, "\nСАМЫЕ КРУПНЫЕ СЛОИ (%d)\n", len(report.TopLayers))
	if len(report.TopLayers) == 0 {
		fmt.Fprintf(buf, "  нет данных\n")
		return
	}

	table := tabwriter.NewWriter(buf, 0, 0, 2, ' ', 0)
	// tabwriter копит записи в буфере: ошибки записи проявятся только на Flush,
	// поэтому отдельные вызовы Fprintf здесь намеренно не проверяются.
	_, _ = fmt.Fprintln(table, "  №\tРАЗМЕР\tДОЛЯ\tКОМАНДА")
	for _, layer := range report.TopLayers {
		_, _ = fmt.Fprintf(table, "  %d\t%s\t%.1f%%\t%s\n",
			layer.Index, layer.SizeHuman, share(layer.Size, report.Image.TotalSize), truncate(layer.CreatedBy))
	}
	_ = table.Flush()
}

// share считает долю слоя в общем размере образа в процентах.
func share(size, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(size) / float64(total) * 100
}

func platform(report *analyze.Report) string {
	osName := orDash(report.Image.OS)
	architecture := orDash(report.Image.Architecture)
	return osName + "/" + architecture
}

func formatCreated(value string) string {
	if value == "" {
		return "нет данных"
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, value)
	}
	if err != nil {
		return value
	}
	return parsed.UTC().Format("2006-01-02 15:04:05 UTC")
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

// truncate сжимает пробелы и обрезает длинную команду, чтобы отчёт оставался читаемым.
func truncate(value string) string {
	return truncateMessage(value, maxCommandSize)
}

// truncateMessage сжимает пробелы и обрезает текст до limit символов,
// чтобы длинные сообщения сканера не ломали вывод.
func truncateMessage(value string, limit int) string {
	compact := strings.Join(strings.Fields(value), " ")
	if compact == "" {
		return "нет данных"
	}
	runes := []rune(compact)
	if len(runes) <= limit {
		return compact
	}
	return string(runes[:limit-1]) + "…"
}

// firstLine возвращает первую строку многострочного сообщения.
func firstLine(value string) string {
	if index := strings.IndexAny(value, "\r\n"); index >= 0 {
		return value[:index]
	}
	return value
}
