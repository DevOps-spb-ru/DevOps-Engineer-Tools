// Package analyze содержит доменную модель отчёта и правила анализа образа.
package analyze

import (
	"fmt"
	"strings"
)

// Severity — уровень значимости находки или уязвимости.
type Severity string

// Уровни значимости.
const (
	SeverityUnknown  Severity = "unknown"
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

var severityRanks = map[Severity]int{
	SeverityUnknown:  -1,
	SeverityInfo:     0,
	SeverityLow:      1,
	SeverityMedium:   2,
	SeverityHigh:     3,
	SeverityCritical: 4,
}

// SeverityOrder возвращает уровни значимости от самого критичного к самому слабому.
func SeverityOrder() []Severity {
	return []Severity{
		SeverityCritical,
		SeverityHigh,
		SeverityMedium,
		SeverityLow,
		SeverityInfo,
		SeverityUnknown,
	}
}

// Rank возвращает числовой ранг значимости: чем больше число, тем критичнее уровень.
func (s Severity) Rank() int {
	rank, ok := severityRanks[s]
	if !ok {
		return -1
	}
	return rank
}

// AtLeast сообщает, что уровень значимости не ниже порога.
func (s Severity) AtLeast(threshold Severity) bool {
	if threshold == "" {
		return false
	}
	return s.Rank() >= threshold.Rank()
}

// ParseSeverity разбирает имя уровня значимости.
// Пустая строка даёт ("", false, nil) — это означает, что порог не задан.
func ParseSeverity(value string) (Severity, bool, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return "", false, nil
	}
	severity := Severity(normalized)
	rank, ok := severityRanks[severity]
	if !ok || rank < 0 {
		return "", false, fmt.Errorf(
			"неизвестный уровень %q (ожидается info, low, medium, high или critical)", value)
	}
	return severity, true, nil
}
