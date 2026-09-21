// Package pg запускает утилиты PostgreSQL (pg_dump, pg_restore, psql, pg_isready)
// и разбирает их вывод.
//
// Команды запускаются только через argv-массив: промежуточный шелл не используется,
// поэтому имя БД из веб-формы не может превратиться в команду. В режиме sudo утилиты
// выполняются от пользователя postgres через sudo -n -u (peer-аутентификация
// локального сокета), пароли при этом не нужны.
package pg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// stderrLimit — сколько символов stderr попадает в сообщение об ошибке:
// вывод pg_restore на большом дампе измеряется мегабайтами и печатать его целиком нельзя.
const stderrLimit = 2000

// CommandRunner выполняет внешнюю команду. Интерфейс позволяет проверять
// формирование argv и разбор вывода без установленного PostgreSQL.
type CommandRunner interface {
	Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error
	Output(ctx context.Context, name string, args []string) ([]byte, error)
	LookPath(name string) (string, error)
}

// ExecRunner — реализация CommandRunner поверх os/exec.
type ExecRunner struct {
	// Env — дополнительные переменные окружения (например PGPASSFILE).
	// Окружение процесса при этом сохраняется: sudo требует PATH и SUDO_*.
	Env []string
}

// Run запускает команду, подключая потоки ввода-вывода. Так передаётся дамп
// в pg_restore и принимается вывод pg_dump — без временных файлов и без шелла.
func (r ExecRunner) Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := r.command(ctx, name, args)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return commandError(name, args, nil, err)
	}
	return nil
}

// Output запускает команду и возвращает её stdout; при ошибке в неё попадает stderr.
func (r ExecRunner) Output(ctx context.Context, name string, args []string) ([]byte, error) {
	cmd := r.command(ctx, name, args)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), commandError(name, args, stderr.Bytes(), err)
	}
	return stdout.Bytes(), nil
}

// LookPath отвечает, доступен ли файл: принимает и имя из PATH, и полный путь.
func (r ExecRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

// command собирает *exec.Cmd: путь к бинарю берётся из конфига, аргументы — массивом.
func (r ExecRunner) command(ctx context.Context, name string, args []string) *exec.Cmd {
	// #nosec G204 -- имя файла приходит из конфига (/usr/bin/sudo или /usr/bin/pg_dump)
	// или из фиксированного списка утилит, аргументы передаются массивом, без шелла.
	cmd := exec.CommandContext(ctx, name, args...)
	if len(r.Env) > 0 {
		cmd.Env = append(os.Environ(), r.Env...)
	}
	return cmd
}

// commandError сохраняет исходную ошибку и добавляет к ней stderr команды,
// чтобы причина сбоя была видна в журнале задачи и в веб-интерфейсе.
func commandError(name string, args []string, stderr []byte, err error) error {
	command := PrintableCommand(name, args)
	message := strings.TrimSpace(truncate(string(stderr), stderrLimit))
	if message == "" {
		return fmt.Errorf("%s: %w", command, err)
	}
	return fmt.Errorf("%s: %w: %s", command, err, message)
}

// PrintableCommand возвращает команду в виде, пригодном для журнала и сообщений об ошибке.
func PrintableCommand(name string, args []string) string {
	return strings.Join(append([]string{name}, args...), " ")
}

// truncate ограничивает длину текста по символам: stderr размера мегабайта нельзя
// печатать целиком, а обрезка по байтам может разрезать многобайтовый символ
// и выдать нечитаемый мусор в сообщении об ошибке.
func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
