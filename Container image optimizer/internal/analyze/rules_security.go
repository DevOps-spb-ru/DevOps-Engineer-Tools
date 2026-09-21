package analyze

import (
	"fmt"
	"regexp"
	"strings"
)

// pipeToShellPattern ищет запуск скачанного скрипта напрямую из сети (curl ... | sh).
// Пробел и границы слова в шаблоне важны: иначе "| sha256sum" ложно считается запуском shell.
var pipeToShellPattern = regexp.MustCompile(`(?:curl|wget)\s[^|]{0,200}\|\s*(?:sudo\s+)?(?:ba|z|k)?sh\b`)

// securityFindings ищет небезопасные приёмы в командах слоя.
func securityFindings(layer Layer, command string) []Finding {
	findings := make([]Finding, 0, 2)

	if containsAny(command, "curl ", "wget ") && !strings.Contains(command, "rm ") {
		findings = append(findings, newFinding(RuleDownloadKept, SeverityInfo, layer,
			"скачанный файл не удаляется в том же слое",
			"удаляйте загруженное в той же команде RUN — иначе файл остаётся в слое навсегда"))
	}

	if pipeToShellPattern.MatchString(command) {
		findings = append(findings, newFinding(RuleCurlPipeShell, SeverityMedium, layer,
			"скрипт выполняется напрямую из сети (curl | sh)",
			"скачивайте скрипт, проверяйте контрольную сумму или подпись и только затем выполняйте"))
	}

	if containsAny(command, "env ", "arg ") {
		if keyword := firstMatch(command, secretKeywords); keyword != "" {
			findings = append(findings, newFinding(RuleSecretInLayer, SeverityHigh, layer,
				fmt.Sprintf("в слое зафиксировано значение, похожее на секрет (совпадение по %q)", keyword),
				"передавайте секреты через BuildKit secrets (RUN --mount=type=secret) или переменные окружения рантайма"))
		}
	}

	return findings
}

// buildFindings сообщает о командах сборки, которые остались в финальном образе.
func buildFindings(layer Layer, command string) []Finding {
	tool := firstMatch(command, buildToolCommands)
	if tool == "" {
		return nil
	}
	return []Finding{newFinding(RuleBuildToolInImage, SeverityMedium, layer,
		fmt.Sprintf("в финальном образе есть команда сборки %q", strings.TrimSpace(tool)),
		"вынесите сборку в stage-билдер и копируйте в финальный образ только артефакты (multi-stage build)")}
}
