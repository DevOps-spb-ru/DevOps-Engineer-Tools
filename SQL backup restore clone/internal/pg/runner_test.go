package pg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestExecRunnerEnv проверяет, что дополнительные переменные окружения доходят
// до процесса, а stdout команды попадает в подключённый поток. Так работает
// передача PGPASSFILE в режиме tcp и вывод pg_dump в файл архива.
//
// Дочерний процесс — этот же тестовый бинарник: он печатает значение переменной.
func TestExecRunnerEnv(t *testing.T) {
	const (
		helperEnv = "SQLBRC_TEST_HELPER"
		valueEnv  = "SQLBRC_TEST_ENV"
		value     = "значение-от-сервиса"
	)
	if os.Getenv(helperEnv) == "1" {
		_, _ = fmt.Fprint(os.Stdout, os.Getenv(valueEnv))
		return
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("не удалось определить путь к тестовому бинарнику: %v", err)
	}
	// Переменная-переключатель передаётся дочернему процессу вместе с окружением.
	t.Setenv(helperEnv, "1")

	var stdout bytes.Buffer
	runner := ExecRunner{Env: []string{valueEnv + "=" + value}}
	args := []string{"-test.run=TestExecRunnerEnv"}
	if err := runner.Run(context.Background(), executable, args, nil, &stdout, io.Discard); err != nil {
		t.Fatalf("запуск дочернего процесса: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, value) {
		t.Errorf("дополнительная переменная окружения не дошла до процесса: получено %q", got)
	}
}

// TestExecRunnerRunFailure проверяет, что ошибка запуска содержит команду и не
// теряет исходную причину: по этому тексту видно, что именно не нашлось.
func TestExecRunnerRunFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "нет-такого-бинаря")
	err := (ExecRunner{}).Run(context.Background(), missing, []string{"--version"}, nil, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("ожидалась ошибка запуска несуществующего бинаря")
	}
	if !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "--version") {
		t.Errorf("ошибка не описывает запущенную команду: %v", err)
	}
}

// TestExecRunnerOutput проверяет возврат stdout и поиск файла по полному пути:
// в Debian так проверяется наличие /usr/bin/pg_dump (ссылка на pg_wrapper).
func TestExecRunnerOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("не удалось определить путь к тестовому бинарнику: %v", err)
	}
	runner := ExecRunner{}
	if _, err := runner.LookPath(executable); err != nil {
		t.Errorf("LookPath(%q): %v", executable, err)
	}
	if _, err := runner.LookPath(filepath.Join(t.TempDir(), "нет-такого")); err == nil {
		t.Error("LookPath несуществующего файла вернула nil")
	}

	// -test.run с несуществующим тестом завершается успехом и печатает «no tests to run».
	out, err := runner.Output(context.Background(), executable, []string{"-test.run=TestНичего"})
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(out) == 0 {
		t.Error("ожидался вывод команды, получена пустая строка")
	}

	missing := filepath.Join(t.TempDir(), "нет-такого-бинаря")
	if _, err := runner.Output(context.Background(), missing, nil); err == nil {
		t.Error("ожидалась ошибка запуска несуществующего бинаря")
	}
}

func TestPrintableCommand(t *testing.T) {
	got := PrintableCommand(DefaultSudoPath, []string{"-n", "-u", "postgres", "/usr/bin/pg_dump", "--version"})
	want := "/usr/bin/sudo -n -u postgres /usr/bin/pg_dump --version"
	if got != want {
		t.Errorf("PrintableCommand = %q, ожидалось %q", got, want)
	}
	if got := PrintableCommand("psql", nil); got != "psql" {
		t.Errorf("PrintableCommand без аргументов = %q", got)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		limit int
		want  string
	}{
		{name: "короче предела", text: "кратко", limit: 10, want: "кратко"},
		{name: "равно пределу", text: "12345", limit: 5, want: "12345"},
		{name: "длиннее предела", text: "1234567890", limit: 5, want: "12345…"},
		{name: "обрезка по символам, не по байтам", text: "абвгде", limit: 3, want: "абв…"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := truncate(test.text, test.limit)
			if got != test.want {
				t.Errorf("truncate(%q, %d) = %q, ожидалось %q", test.text, test.limit, got, test.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("truncate(%q, %d) вернула некорректный UTF-8: %q", test.text, test.limit, got)
			}
		})
	}
}

// TestCommandError проверяет, что текст stderr попадает в ошибку: по нему видно
// причину сбоя pg_dump или pg_restore, а stdout большого дампа не печатается.
func TestCommandError(t *testing.T) {
	base := fmt.Errorf("exit status 1")
	stderr := []byte("  pg_restore: error: could not connect  \n")
	err := commandError("pg_restore", []string{"--dbname", "fse-1"}, stderr, base)
	for _, want := range []string{"pg_restore", "fse-1", "could not connect", "exit status 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в сообщении об ошибке нет %q: %s", want, err)
		}
	}
	if err := commandError("psql", nil, nil, base); !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("пустой stderr: исходная ошибка потеряна: %v", err)
	}

	long := strings.Repeat("о", stderrLimit*2)
	truncated := commandError("pg_restore", nil, []byte(long), base).Error()
	if utf8.RuneCountInString(truncated) > stderrLimit+50 {
		t.Errorf("сообщение об ошибке не обрезано: %d символов", utf8.RuneCountInString(truncated))
	}
}
