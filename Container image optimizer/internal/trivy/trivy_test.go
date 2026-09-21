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

// stubLookupEnv имитирует окружение процесса: проверяется проброс переменных
// с кредами реестра в контейнер по имени, без реальных секретов в тестах.
func stubLookupEnv(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

// wantDockerPrefix — начало команды docker-fallback: том с базой уязвимостей.
const wantDockerPrefix = "docker run --rm -v " + DefaultCacheVolume + ":/root/.cache/trivy "

// wantScannerArgs — базовые аргументы сканера.
const wantScannerArgs = "image --format json --quiet --scanners vuln,misconfig"

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
		EnvNames:  []string{},
		Runner:    runner,
	})
	summary, err := scanner.Scan(context.Background(), "postgres:15-alpine")
	if err != nil {
		t.Fatalf("Scan: неожиданная ошибка: %v", err)
	}
	if summary.Source != "docker:"+DefaultImage {
		t.Errorf("Source = %q, ожидалось %q", summary.Source, "docker:"+DefaultImage)
	}

	want := wantDockerPrefix + DefaultImage + " " + wantScannerArgs + " postgres:15-alpine"
	if len(runner.calls) != 1 || runner.calls[0] != want {
		t.Errorf("вызовы сканера = %v, ожидался один вызов %q", runner.calls, want)
	}
	if summary.Command != want {
		t.Errorf("Command = %q, ожидалось %q", summary.Command, want)
	}
}

func TestScanDockerFallbackOptions(t *testing.T) {
	const image = "docker-hub.iitdgroup.ru/finsynapse/back:5.4.1"

	tests := []struct {
		name string
		opts Options
		env  map[string]string
		want string
	}{
		{
			name: "сокет демона и источник docker",
			opts: Options{DockerSocket: "//var/run/docker.sock", ImageSource: "docker"},
			want: wantDockerPrefix + "-v //var/run/docker.sock:/var/run/docker.sock " + DefaultImage +
				" " + wantScannerArgs + " --image-src docker " + image,
		},
		{
			name: "дополнительные аргументы пользователя",
			opts: Options{ExtraArgs: []string{"--skip-db-update", "--severity", "HIGH,CRITICAL"}},
			want: wantDockerPrefix + DefaultImage + " " + wantScannerArgs +
				" --skip-db-update --severity HIGH,CRITICAL " + image,
		},
		{
			name: "кэш базы отключён",
			opts: Options{CacheVolume: DisableCacheVolume},
			want: "docker run --rm " + DefaultImage + " " + wantScannerArgs + " " + image,
		},
		{
			name: "креды реестра пробрасываются по имени",
			opts: Options{EnvNames: []string{"TRIVY_USERNAME", "TRIVY_PASSWORD", "TRIVY_INSECURE"}},
			env: map[string]string{
				"TRIVY_USERNAME": "ci-reader",
				"TRIVY_PASSWORD": "supersecret",
			},
			want: wantDockerPrefix + "-e TRIVY_USERNAME -e TRIVY_PASSWORD " + DefaultImage +
				" " + wantScannerArgs + " " + image,
		},
		{
			name: "источник remote без локального демона",
			opts: Options{ImageSource: "remote"},
			want: wantDockerPrefix + DefaultImage + " " + wantScannerArgs + " --image-src remote " + image,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{output: readFixture(t)}
			opts := test.opts
			opts.UseDocker = true
			opts.Runner = runner
			opts.LookPath = stubLookPath(map[string]string{"docker": "docker"})
			if opts.EnvNames == nil {
				opts.EnvNames = []string{}
			}
			if test.env != nil {
				opts.LookupEnv = stubLookupEnv(test.env)
			}

			scanner := NewCLI(opts)
			if _, err := scanner.Scan(context.Background(), image); err != nil {
				t.Fatalf("Scan: неожиданная ошибка: %v", err)
			}
			if len(runner.calls) != 1 || runner.calls[0] != test.want {
				t.Errorf("команда сканера:\n получено: %v\nожидалось: %q", runner.calls, test.want)
			}
		})
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

func TestScanBinaryHonorsScannerOptions(t *testing.T) {
	runner := &fakeRunner{output: readFixture(t)}

	scanner := NewCLI(Options{
		Bin:         "trivy",
		ImageSource: "remote",
		ExtraArgs:   []string{"--skip-db-update"},
		LookPath:    stubLookPath(nil),
		Runner:      runner,
	})
	image := "docker-hub.iitdgroup.ru/finsynapse/back:5.4.1"
	summary, err := scanner.Scan(context.Background(), image)
	if err != nil {
		t.Fatalf("Scan: неожиданная ошибка: %v", err)
	}

	want := "trivy " + wantScannerArgs + " --image-src remote --skip-db-update " + image
	if len(runner.calls) != 1 || runner.calls[0] != want {
		t.Errorf("вызовы сканера = %v, ожидался один вызов %q", runner.calls, want)
	}
	if summary.Command != want {
		t.Errorf("Command = %q, ожидалось %q", summary.Command, want)
	}
}

func TestScanErrorCarriesCommand(t *testing.T) {
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

	var scanError *ScanError
	if !errors.As(err, &scanError) {
		t.Fatalf("Scan вернул %v, ожидалась ScanError с командой запуска", err)
	}
	if scanError.Source != "trivy" {
		t.Errorf("Source = %q, ожидалось %q", scanError.Source, "trivy")
	}
	wantCommand := "trivy " + wantScannerArgs + " alpine:3.20"
	if scanError.Command != wantCommand {
		t.Errorf("Command = %q, ожидалось %q", scanError.Command, wantCommand)
	}
	if !strings.Contains(scanError.Error(), failure) {
		t.Errorf("сообщение %q не содержит %q", scanError.Error(), failure)
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

func TestSocketPath(t *testing.T) {
	tests := []struct {
		name string
		host string
		goos string
		want string
	}{
		{name: "linux по умолчанию", goos: "linux", want: "/var/run/docker.sock"},
		{name: "windows по умолчанию", goos: "windows", want: "//var/run/docker.sock"},
		{
			name: "unix-сокет из DOCKER_HOST",
			host: "unix:///run/docker.sock",
			goos: "linux",
			want: "/run/docker.sock",
		},
		{
			name: "named pipe Docker Desktop",
			host: "npipe:////./pipe/docker_engine",
			goos: "windows",
			want: "//var/run/docker.sock",
		},
		{name: "удалённый демон по TCP", host: "tcp://10.0.0.1:2375", goos: "linux", want: ""},
		{name: "демон по ssh", host: "ssh://user@host", goos: "linux", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SocketPath(test.host, test.goos); got != test.want {
				t.Errorf("SocketPath(%q, %q) = %q, ожидалось %q", test.host, test.goos, got, test.want)
			}
		})
	}
}

func TestValidateImageSrc(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "не задано", value: ""},
		{name: "один источник", value: "docker"},
		{name: "несколько источников", value: "docker,remote"},
		{name: "пробелы внутри списка", value: "docker, remote"},
		{name: "опечатка", value: "docer", wantErr: true},
		{name: "частично неизвестный источник", value: "docker,vm", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateImageSrc(test.value)
			if test.wantErr && err == nil {
				t.Errorf("ValidateImageSrc(%q): ожидалась ошибка", test.value)
			}
			if !test.wantErr && err != nil {
				t.Errorf("ValidateImageSrc(%q): неожиданная ошибка: %v", test.value, err)
			}
		})
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
