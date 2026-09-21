package render

import (
	"fmt"
	"strings"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
)

const (
	// maxErrorLines — сколько строк причины показывать: у Trivy причина сбоя
	// часто находится в конце цепочки ошибок, поэтому сохраняются и начало, и конец.
	maxErrorLines = 12
	// maxErrorLineSize — предел длины одной строки: длинные строки сжимаются
	// с сохранением начала и конца.
	maxErrorLineSize = 300
	// errorIndent — отступ для продолжений сообщения об ошибке.
	errorIndent = "              "
)

// writeScanFailure печатает причину недоступности сканера. Раньше выводилась только
// первая строка, обрезанная до 220 символов, и реальная причина терялась.
// Отчёт передаётся целиком: подсказке нужен ещё и образ, чтобы напечатать
// готовые команды восстановления.
func writeScanFailure(buf *strings.Builder, report *analyze.Report) {
	scan := report.Scan
	lines := errorLines(scan.Error)
	fmt.Fprintf(buf, "  недоступно: %s\n", lines[0])
	for _, line := range lines[1:] {
		fmt.Fprintf(buf, "%s%s\n", errorIndent, line)
	}
	if hint := hintForScanError(scan.Error, report.Image.Ref); hint != "" {
		fmt.Fprintf(buf, "  подсказка:  %s\n", hint)
	}
	if scan.Command != "" {
		fmt.Fprintf(buf, "  команда:    %s\n", scan.Command)
	}
}

// errorLines приводит многострочный вывод сканера к виду, пригодному для отчёта:
// убирает кадры трассировки стека, сжимает пробелы и оставляет начало и конец
// слишком длинного сообщения.
func errorLines(message string) []string {
	raw := strings.Split(strings.ReplaceAll(message, "\t", " "), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		compact := strings.Join(strings.Fields(line), " ")
		if compact == "" || isStackFrame(compact) {
			continue
		}
		lines = append(lines, abbreviateLine(compact, maxErrorLineSize))
	}
	if len(lines) == 0 {
		return []string{"нет данных"}
	}
	if len(lines) <= maxErrorLines {
		return lines
	}

	head := maxErrorLines / 2
	tail := maxErrorLines - head - 1
	short := append([]string{}, lines[:head]...)
	short = append(short, fmt.Sprintf("… пропущено строк: %d", len(lines)-maxErrorLines))
	return append(short, lines[len(lines)-tail:]...)
}

// isStackFrame определяет строки трассировки стека Go: они не объясняют причину
// сбоя и только засоряют отчёт.
func isStackFrame(line string) bool {
	// Путь к исходнику Trivy: /home/runner/work/trivy/trivy/pkg/.../scanner.go:261.
	if strings.Contains(line, ".go:") {
		return true
	}
	// Кадр стека без пробелов: github.com/aquasecurity/trivy/pkg/.../artifact.createLocalService.
	// Ссылки не считаем кадром: в них тоже нет пробелов и есть точка в конце пути.
	if strings.ContainsAny(line, " \t") || strings.Contains(line, "://") {
		return false
	}
	slash := strings.LastIndex(line, "/")
	return slash >= 0 && strings.Contains(line[slash+1:], ".")
}

// abbreviateLine сжимает слишком длинную строку, сохраняя её начало и конец.
func abbreviateLine(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	head := limit / 2
	tail := limit - head - len([]rune(" … "))
	return string(runes[:head]) + " … " + string(runes[len(runes)-tail:])
}
