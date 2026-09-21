// Package main — точка входа CLI сервиса sqlbrc: бэкапы, восстановление и
// клонирование баз PostgreSQL 15 на одном сервере.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
)

// Переменные версии заполняются при сборке через -ldflags:
//
//	go build -ldflags "-X main.version=0.1.0 -X main.commit=abc1234 -X main.date=2026-09-21"
//
// Значения по умолчанию используются при сборке из исходников без флагов,
// поэтому `sqlbrc --version` работает всегда (см. цель version в Makefile).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionString возвращает строку для флага --version.
func versionString() string {
	return fmt.Sprintf("%s (commit %s, сборка %s)", version, commit, date)
}

// codedError позволяет вернуть конкретный код возврата: у `doctor` код 1
// означает «проверки не пройдены», а не сбой самой команды.
type codedError struct {
	code int
	err  error
}

// Error реализует error.
func (e *codedError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("код возврата %d", e.code)
	}
	return e.err.Error()
}

// Unwrap позволяет использовать errors.Is/errors.As для вложенной ошибки.
func (e *codedError) Unwrap() error { return e.err }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	// stop вызывается явно: os.Exit не выполняет отложенные вызовы.
	stop()
	os.Exit(code)
}

// Коды возврата CLI. Отказ операции (2) отделён от ошибки (1): скрипт отличает
// «сервис отказался по правилам» (чужая база, режим «только просмотр», мало
// места) от «операция не удалась».
const (
	exitOK       = 0
	exitError    = 1
	exitRejected = 2
)

// run выполняет команду и возвращает код возврата, чтобы main оставался тестируемым.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := newRootCommand(stdin, stdout, stderr)
	root.SetArgs(args)

	if err := root.ExecuteContext(ctx); err != nil {
		var coded *codedError
		switch {
		case errors.As(err, &coded):
			if coded.err != nil {
				_, _ = fmt.Fprintf(stderr, "ошибка: %v\n", coded.err)
			}
			return coded.code
		case backup.IsRejected(err):
			_, _ = fmt.Fprintf(stderr, "отказано: %v\n", err)
			return exitRejected
		default:
			_, _ = fmt.Fprintf(stderr, "ошибка: %v\n", err)
			return exitError
		}
	}
	return exitOK
}

// newRootCommand собирает дерево команд. Функция вынесена отдельно, чтобы
// проверки CLI работали с буферами вывода, а не с os.Stdout.
func newRootCommand(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	app := &appState{stdin: stdin, stdout: stdout, stderr: stderr}
	root := &cobra.Command{
		Use:   "sqlbrc",
		Short: "SQL backup, restore and clone: бэкапы, восстановление и клонирование баз PostgreSQL",
		Long: `sqlbrc — сервис для работы с базами PostgreSQL 15 на одном сервере:

  * backup — снять бэкап базы в каталог сервиса (pg_dump --format custom со сжатием);
  * restore — развернуть базу из бэкапа: отсутствующая создаётся, существующая
    очищается (перед этим снимается служебный бэкап с меткой pre-restore);
  * clone — создать стенд вида fse-1234 из бэкапа другой базы;
  * backups — список бэкапов и уборка по политике хранения (keep_last, keep_days);
  * jobs — журнал задач: что делали, когда и чем закончилось;
  * hash-password — bcrypt-хэш пароля для auth.users;
  * serve — веб-интерфейс и API для тех же операций (вход по auth.users,
    API — по токену из auth.token_file);
  * doctor — проверка готовности сервера: конфиг, утилиты PostgreSQL, правило
    sudoers, каталоги, свободное место и свежесть бэкапов.

Коды возврата: 0 — успех, 1 — ошибка, 2 — операция отвергнута до начала (чужая
или защищённая база, режим server.read_only, не хватает свободного места).

Утилиты PostgreSQL запускаются под пользователем postgres через sudo -n -u:
пароли в конфиге не хранятся, подключение идёт через локальный сокет (peer).`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		PersistentPreRunE: app.validateFlags,
	}
	root.PersistentFlags().StringVar(&app.configPath, "config", "",
		fmt.Sprintf("путь к конфигу (по умолчанию %s или $%s)", config.DefaultConfigPath, config.EnvConfigPath))
	root.PersistentFlags().StringVar(&app.logLevel, "log-level", "",
		"уровень журнала: debug, info, warn, error (переопределяет logging.level)")
	root.PersistentFlags().StringVar(&app.logFormat, "log-format", "",
		"формат журнала: text или json (переопределяет logging.format)")
	root.Version = versionString()
	root.SetVersionTemplate("sqlbrc {{.Version}}\n")
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(
		newVersionCommand(app),
		newDoctorCommand(app),
		newBackupCommand(app),
		newRestoreCommand(app),
		newCloneCommand(app),
		newBackupsCommand(app),
		newJobsCommand(app),
		newHashPasswordCommand(app),
		newServeCommand(app),
	)
	return root
}
