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
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
)

// DefaultImage — образ Trivy для docker-fallback. Тег фиксируется: с :latest
// состав сканера меняется между запусками, а его уязвимости попадают в отчёты
// пользователя. Актуальную версию подсказывает Dependabot.
const DefaultImage = "aquasec/trivy:0.74.0"

// DefaultTimeout — предел времени на один запуск сканера.
const DefaultTimeout = 5 * time.Minute

// ErrNotFound возвращается, когда недоступны ни бинарь trivy, ни docker.
var ErrNotFound = errors.New("trivy не найден: нет бинаря в PATH и недоступен docker-fallback")

// baseArgs — общие аргументы запуска сканера: формат отчёта и набор сканеров.
var baseArgs = []string{"image", "--format", "json", "--quiet", "--scanners", "vuln,misconfig"}

// DefaultEnvNames — переменные окружения Trivy, которые пробрасываются в контейнер
// по имени. Значения не попадают в командную строку, поэтому креды реестра
// (TRIVY_USERNAME/TRIVY_PASSWORD) не видны в списке процессов.
var DefaultEnvNames = []string{"TRIVY_USERNAME", "TRIVY_PASSWORD", "TRIVY_INSECURE"}

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
	// ImageSource — значение флага --image-src (например "docker" или "remote").
	// Пустое значение оставляет порядок источников, заданный в Trivy.
	ImageSource string
	// DockerSocket — путь к сокету демона на хосте; непустое значение монтирует
	// сокет в контейнер, чтобы Trivy видел образы локального демона.
	DockerSocket string
	// CacheVolume — том с базой уязвимостей, чтобы не качать её при каждом запуске.
	// Пустое значение подставляет DefaultCacheVolume, DisableCacheVolume отключает кэш.
	CacheVolume string
	// ExtraArgs — дополнительные аргументы сканера (флаг --trivy-arg).
	ExtraArgs []string
	// EnvNames — имена переменных окружения, пробрасываемых в контейнер по имени.
	// Пустое значение подставляет DefaultEnvNames.
	EnvNames []string
	// LookupEnv подменяет os.LookupEnv в тестах.
	LookupEnv func(string) (string, bool)
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
	if opts.CacheVolume == "" {
		opts.CacheVolume = DefaultCacheVolume
	}
	if opts.EnvNames == nil {
		opts.EnvNames = DefaultEnvNames
	}
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
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
		return c.scanWith(ctx, bin, c.scanArgs(), imageRef, "trivy")
	}
	if !c.opts.UseDocker {
		return nil, ErrNotFound
	}
	if _, err := c.opts.LookPath(c.opts.DockerBin); err != nil {
		return nil, ErrNotFound
	}
	source := "docker:" + c.opts.FallbackImage
	return c.scanWith(ctx, c.opts.DockerBin, dockerRunArgs(c.opts, c.scanArgs()), imageRef, source)
}

// scanArgs собирает аргументы сканера: базовые флаги, источник образов и
// дополнительные аргументы пользователя.
func (c *CLI) scanArgs() []string {
	args := append([]string{}, baseArgs...)
	if c.opts.ImageSource != "" {
		args = append(args, "--image-src", c.opts.ImageSource)
	}
	return append(args, c.opts.ExtraArgs...)
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

// ScanError — сбой сканера вместе с контекстом: способ запуска и команда,
// которую пользователь может воспроизвести вручную.
type ScanError struct {
	// Source — способ запуска: "trivy" для бинаря или "docker:<образ>".
	Source string
	// Command — полная команда запуска сканера.
	Command string
	// Err — исходная причина сбоя.
	Err error
}

// Error возвращает причину сбоя в прежнем виде: способ запуска и текст ошибки.
func (e *ScanError) Error() string {
	return fmt.Sprintf("trivy (%s): %v", e.Source, e.Err)
}

// Unwrap даёт доступ к исходной ошибке для errors.Is/errors.As.
func (e *ScanError) Unwrap() error {
	return e.Err
}

func (c *CLI) scanWith(
	ctx context.Context, bin string, args []string, imageRef, source string,
) (*analyze.ScanSummary, error) {
	fullArgs := append(append([]string{}, args...), imageRef)
	command := strings.Join(append([]string{bin}, fullArgs...), " ")

	stdout, runErr := c.runner.Run(ctx, bin, fullArgs...)
	if runErr != nil {
		return nil, &ScanError{Source: source, Command: command, Err: runErr}
	}
	summary, err := Parse(stdout)
	if err != nil {
		return nil, &ScanError{Source: source, Command: command, Err: err}
	}
	summary.Source = source
	summary.Command = command
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

	// #nosec G204 -- команда собирается самим CLI: bin берётся из PATH или из флага
	// --trivy-bin, args — фиксированный список плюс аргументы пользователя (--trivy-arg).
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
