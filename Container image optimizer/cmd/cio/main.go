// Package main — точка входа CLI Container image optimizer (cio):
// анализ размера слоёв, типовых ошибок сборки и уязвимостей Docker-образов.
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
)

// codedError позволяет вернуть конкретный код возврата.
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
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	// stop вызывается явно: os.Exit не выполняет отложенные вызовы.
	stop()
	os.Exit(code)
}

// run выполняет команду и возвращает код возврата, чтобы main оставался тестируемым.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := newRootCommand(stdout, stderr)
	root.SetArgs(args)

	if err := root.ExecuteContext(ctx); err != nil {
		var coded *codedError
		if errors.As(err, &coded) {
			if coded.err != nil {
				_, _ = fmt.Fprintf(stderr, "ошибка: %v\n", coded.err)
			}
			return coded.code
		}
		_, _ = fmt.Fprintf(stderr, "ошибка: %v\n", err)
		return 1
	}
	return 0
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "cio",
		Short: "Container image optimizer: анализ размера слоёв и уязвимостей Docker-образа",
		Long: `Container image optimizer — cio анализирует Docker-образ через Docker Engine API:

  * показывает самые крупные слои и их долю в размере образа;
  * находит типовые ошибки сборки (кэши пакетных менеджеров, секреты, сборка в финальном образе);
  * запускает Trivy (локальный бинарь или контейнер aquasec/trivy) для поиска уязвимостей.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(newAnalyzeCommand())
	return root
}
