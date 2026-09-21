package trivy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
)

// fakeRunner — заглушка запуска внешних команд: позволяет проверить, какой
// именно способ выбран, без установленных trivy и docker.
type fakeRunner struct {
	output []byte
	err    error
	calls  []string
}

// Run реализует CommandRunner.
func (f *fakeRunner) Run(_ context.Context, bin string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(append([]string{bin}, args...), " "))
	return f.output, f.err
}

func readFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "trivy_report.json"))
	if err != nil {
		t.Fatalf("не удалось прочитать тестовый отчёт: %v", err)
	}
	return data
}

func stubLookPath(available map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path, ok := available[name]; ok {
			return path, nil
		}
		return "", exec.ErrNotFound
	}
}

func TestParseReport(t *testing.T) {
	summary, err := Parse(readFixture(t))
	if err != nil {
		t.Fatalf("Parse: неожиданная ошибка: %v", err)
	}

	if summary.TotalVulnerabilities != 4 {
		t.Errorf("TotalVulnerabilities = %d, ожидалось 4", summary.TotalVulnerabilities)
	}
	if summary.Misconfigurations != 2 {
		t.Errorf("Misconfigurations = %d, ожидалось 2", summary.Misconfigurations)
	}

	wantSeverities := map[string]int{
		string(analyze.SeverityCritical): 1,
		string(analyze.SeverityHigh):     1,
		string(analyze.SeverityMedium):   1,
		string(analyze.SeverityLow):      1,
	}
	for name, want := range wantSeverities {
		if got := summary.BySeverity[name]; got != want {
			t.Errorf("BySeverity[%q] = %d, ожидалось %d", name, got, want)
		}
	}

	if len(summary.Top) != 4 {
		t.Fatalf("Top содержит %d записей, ожидалось 4", len(summary.Top))
	}
	if summary.Top[0].Severity != analyze.SeverityCritical {
		t.Errorf("первая уязвимость имеет уровень %q, ожидался %q",
			summary.Top[0].Severity, analyze.SeverityCritical)
	}
	if summary.Top[0].Package != "libcrypto3" || summary.Top[0].FixedVersion != "3.1.3-r0" {
		t.Errorf("данные первой уязвимости разобраны неверно: %+v", summary.Top[0])
	}
}

func TestParseWithLogNoise(t *testing.T) {
	noisy := append([]byte("2026-09-21T10:00:00Z\tINFO\tNeed to update DB\n"), readFixture(t)...)
	summary, err := Parse(noisy)
	if err != nil {
		t.Fatalf("Parse: неожиданная ошибка: %v", err)
	}
	if summary.TotalVulnerabilities != 4 {
		t.Errorf("TotalVulnerabilities = %d, ожидалось 4", summary.TotalVulnerabilities)
	}
}

func TestParseUnknownSeverity(t *testing.T) {
	payload := []byte(`{"Results":[{"Vulnerabilities":[{"VulnerabilityID":"CVE-0000","Severity":"weird"}]}]}`)
	summary, err := Parse(payload)
	if err != nil {
		t.Fatalf("Parse: неожиданная ошибка: %v", err)
	}
	if got := summary.BySeverity[string(analyze.SeverityUnknown)]; got != 1 {
		t.Errorf("BySeverity[unknown] = %d, ожидалось 1", got)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "пустой ответ", payload: "   "},
		{name: "не JSON", payload: "scanner failed"},
		{name: "битый JSON", payload: `{"Results": [}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(test.payload)); err == nil {
				t.Errorf("Parse(%q): ожидалась ошибка", test.payload)
			}
		})
	}
}

func TestScanUsesBinaryFromPath(t *testing.T) {
	runner := &fakeRunner{output: readFixture(t)}

	scanner := NewCLI(Options{
		LookPath: stubLookPath(map[string]string{"trivy": "trivy"}),
		Runner:   runner,
	})
	summary, err := scanner.Scan(context.Background(), "postgres:15-alpine")
	if err != nil {
		t.Fatalf("Scan: неожиданная ошибка: %v", err)
	}
	if summary.Source != "trivy" {
		t.Errorf("Source = %q, ожидалось %q", summary.Source, "trivy")
	}
	if summary.TotalVulnerabilities != 4 {
		t.Errorf("TotalVulnerabilities = %d, ожидалось 4", summary.TotalVulnerabilities)
	}

	want := "trivy image --format json --quiet --scanners vuln,misconfig postgres:15-alpine"
	if len(runner.calls) != 1 || runner.calls[0] != want {
		t.Errorf("вызовы сканера = %v, ожидался один вызов %q", runner.calls, want)
	}
}

func TestScanFallsBackToDocker(t *testing.T) {
	runner := &fakeRunner{output: readFixture(t)}

	scanner := NewCLI(Options{
		LookPath:  stubLookPath(map[string]string{"docker": "docker"}),
		UseDocker: true,
		Runner:    runner,
	})
	summary, err := scanner.Scan(context.Background(), "postgres:15-alpine")
	if err != nil {
		t.Fatalf("Scan: неожиданная ошибка: %v", err)
	}
	if summary.Source != "docker:"+DefaultImage {
		t.Errorf("Source = %q, ожидалось %q", summary.Source, "docker:"+DefaultImage)
	}

	want := "docker run --rm " + DefaultImage +
		" image --format json --quiet --scanners vuln,misconfig postgres:15-alpine"
	if len(runner.calls) != 1 || runner.calls[0] != want {
		t.Errorf("вызовы сканера = %v, ожидался один вызов %q", runner.calls, want)
	}
}

func TestScanDoesNotUseDockerWhenDisabled(t *testing.T) {
	runner := &fakeRunner{output: readFixture(t)}

	scanner := NewCLI(Options{
		LookPath: stubLookPath(map[string]string{"docker": "docker"}),
		Runner:   runner,
	})
	if _, err := scanner.Scan(context.Background(), "alpine:3.20"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Scan без trivy вернул %v, ожидалась ErrNotFound", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("сканер не должен запускаться, вызовы: %v", runner.calls)
	}
}

func TestScanWithoutScanner(t *testing.T) {
	scanner := NewCLI(Options{LookPath: stubLookPath(nil), Runner: &fakeRunner{}})
	if _, err := scanner.Scan(context.Background(), "alpine:3.20"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Scan без trivy и docker вернул %v, ожидалась ErrNotFound", err)
	}
}

func TestScanReportsScannerError(t *testing.T) {
	const failure = "trivy: database is not reachable"

	scanner := NewCLI(Options{
		Bin:      "trivy",
		LookPath: stubLookPath(nil),
		Runner:   &fakeRunner{err: errors.New(failure)},
	})
	_, err := scanner.Scan(context.Background(), "alpine:3.20")
	if err == nil {
		t.Fatal("Scan: ожидалась ошибка сканера")
	}
	if !strings.Contains(err.Error(), failure) {
		t.Errorf("сообщение об ошибке %q не содержит %q", err.Error(), failure)
	}
}

func TestScanRejectsBrokenReport(t *testing.T) {
	scanner := NewCLI(Options{
		Bin:      "trivy",
		LookPath: stubLookPath(nil),
		Runner:   &fakeRunner{output: []byte("scanner failed")},
	})
	if _, err := scanner.Scan(context.Background(), "alpine:3.20"); err == nil {
		t.Fatal("Scan с испорченным отчётом должен вернуть ошибку")
	}
}
