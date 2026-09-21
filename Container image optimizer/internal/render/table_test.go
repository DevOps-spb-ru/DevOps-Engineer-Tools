package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
)

func sampleReport() *analyze.Report {
	meta := analyze.ImageMeta{
		ID:           "sha256:" + strings.Repeat("a", 64),
		RepoTags:     []string{"myapp:1.0"},
		Architecture: "amd64",
		OS:           "linux",
		Created:      "2026-01-02T03:04:05.000000000Z",
		Size:         500_000_000,
	}
	history := []analyze.HistoryLayer{
		{
			ID:        "sha256:" + strings.Repeat("b", 64),
			CreatedBy: "/bin/sh -c apt-get update && apt-get install -y curl",
			Size:      150_000_000,
		},
		{
			ID:        "sha256:" + strings.Repeat("c", 64),
			CreatedBy: "/bin/sh -c #(nop)  ENV API_TOKEN=abcdef",
			Size:      0,
		},
	}

	report := analyze.Build("myapp:1.0", meta, history, analyze.Options{
		Top:           analyze.DefaultTopLayers,
		MinLayerSize:  analyze.DefaultMinLayerSize,
		HugeLayerSize: analyze.DefaultHugeLayerSize,
	})
	report.Scan = &analyze.ScanSummary{
		Source:               "trivy",
		BySeverity:           map[string]int{"critical": 1, "high": 2},
		TotalVulnerabilities: 3,
		Misconfigurations:    1,
		Top: []analyze.Vulnerability{{
			ID:               "CVE-2026-0001",
			Package:          "libcrypto3",
			InstalledVersion: "3.1.2-r0",
			FixedVersion:     "3.1.3-r0",
			Severity:         analyze.SeverityCritical,
		}},
	}
	return report
}

func TestTableContainsKeySections(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := Table(buf, sampleReport()); err != nil {
		t.Fatalf("Table: неожиданная ошибка: %v", err)
	}
	output := buf.String()

	for _, want := range []string{
		"ОБРАЗ",
		"myapp:1.0",
		"linux/amd64",
		"500.0 MB",
		"САМЫЕ КРУПНЫЕ СЛОИ",
		"150.0 MB",
		"ЗАМЕЧАНИЯ ПО СБОРКЕ",
		"СКАНИРОВАНИЕ TRIVY",
		"trivy",
		"CRITICAL=1",
		"CVE-2026-0001",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("в отчёте нет фрагмента %q\n%s", want, output)
		}
	}
}

func TestTableWithoutScan(t *testing.T) {
	report := sampleReport()
	report.Scan = nil

	buf := &bytes.Buffer{}
	if err := Table(buf, report); err != nil {
		t.Fatalf("Table: неожиданная ошибка: %v", err)
	}
	if !strings.Contains(buf.String(), "--no-trivy") {
		t.Errorf("в отчёте нет информации о пропущенном сканировании:\n%s", buf.String())
	}
}

func TestTableWithScanError(t *testing.T) {
	report := sampleReport()
	report.Scan = &analyze.ScanSummary{Source: "trivy", Error: "trivy не найден"}

	buf := &bytes.Buffer{}
	if err := Table(buf, report); err != nil {
		t.Fatalf("Table: неожиданная ошибка: %v", err)
	}
	if !strings.Contains(buf.String(), "недоступно: trivy не найден") {
		t.Errorf("в отчёте нет причины недоступности сканера:\n%s", buf.String())
	}
}

func TestJSONRoundTrip(t *testing.T) {
	report := sampleReport()
	buf := &bytes.Buffer{}
	if err := JSON(buf, report); err != nil {
		t.Fatalf("JSON: неожиданная ошибка: %v", err)
	}

	var decoded analyze.Report
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("отчёт не является корректным JSON: %v", err)
	}
	if decoded.Image.Ref != report.Image.Ref {
		t.Errorf("Ref = %q, ожидалось %q", decoded.Image.Ref, report.Image.Ref)
	}
	if len(decoded.Findings) != len(report.Findings) {
		t.Errorf("находок в JSON %d, ожидалось %d", len(decoded.Findings), len(report.Findings))
	}
	if decoded.Scan == nil || decoded.Scan.TotalVulnerabilities != 3 {
		t.Errorf("данные сканирования потеряны: %+v", decoded.Scan)
	}
}
