// Package trivy запускает внешний сканер Trivy и нормализует его JSON-отчёт.
//
// Поддерживаются два способа запуска: локальный бинарь trivy и, если бинарь не найден,
// контейнер aquasec/trivy через docker run. Это позволяет работать в средах,
// где Trivy установлен только как образ.
package trivy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
)

// DefaultImage — образ Trivy для docker-fallback.
const DefaultImage = "aquasec/trivy:latest"

// DefaultTimeout — предел времени на один запуск сканера.
const DefaultTimeout = 5 * time.Minute

// ErrNotFound возвращается, когда недоступны ни бинарь trivy, ни docker.
var ErrNotFound = errors.New("trivy не найден: нет бинаря в PATH и недоступен docker-fallback")

// scannerArgs — общие аргументы запуска сканера.
var scannerArgs = []string{"image", "--format", "json", "--quiet", "--scanners", "vuln,misconfig"}

// CommandRunner выполняет внешнюю команду и возвращает её stdout.
// Интерфейс позволяет проверять выбор способа запуска без установленных trivy и docker.
type CommandRunner interface {
	Run(ctx context.Context, bin string, args ...string) ([]byte, error)
}

// Options настраивает способ запуска Trivy.
type Options struct {
	// Bin — явный путь к бинарю trivy; пустое значение включает поиск в PATH.
	Bin string
	// FallbackImage — образ trivy для запуска через docker.
	FallbackImage string
	// UseDocker разрешает docker-fallback, когда бинарь не найден.
	UseDocker bool
	// DockerBin — исполняемый файл docker, по умолчанию "docker".
	DockerBin string
	// Timeout — предел времени на один запуск.
	Timeout time.Duration
	// LookPath подменяет exec.LookPath в тестах.
	LookPath func(string) (string, error)
	// Runner подменяет запуск внешних команд в тестах.
	Runner CommandRunner
}

// CLI — обёртка над внешним Trivy.
type CLI struct {
	opts   Options
	runner CommandRunner
}

// NewCLI создаёт обёртку, подставляя значения по умолчанию.
func NewCLI(opts Options) *CLI {
	if opts.FallbackImage == "" {
		opts.FallbackImage = DefaultImage
	}
	if opts.DockerBin == "" {
		opts.DockerBin = "docker"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}

	runner := opts.Runner
	if runner == nil {
		runner = execRunner{timeout: opts.Timeout}
	}
	return &CLI{opts: opts, runner: runner}
}

// Scan сканирует образ и возвращает нормализованную сводку.
func (c *CLI) Scan(ctx context.Context, imageRef string) (*analyze.ScanSummary, error) {
	if bin := c.resolveBinary(); bin != "" {
		return c.scanWith(ctx, bin, scannerArgs, imageRef, "trivy")
	}
	if !c.opts.UseDocker {
		return nil, ErrNotFound
	}
	if _, err := c.opts.LookPath(c.opts.DockerBin); err != nil {
		return nil, ErrNotFound
	}
	source := "docker:" + c.opts.FallbackImage
	args := append([]string{"run", "--rm", c.opts.FallbackImage}, scannerArgs...)
	return c.scanWith(ctx, c.opts.DockerBin, args, imageRef, source)
}

// resolveBinary возвращает путь к бинарю trivy или пустую строку, если его нет.
func (c *CLI) resolveBinary() string {
	if c.opts.Bin != "" {
		return c.opts.Bin
	}
	path, err := c.opts.LookPath("trivy")
	if err != nil {
		return ""
	}
	return path
}

func (c *CLI) scanWith(
	ctx context.Context, bin string, args []string, imageRef, source string,
) (*analyze.ScanSummary, error) {
	fullArgs := append(append([]string{}, args...), imageRef)
	stdout, runErr := c.runner.Run(ctx, bin, fullArgs...)
	if runErr != nil {
		return nil, fmt.Errorf("trivy (%s): %w", source, runErr)
	}
	summary, err := Parse(stdout)
	if err != nil {
		return nil, fmt.Errorf("trivy (%s): %w", source, err)
	}
	summary.Source = source
	return summary, nil
}

// execRunner — реализация CommandRunner поверх os/exec.
type execRunner struct {
	timeout time.Duration
}

// Run запускает команду и возвращает её stdout. Текст stderr попадает в ошибку,
// чтобы причина сбоя Trivy была видна пользователю.
func (r execRunner) Run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return stdout.Bytes(), nil
}
