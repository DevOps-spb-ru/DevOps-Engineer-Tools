package trivy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
)

// maxTopVulnerabilities — сколько уязвимостей показывать в отчёте детально.
const maxTopVulnerabilities = 10

// report — структура JSON-отчёта Trivy (только нужные поля).
type report struct {
	ArtifactName string   `json:"ArtifactName"`
	Results      []result `json:"Results"`
}

type result struct {
	Target            string             `json:"Target"`
	Class             string             `json:"Class"`
	Vulnerabilities   []vulnerability    `json:"Vulnerabilities"`
	Misconfigurations []misconfiguration `json:"Misconfigurations"`
}

type vulnerability struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
}

type misconfiguration struct {
	ID       string `json:"ID"`
	Title    string `json:"Title"`
	Severity string `json:"Severity"`
}

// Parse разбирает JSON-отчёт Trivy. Строки лога перед JSON игнорируются.
func Parse(data []byte) (*analyze.ScanSummary, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("пустой ответ сканера")
	}

	payload := data
	if index := bytes.IndexByte(data, '{'); index > 0 {
		payload = data[index:]
	}

	var parsed report
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, fmt.Errorf("некорректный JSON отчёта: %w", err)
	}

	summary := &analyze.ScanSummary{BySeverity: make(map[string]int)}
	for _, item := range parsed.Results {
		for _, found := range item.Vulnerabilities {
			severity := normalizeSeverity(found.Severity)
			summary.BySeverity[string(severity)]++
			summary.TotalVulnerabilities++
			summary.Top = append(summary.Top, analyze.Vulnerability{
				ID:               found.VulnerabilityID,
				Package:          found.PkgName,
				InstalledVersion: found.InstalledVersion,
				FixedVersion:     found.FixedVersion,
				Severity:         severity,
				Title:            found.Title,
			})
		}
		summary.Misconfigurations += len(item.Misconfigurations)
	}

	sort.SliceStable(summary.Top, func(i, j int) bool {
		left, right := summary.Top[i], summary.Top[j]
		if left.Severity.Rank() != right.Severity.Rank() {
			return left.Severity.Rank() > right.Severity.Rank()
		}
		return left.ID < right.ID
	})
	if len(summary.Top) > maxTopVulnerabilities {
		summary.Top = summary.Top[:maxTopVulnerabilities]
	}
	if len(summary.BySeverity) == 0 {
		summary.BySeverity = nil
	}
	return summary, nil
}

// normalizeSeverity приводит уровень значимости Trivy к модели отчёта.
func normalizeSeverity(value string) analyze.Severity {
	switch severity := analyze.Severity(strings.ToLower(strings.TrimSpace(value))); severity {
	case analyze.SeverityCritical, analyze.SeverityHigh, analyze.SeverityMedium,
		analyze.SeverityLow, analyze.SeverityInfo:
		return severity
	default:
		return analyze.SeverityUnknown
	}
}
