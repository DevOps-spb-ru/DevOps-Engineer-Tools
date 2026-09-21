package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

// Форматы отчёта.
const (
	// FormatTable — таблица для человека в терминале.
	FormatTable = "table"
	// FormatJSON — JSON для скриптов и CI.
	FormatJSON = "json"
)

// ValidateFormat проверяет формат отчёта до выполнения проверок: опечатка
// в флаге должна выясняться сразу, а не после минуты работы с sudo.
func ValidateFormat(format string) error {
	switch format {
	case FormatTable, FormatJSON:
		return nil
	default:
		return fmt.Errorf("неизвестный формат отчёта %q (поддерживаются %s и %s)", format, FormatTable, FormatJSON)
	}
}

// Write печатает отчёт в выбранном формате.
func (r Report) Write(w io.Writer, format string) error {
	if err := ValidateFormat(format); err != nil {
		return err
	}
	if format == FormatJSON {
		return r.WriteJSON(w)
	}
	return r.WriteText(w)
}

// WriteText печатает отчёт таблицей: уровень, имя проверки, результат и подсказка.
func (r Report) WriteText(w io.Writer) error {
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "УРОВЕНЬ\tПРОВЕРКА\tРЕЗУЛЬТАТ"); err != nil {
		return err
	}
	for _, check := range r.Checks {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", check.Level, check.Name, check.Message); err != nil {
			return err
		}
		if check.Hint != "" {
			if _, err := fmt.Fprintf(writer, "\t\t→ %s\n", check.Hint); err != nil {
				return err
			}
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%s\n", r.Summary())
	return err
}

// WriteJSON печатает отчёт в виде JSON: формат читают скрипты и CI.
func (r Report) WriteJSON(w io.Writer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(r)
}

// Summary возвращает строку «проверок 12: ok 9, предупреждений 2, ошибок 1».
func (r Report) Summary() string {
	return fmt.Sprintf("проверок %d: ok %d, предупреждений %d, ошибок %d",
		len(r.Checks), r.count(LevelOK), r.Warnings(), r.Errors())
}
