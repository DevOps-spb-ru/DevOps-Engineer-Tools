package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
)

// execute запускает команду с буферами вывода — так проверяются коды возврата без запуска Docker.
func execute(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunHelp(t *testing.T) {
	code, stdout, _ := execute(t, "--help")
	if code != 0 {
		t.Fatalf("код возврата = %d, ожидался 0", code)
	}
	if !strings.Contains(stdout, "Container image optimizer") {
		t.Errorf("в справке нет описания утилиты:\n%s", stdout)
	}
	if !strings.Contains(stdout, "analyze") {
		t.Errorf("в справке нет команды analyze:\n%s", stdout)
	}
}

func TestRunRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantHint string
	}{
		{
			name:     "без аргумента-образа",
			args:     []string{"analyze"},
			wantHint: "arg",
		},
		{
			name:     "неизвестный формат",
			args:     []string{"analyze", "--format", "yaml", "alpine:3.20"},
			wantHint: "--format",
		},
		{
			name:     "неизвестный порог --fail-on",
			args:     []string{"analyze", "--fail-on", "urgent", "alpine:3.20"},
			wantHint: "--fail-on",
		},
		{
			name:     "некорректный порог размера слоя",
			args:     []string{"analyze", "--min-layer-size", "10XB", "alpine:3.20"},
			wantHint: "--min-layer-size",
		},
		{
			name:     "неизвестная команда",
			args:     []string{"inspect", "alpine:3.20"},
			wantHint: "unknown command",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, _, stderr := execute(t, test.args...)
			if code != 1 {
				t.Fatalf("код возврата = %d, ожидался 1 (stderr: %s)", code, stderr)
			}
			if !strings.Contains(stderr, test.wantHint) {
				t.Errorf("сообщение %q не содержит %q", stderr, test.wantHint)
			}
		})
	}
}

func TestExceedsThreshold(t *testing.T) {
	tests := []struct {
		name      string
		report    *analyze.Report
		threshold analyze.Severity
		want      bool
	}{
		{
			name:      "замечаний и уязвимостей нет",
			report:    &analyze.Report{},
			threshold: analyze.SeverityHigh,
			want:      false,
		},
		{
			name:      "замечание ниже порога",
			report:    &analyze.Report{Findings: []analyze.Finding{{Severity: analyze.SeverityLow}}},
			threshold: analyze.SeverityHigh,
			want:      false,
		},
		{
			name:      "замечание на уровне порога",
			report:    &analyze.Report{Findings: []analyze.Finding{{Severity: analyze.SeverityHigh}}},
			threshold: analyze.SeverityHigh,
			want:      true,
		},
		{
			name:      "уязвимость выше порога",
			report:    &analyze.Report{Scan: &analyze.ScanSummary{BySeverity: map[string]int{"critical": 2}}},
			threshold: analyze.SeverityHigh,
			want:      true,
		},
		{
			name:      "уязвимость ниже порога",
			report:    &analyze.Report{Scan: &analyze.ScanSummary{BySeverity: map[string]int{"low": 5}}},
			threshold: analyze.SeverityHigh,
			want:      false,
		},
		{
			name:      "сканирование недоступно",
			report:    &analyze.Report{Scan: &analyze.ScanSummary{Error: "trivy не найден"}},
			threshold: analyze.SeverityInfo,
			want:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := exceedsThreshold(test.report, test.threshold); got != test.want {
				t.Errorf("exceedsThreshold = %v, ожидалось %v", got, test.want)
			}
		})
	}
}
