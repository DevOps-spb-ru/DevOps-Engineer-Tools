package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/passwd"
)

// operationsConfigDocument собирает конфиг для проверок команд: пути каталогов
// те же, что в примере установки. Проверки команд не создают файлов: операция
// отвергается до первого обращения к хранилищу, поэтому /var в тестах не трогается.
func operationsConfigDocument() string {
	return `
server:
  listen: "0.0.0.0:8088"
  allow_insecure: true
auth:
  users:
    - login: admin
      password_bcrypt: "$2y$10$abcdefghijklmnopqrstuu0123456789abcdefghijklmnopqrstu"
storage:
  dir: "/var/backups/sqlbrc"
  state_dir: "/var/lib/sqlbrc"
`
}

// TestRunOperationsRejectForeignDatabases проверяет код возврата 2: операция с
// чужой базой отвергается до её начала, и скрипт отличает отказ от сбоя.
func TestRunOperationsRejectForeignDatabases(t *testing.T) {
	configPath := writeConfig(t, operationsConfigDocument())
	cases := []struct {
		name string
		args []string
	}{
		{"бэкап", []string{"backup", "--db", "prod", "--config", configPath}},
		{"восстановление", []string{"restore", "--db", "prod", "--config", configPath}},
		{"клонирование", []string{"clone", "--from", "prod", "--to", "fssd-7", "--config", configPath}},
		{"уборка", []string{"backups", "prune", "--db", "prod", "--config", configPath}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			code, stdout, stderr := execute(t, testCase.args...)
			if code != exitRejected {
				t.Fatalf("код возврата = %d, ожидался %d (stdout: %s, stderr: %s)",
					code, exitRejected, stdout, stderr)
			}
			if !strings.Contains(stderr, "отказано") || !strings.Contains(stderr, "не входит в список обслуживаемых") {
				t.Errorf("в stderr нет причины отказа: %s", stderr)
			}
			if stdout != "" {
				t.Errorf("при отказе что-то напечатано в stdout: %s", stdout)
			}
		})
	}
}

// TestRunOperationsRequireNames проверяет подсказки вместо пустого запуска: без
// имени базы команда объясняет, чего не хватает, и возвращает код 1.
func TestRunOperationsRequireNames(t *testing.T) {
	configPath := writeConfig(t, operationsConfigDocument())
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"бэкап без базы", []string{"backup", "--config", configPath}, "не задана база"},
		{"восстановление без базы", []string{"restore", "--config", configPath}, "не задана база"},
		{"клонирование без источника", []string{"clone", "--to", "fssd-7", "--config", configPath}, "не задан источник"},
		{"клонирование без приёмника", []string{"clone", "--from", "fse-1234", "--config", configPath}, "не задан приёмник"},
		{"хэш без логина", []string{"hash-password"}, "не задан логин"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			code, _, stderr := execute(t, testCase.args...)
			if code != exitError {
				t.Fatalf("код возврата = %d, ожидался %d (stderr: %s)", code, exitError, stderr)
			}
			if !strings.Contains(stderr, testCase.want) {
				t.Errorf("в stderr нет %q: %s", testCase.want, stderr)
			}
		})
	}
}

// TestRunBackupsEmpty проверяет пустой каталог: список бэкапов и уборка сообщают,
// что работы нет, и не считают это ошибкой.
func TestRunBackupsEmpty(t *testing.T) {
	configPath := writeConfig(t, operationsConfigDocument())
	code, stdout, stderr := execute(t, "backups", "list", "--config", configPath)
	if code != exitOK {
		t.Fatalf("код возврата = %d, ожидался 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "бэкапов нет") {
		t.Errorf("вывод списка: %q", stdout)
	}

	code, stdout, stderr = execute(t, "backups", "prune", "--dry-run", "--config", configPath)
	if code != exitOK {
		t.Fatalf("код возврата = %d, ожидался 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "удалять нечего") {
		t.Errorf("вывод уборки: %q", stdout)
	}
}

// TestRunJobsEmpty проверяет журнал без записей: команда сообщает об этом в
// текстовом выводе и отдаёт пустой массив в машинном формате.
func TestRunJobsEmpty(t *testing.T) {
	configPath := writeConfig(t, operationsConfigDocument())
	code, stdout, stderr := execute(t, "jobs", "--config", configPath)
	if code != exitOK {
		t.Fatalf("код возврата = %d, ожидался 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "журнал задач пуст") {
		t.Errorf("вывод журнала: %q", stdout)
	}

	code, stdout, stderr = execute(t, "jobs", "--json", "--config", configPath)
	if code != exitOK {
		t.Fatalf("код возврата = %d, ожидался 0 (stderr: %s)", code, stderr)
	}
	stored := []jobs.Job{}
	if err := json.Unmarshal([]byte(stdout), &stored); err != nil {
		t.Fatalf("вывод --json не разобран: %v (%q)", err, stdout)
	}
	if len(stored) != 0 {
		t.Errorf("журнал в JSON содержит %d записей", len(stored))
	}
}

// TestRunHashPassword проверяет создание хэша: команда читает пароль из stdin и
// печатает готовый фрагмент конфига, по которому пароль проходит проверку.
func TestRunHashPassword(t *testing.T) {
	const password = "пароль-стенда-42"
	code, stdout, stderr := executeInput(t, password+"\n", "hash-password", "--login", "admin")
	if code != exitOK {
		t.Fatalf("код возврата = %d, ожидался 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "login: admin") {
		t.Errorf("в выводе нет логина: %q", stdout)
	}
	hash := hashFromOutput(t, stdout)
	if err := passwd.Verify(hash, password); err != nil {
		t.Errorf("пароль не проходит проверку по напечатанному хэшу: %v", err)
	}
	if strings.Contains(stdout, password) {
		t.Errorf("пароль попал в вывод команды: %q", stdout)
	}
}

// TestRunHashPasswordRejectsShortPassword проверяет отказ на пароль, который не
// подходит по длине: подсказка должна объяснять причину.
func TestRunHashPasswordRejectsShortPassword(t *testing.T) {
	code, _, stderr := executeInput(t, "123\n", "hash-password", "--login", "admin")
	if code != exitError {
		t.Fatalf("код возврата = %d, ожидался %d", code, exitError)
	}
	if !strings.Contains(stderr, fmt.Sprint(passwd.MinPasswordLength)) {
		t.Errorf("в stderr нет требования к длине пароля: %s", stderr)
	}
}

// hashFromOutput достаёт хэш из напечатанного фрагмента конфига: значение
// напечатано в кавычках, поэтому разбирается как строковый литерал Go.
func hashFromOutput(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "password_bcrypt:")
		if !ok {
			continue
		}
		unquoted, err := strconv.Unquote(strings.TrimSpace(value))
		if err != nil {
			t.Fatalf("значение password_bcrypt не разобрано: %v", err)
		}
		return unquoted
	}
	t.Fatalf("в выводе нет строки password_bcrypt: %q", output)
	return ""
}
