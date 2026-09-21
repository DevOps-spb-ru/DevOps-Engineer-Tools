package trivy

import "testing"

// FuzzParse проверяет разбор отчёта Trivy на произвольных данных: формат чужих
// отчётов меняется, и паника на неожиданном JSON недопустима.
func FuzzParse(f *testing.F) {
	seeds := []string{
		``,
		`{}`,
		`{"ArtifactName":"alpine:3.20","Results":[]}`,
		`2026-09-21T10:00:00Z	INFO	[vuln] Vulnerability scanning is enabled` +
			`{"ArtifactName":"app:1.0","Results":[{"Target":"app","Class":"os-pkgs",` +
			`"Vulnerabilities":[{"VulnerabilityID":"CVE-2026-1","PkgName":"openssl",` +
			`"InstalledVersion":"1.0","FixedVersion":"1.1","Severity":"HIGH","Title":"t"}],` +
			`"Misconfigurations":[{"ID":"DS-0002","Severity":"HIGH","Title":"root user"}]}]}`,
		`{"Results":[{"Target":"x","Vulnerabilities":[{"Severity":""},{"Severity":"unknown"}]}]}`,
		`{"Results":[{"Vulnerabilities":null,"Misconfigurations":null}]}`,
		`{"Results":[`,
		`null`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data string) {
		summary, err := Parse([]byte(data))
		if err != nil {
			if summary != nil {
				t.Fatalf("при ошибке вернулась сводка: %+v", summary)
			}
			return
		}
		if summary == nil {
			t.Fatal("без ошибки вернулась пустая сводка")
		}
		if len(summary.Top) > maxTopVulnerabilities {
			t.Errorf("в отчёте %d уязвимостей, максимум %d", len(summary.Top), maxTopVulnerabilities)
		}
		total := 0
		for _, count := range summary.BySeverity {
			total += count
		}
		if total != summary.TotalVulnerabilities {
			t.Errorf("сумма по уровням = %d, всего уязвимостей = %d",
				total, summary.TotalVulnerabilities)
		}
		for i := 1; i < len(summary.Top); i++ {
			if summary.Top[i-1].Severity.Rank() < summary.Top[i].Severity.Rank() {
				t.Errorf("нарушен порядок по значимости: %q (%d) → %q (%d)",
					summary.Top[i-1].ID, summary.Top[i-1].Severity.Rank(),
					summary.Top[i].ID, summary.Top[i].Severity.Rank())
			}
		}
	})
}
