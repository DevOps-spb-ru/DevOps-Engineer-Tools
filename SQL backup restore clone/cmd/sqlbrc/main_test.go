package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/doctor"
)

// execute запускает команду с буферами вывода — так проверяются коды возврата
// без запуска sudo, psql и pg_dump.
func execute(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// writeConfig создаёт временный конфиг: минимальный набор из deploy/config.example.yaml.
func writeConfig(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("не удалось создать конфиг: %v", err)
	}
	return path
}

// validConfigDocument — конфиг из README: интерфейс на 0.0.0.0:8088 без TLS
// с осознанным allow_insecure, один встроенный пользователь, каталог /var/backups.
const validConfigDocument = `
server:
  listen: "0.0.0.0:8088"
  allow_insecure: true
auth:
  users:
    - login: admin
      password_bcrypt: "$2y$10$abcdefghijklmnopqrstuu0123456789abcdefghijklmnopqrstu"
postgres:
  sudo_user: postgres
storage:
  dir: "/var/backups"
  keep_last: 7
`

func TestRunHelp(t *testing.T) {
	code, stdout, _ := execute(t, "--help")
	if code != 0 {
		t.Fatalf("код возврата = %d, ожидался 0", code)
	}
	for _, want := range []string{"sqlbrc", "doctor", "version", "sudo -n -u"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в справке нет %q:\n%s", want, stdout)
		}
	}
}

func TestRunVersion(t *testing.T) {
	code, stdout, stderr := execute(t, "--version")
	if code != 0 {
		t.Fatalf("код возврата = %d, ожидался 0 (stderr: %s)", code, stderr)
	}
	// Значения по умолчанию задаются в main.go; при сборке через -ldflags подставляется версия тега.
	for _, want := range []string{version, commit, date} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в выводе --version нет %q:\n%s", want, stdout)
		}
	}
	if !strings.HasPrefix(stdout, "sqlbrc ") {
		t.Errorf("вывод --version должен начинаться с имени утилиты:\n%s", stdout)
	}
}

func TestRunVersionCommand(t *testing.T) {
	code, stdout, stderr := execute(t, "version")
	if code != 0 {
		t.Fatalf("код возврата = %d, ожидался 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, versionString()) {
		t.Errorf("команда version напечатала %q, ожидалась строка %q", stdout, versionString())
	}
}

func TestRunRejectsInvalidInput(t *testing.T) {
	configPath := writeConfig(t, validConfigDocument)

	tests := []struct {
		name     string
		args     []string
		wantHint string
	}{
		{
			name:     "неизвестная команда",
			args:     []string{"backup", "--db", "fse-1"},
			wantHint: "unknown command",
		},
		{
			name:     "неизвестный формат отчёта",
			args:     []string{"doctor", "--format", "yaml", "--config", configPath},
			wantHint: doctor.FormatJSON,
		},
		{
			name:     "неизвестный уровень журнала",
			args:     []string{"--log-level", "verbose", "version"},
			wantHint: "--log-level",
		},
		{
			name:     "неизвестный формат журнала",
			args:     []string{"--log-format", "xml", "version"},
			wantHint: "--log-format",
		},
		{
			name:     "лишний аргумент у version",
			args:     []string{"version", "лишнее"},
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

// TestRunDoctorWithoutConfig проверяет, что отсутствие конфига — понятная ошибка
// с подсказкой про образец, а не паника или пустой отчёт.
func TestRunDoctorWithoutConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "нет-такого.yaml")
	code, stdout, stderr := execute(t, "doctor", "--config", missing)
	if code != 1 {
		t.Fatalf("код возврата = %d, ожидался 1", code)
	}
	if !strings.Contains(stderr, "не найден") || !strings.Contains(stderr, "config.example.yaml") {
		t.Errorf("ошибка не объясняет, где взять конфиг: %s", stderr)
	}
	if stdout != "" {
		t.Errorf("при отсутствующем конфиге что-то напечатано в stdout: %q", stdout)
	}
}

// TestRunDoctorInvalidConfig проверяет, что ошибки конфига видны в отчёте как
// проверка config, а код возврата равен 1: так doctor работает «воротами» в CI.
func TestRunDoctorInvalidConfig(t *testing.T) {
	configPath := writeConfig(t, strings.Replace(validConfigDocument, "keep_last: 7", "keep_last: 0", 1))

	code, stdout, stderr := execute(t, "doctor", "--config", configPath)
	if code != 1 {
		t.Fatalf("код возврата = %d, ожидался 1", code)
	}
	if !strings.Contains(stdout, "config") || !strings.Contains(stdout, "keep_last") {
		t.Errorf("отчёт не показывает ошибку конфига:\n%s", stdout)
	}
	if !strings.Contains(stdout, "ошибок 1") {
		t.Errorf("в сводке нет числа ошибок:\n%s", stdout)
	}
	if !strings.Contains(stderr, "проверки не пройдены") {
		t.Errorf("в stderr нет причины кода возврата: %s", stderr)
	}
}

// TestRunDoctorJSON проверяет машинный формат отчёта. Проверки идут на реальном
// окружении тестовой машины: команд sudo и psql там нет, поэтому отчёт содержит
// ошибки — важно, что он формируется целиком и разбирается как JSON.
func TestRunDoctorJSON(t *testing.T) {
	configPath := writeConfig(t, validConfigDocument)
	code, stdout, stderr := execute(t, "doctor", "--format", "json", "--config", configPath)
	if code != 1 {
		t.Fatalf("код возврата = %d, ожидался 1 (stderr: %s)", code, stderr)
	}

	var report doctor.Report
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("вывод не является JSON-отчётом: %v\n%s", err, stdout)
	}
	if len(report.Checks) < 10 {
		t.Errorf("в отчёте %d проверок, ожидалось не меньше 10", len(report.Checks))
	}
	if report.ConfigPath != configPath {
		t.Errorf("путь к конфигу в отчёте = %q, ожидался %q", report.ConfigPath, configPath)
	}
	// Конфиг из README обязан быть корректным: ошибка здесь означала бы, что
	// образец для администратора не работает.
	var configCheck *doctor.Check
	for index := range report.Checks {
		if report.Checks[index].Name == "config" {
			configCheck = &report.Checks[index]
		}
	}
	if configCheck == nil {
		t.Fatal("в отчёте нет проверки config")
	}
	if configCheck.Level != doctor.LevelOK {
		t.Errorf("проверка конфига: %+v", configCheck)
	}
}

// TestRunDoctorHelp проверяет справку doctor: в ней перечислены проверки и
// переменные окружения, которые переопределяют конфиг.
func TestRunDoctorHelp(t *testing.T) {
	code, stdout, _ := execute(t, "doctor", "--help")
	if code != 0 {
		t.Fatalf("код возврата = %d, ожидался 0", code)
	}
	for _, want := range []string{"SQLBRC_SERVER_LISTEN", "SQLBRC_STORAGE_DIR", "table", "json"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в справке doctor нет %q:\n%s", want, stdout)
		}
	}
}

// TestVersionString проверяет формат строки версии: её читают и человек, и скрипты.
func TestVersionString(t *testing.T) {
	text := versionString()
	for _, want := range []string{version, commit, date} {
		if !strings.Contains(text, want) {
			t.Errorf("в строке версии нет %q: %q", want, text)
		}
	}
	if !strings.Contains(text, "commit") {
		t.Errorf("в строке версии нет пояснения про коммит: %q", text)
	}
}

// TestCodedError проверяет код возврата и разворачивание ошибки: doctor возвращает
// код 1 при непройденных проверках, а причина остаётся доступна через errors.Is.
func TestCodedError(t *testing.T) {
	inner := errors.New("проверки не пройдены")
	coded := &codedError{code: 1, err: inner}
	if coded.Error() != inner.Error() {
		t.Errorf("Error() = %q, ожидалось %q", coded.Error(), inner.Error())
	}
	if !errors.Is(coded, inner) {
		t.Error("errors.Is не находит вложенную ошибку")
	}
	empty := &codedError{code: 2}
	if !strings.Contains(empty.Error(), "2") {
		t.Errorf("Error() без причины = %q", empty.Error())
	}
	if empty.Unwrap() != nil {
		t.Error("Unwrap() без причины вернула ошибку")
	}
}
