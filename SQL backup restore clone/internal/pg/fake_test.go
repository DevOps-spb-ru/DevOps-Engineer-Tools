package pg

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// fakeRunner — подмена CommandRunner: тесты не запускают sudo, psql и pg_dump,
// а проверяют сформированные argv и разбор вывода. Это требование правил проекта:
// тесты не должны зависеть от установленного PostgreSQL и от окружения CI.
type fakeRunner struct {
	// calls — вызовы в порядке обращения (команда целиком, как она попадает в журнал).
	calls []string
	// responses — stdout по имени утилиты.
	responses map[string]string
	// missing — утилиты, которых нет в PATH.
	missing map[string]bool
	// failures — утилиты, которые завершаются ошибкой; значение — текст stderr.
	failures map[string]string
}

// newFakeRunner создаёт подмену без подготовленных ответов.
func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		responses: make(map[string]string),
		missing:   make(map[string]bool),
		failures:  make(map[string]string),
	}
}

// LookPath отвечает, найден ли файл.
func (f *fakeRunner) LookPath(name string) (string, error) {
	tool := toolName(name, nil)
	if f.missing[tool] {
		return "", fmt.Errorf("exec: %q: executable file not found in %%PATH%%", name)
	}
	return name, nil
}

// Run записывает вызов и отдаёт подготовленный stdout.
func (f *fakeRunner) Run(_ context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	tool := f.record(name, args)
	if stdin != nil {
		// Чтение stdin завершает «процесс»: так проверяется, что архив открыт.
		_, _ = io.Copy(io.Discard, stdin)
	}
	if message, ok := f.failures[tool]; ok {
		if stderr != nil {
			_, _ = io.WriteString(stderr, message)
		}
		return fmt.Errorf("exit status 1")
	}
	if stdout != nil {
		_, _ = io.WriteString(stdout, f.responses[tool])
	}
	return nil
}

// Output записывает вызов и возвращает подготовленный stdout.
func (f *fakeRunner) Output(_ context.Context, name string, args []string) ([]byte, error) {
	tool := f.record(name, args)
	if message, ok := f.failures[tool]; ok {
		return nil, fmt.Errorf("exit status 1: %s", message)
	}
	return []byte(f.responses[tool]), nil
}

// record сохраняет вызов и возвращает имя утилиты.
func (f *fakeRunner) record(name string, args []string) string {
	f.calls = append(f.calls, PrintableCommand(name, args))
	return toolName(name, args)
}

// toolName определяет утилиту по аргументам: в режиме sudo имя процесса — sudo,
// а утилита идёт четвёртым аргументом после -n и -u <пользователь>.
func toolName(name string, args []string) string {
	if toolNameOf(name) == "sudo" {
		for _, arg := range args {
			if tool := toolNameOf(arg); ValidateTool(tool) == nil {
				return tool
			}
		}
	}
	return toolNameOf(name)
}

// toolNameOf возвращает последний элемент пути.
func toolNameOf(name string) string {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if index := strings.LastIndex(normalized, "/"); index >= 0 {
		return normalized[index+1:]
	}
	return normalized
}

// lastCall возвращает последний записанный вызов.
func (f *fakeRunner) lastCall() string {
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}
