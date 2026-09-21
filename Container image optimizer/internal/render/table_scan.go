package render

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
)

func writeFindings(buf *strings.Builder, report *analyze.Report) {
	fmt.Fprintf(buf, "\nЗАМЕЧАНИЯ ПО СБОРКЕ (%d)\n", len(report.Findings))
	if len(report.Findings) == 0 {
		fmt.Fprintf(buf, "  замечаний нет\n")
		return
	}
	for _, finding := range report.Findings {
		fmt.Fprintf(buf, "  [%-8s] %s\n", strings.ToUpper(string(finding.Severity)), layerRef(finding))
		fmt.Fprintf(buf, "  %s\n", finding.Message)
		fmt.Fprintf(buf, "  %s рекомендация: %s\n", strings.Repeat(" ", 10), finding.Recommendation)
	}
}

func layerRef(finding analyze.Finding) string {
	if finding.LayerIndex < 0 {
		return "правило " + finding.Rule
	}
	if finding.LayerID == "" {
		return fmt.Sprintf("правило %s, слой %d", finding.Rule, finding.LayerIndex)
	}
	return fmt.Sprintf("правило %s, слой %d (%s)", finding.Rule, finding.LayerIndex, finding.LayerID)
}

func writeScan(buf *strings.Builder, report *analyze.Report) {
	fmt.Fprintf(buf, "\nСКАНИРОВАНИЕ TRIVY\n")
	scan := report.Scan
	switch {
	case scan == nil:
		fmt.Fprintf(buf, "  сканирование не запускалось (--no-trivy)\n")
		return
	case scan.Error != "":
		writeScanFailure(buf, scan)
		return
	}

	fmt.Fprintf(buf, "  Источник:        %s\n", scan.Source)
	fmt.Fprintf(buf, "  Уязвимостей:     %d\n", scan.TotalVulnerabilities)
	fmt.Fprintf(buf, "  Мисконфигураций: %d\n", scan.Misconfigurations)
	if severityList := severityLine(scan); severityList != "" {
		fmt.Fprintf(buf, "  По уровням:      %s\n", severityList)
	}
	if len(scan.Top) == 0 {
		return
	}

	fmt.Fprintf(buf, "  Самые критичные уязвимости:\n")
	table := tabwriter.NewWriter(buf, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "    ID\tУРОВЕНЬ\tПАКЕТ\tУСТАНОВЛЕНО\tИСПРАВЛЕНО")
	for _, found := range scan.Top {
		_, _ = fmt.Fprintf(table, "    %s\t%s\t%s\t%s\t%s\n", found.ID,
			strings.ToUpper(string(found.Severity)), orDash(found.Package),
			orDash(found.InstalledVersion), orDash(found.FixedVersion))
	}
	_ = table.Flush()
}

func severityLine(scan *analyze.ScanSummary) string {
	parts := make([]string, 0, len(scan.BySeverity))
	for _, severity := range analyze.SeverityOrder() {
		if count := scan.BySeverity[string(severity)]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", strings.ToUpper(string(severity)), count))
		}
	}
	return strings.Join(parts, "  ")
}
