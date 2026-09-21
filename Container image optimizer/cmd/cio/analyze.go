package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/dockerclient"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/render"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/trivy"
)

// Форматы отчёта.
const (
	formatTable = "table"
	formatJSON  = "json"
)

// analyzeFlags — значения флагов команды analyze.
type analyzeFlags struct {
	format          string
	output          string
	top             int
	minLayerSize    string
	hugeLayerSize   string
	noTrivy         bool
	trivyBin        string
	trivyImage      string
	trivyImageSrc   string
	trivySocket     bool
	trivyCache      string
	trivyArgs       []string
	trivyTimeout    time.Duration
	trivySocketPath string
	failOn          string
}

func newAnalyzeCommand() *cobra.Command {
	flags := &analyzeFlags{}
	cmd := &cobra.Command{
		Use:   "analyze <image>",
		Short: "Проанализировать образ и вывести отчёт",
		Long: `Команда получает историю слоёв и метаданные образа из Docker Engine API,
оценивает размеры слоёв, ищет типовые ошибки сборки и запускает Trivy.`,
		Example: `  cio analyze postgres:15-alpine
  cio analyze --format json --output report.json myapp:1.0
  cio analyze --no-trivy --fail-on high myapp:1.0`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAnalyze(cmd.Context(), cmd.OutOrStdout(), args[0], flags)
		},
	}

	cmd.Flags().StringVar(&flags.format, "format", formatTable, "формат отчёта: table или json")
	cmd.Flags().StringVarP(&flags.output, "output", "o", "", "записать отчёт в файл (по умолчанию — stdout)")
	cmd.Flags().IntVar(&flags.top, "top", analyze.DefaultTopLayers, "сколько самых больших слоёв показать")
	cmd.Flags().StringVar(&flags.minLayerSize, "min-layer-size", "10MB", "порог крупного слоя, например 10MB")
	cmd.Flags().StringVar(&flags.hugeLayerSize, "huge-layer-size", "100MB", "порог очень крупного слоя")
	cmd.Flags().BoolVar(&flags.noTrivy, "no-trivy", false, "не запускать сканирование Trivy")
	cmd.Flags().StringVar(&flags.trivyBin, "trivy-bin", "", "путь к бинарю trivy (по умолчанию — поиск в PATH)")
	cmd.Flags().StringVar(&flags.trivyImage, "trivy-image", trivy.DefaultImage,
		"образ trivy для запуска через docker, если бинарь не найден")
	cmd.Flags().StringVar(&flags.trivyImageSrc, "trivy-image-src", "",
		"источники образов для Trivy через запятую: docker,containerd,podman,remote "+
			"(по умолчанию — порядок внутри Trivy)")
	cmd.Flags().BoolVar(&flags.trivySocket, "trivy-docker-socket", false,
		"смонтировать сокет Docker в контейнер Trivy, чтобы сканировать локальные образы")
	cmd.Flags().StringVar(&flags.trivyCache, "trivy-cache", trivy.DefaultCacheVolume,
		"том с базой уязвимостей Trivy ("+trivy.DisableCacheVolume+" — не сохранять кэш между запусками)")
	cmd.Flags().StringArrayVar(&flags.trivyArgs, "trivy-arg", nil,
		"дополнительный аргумент Trivy, можно повторять (например --trivy-arg=--skip-db-update)")
	cmd.Flags().DurationVar(&flags.trivyTimeout, "trivy-timeout", trivy.DefaultTimeout,
		"предел времени на один запуск Trivy, например 10m")
	cmd.Flags().StringVar(&flags.failOn, "fail-on", "",
		"вернуть код 1, если есть замечания или уязвимости уровня info|low|medium|high|critical и выше")

	return cmd
}

func runAnalyze(ctx context.Context, out io.Writer, imageRef string, flags *analyzeFlags) error {
	minLayerSize, err := analyze.ParseSize(flags.minLayerSize)
	if err != nil {
		return fmt.Errorf("флаг --min-layer-size: %w", err)
	}
	hugeLayerSize, err := analyze.ParseSize(flags.hugeLayerSize)
	if err != nil {
		return fmt.Errorf("флаг --huge-layer-size: %w", err)
	}
	failOn, hasThreshold, err := analyze.ParseSeverity(flags.failOn)
	if err != nil {
		return fmt.Errorf("флаг --fail-on: %w", err)
	}
	if err := trivy.ValidateImageSrc(flags.trivyImageSrc); err != nil {
		return fmt.Errorf("флаг --trivy-image-src: %w", err)
	}
	if flags.trivyTimeout <= 0 {
		return fmt.Errorf("флаг --trivy-timeout: значение должно быть положительным, получено %s", flags.trivyTimeout)
	}
	if flags.trivySocket {
		socket, socketErr := trivySocketPath(os.Getenv("DOCKER_HOST"))
		if socketErr != nil {
			return fmt.Errorf("флаг --trivy-docker-socket: %w", socketErr)
		}
		flags.trivySocketPath = socket
	}
	if flags.format != formatTable && flags.format != formatJSON {
		return fmt.Errorf("флаг --format: неизвестный формат %q (ожидается %s или %s)",
			flags.format, formatTable, formatJSON)
	}

	report, err := buildReport(ctx, imageRef, flags, minLayerSize, hugeLayerSize)
	if err != nil {
		return err
	}

	writer := out
	if flags.output != "" {
		// Отчёт может содержать секреты из истории сборки и токены из находок,
		// поэтому файл создаётся с правами только для владельца (0600).
		file, createErr := openReportFile(flags.output)
		if createErr != nil {
			return fmt.Errorf("не удалось создать файл отчёта %q: %w", flags.output, createErr)
		}
		defer func() { _ = file.Close() }()
		writer = file
	}

	if err = writeReport(writer, report, flags.format); err != nil {
		return err
	}

	if hasThreshold && exceedsThreshold(report, failOn) {
		return &codedError{code: 1, err: fmt.Errorf("найдены замечания уровня %s и выше", failOn)}
	}
	return nil
}

// openReportFile создаёт файл отчёта с правами только для владельца: в отчёт
// попадают команды сборки образа и находки сканера, читать его посторонним незачем.
func openReportFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
}

// trivySocketPath возвращает путь к сокету демона для монтирования в контейнер Trivy.
// Для удалённого демона (DOCKER_HOST=tcp:// или ssh://) монтировать нечего.
func trivySocketPath(dockerHost string) (string, error) {
	socket := trivy.SocketPath(dockerHost, runtime.GOOS)
	if socket == "" {
		return "", fmt.Errorf("DOCKER_HOST=%q указывает на удалённый демон: сокет нельзя смонтировать, "+
			"используйте --trivy-image-src remote с кредами реестра", dockerHost)
	}
	return socket, nil
}

// buildReport собирает отчёт по данным Docker API и, если разрешено, Trivy.
func buildReport(
	ctx context.Context, imageRef string, flags *analyzeFlags, minLayerSize, hugeLayerSize int64,
) (*analyze.Report, error) {
	client, err := dockerclient.New()
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	history, err := client.ImageHistory(ctx, imageRef)
	if err != nil {
		return nil, err
	}
	meta, err := client.ImageInspect(ctx, imageRef)
	if err != nil {
		return nil, err
	}

	report := analyze.Build(imageRef, meta, history, analyze.Options{
		Top:           flags.top,
		MinLayerSize:  minLayerSize,
		HugeLayerSize: hugeLayerSize,
	})
	if flags.noTrivy {
		return report, nil
	}

	scanner := trivy.NewCLI(trivy.Options{
		Bin:           flags.trivyBin,
		FallbackImage: flags.trivyImage,
		UseDocker:     true,
		ImageSource:   flags.trivyImageSrc,
		DockerSocket:  flags.trivySocketPath,
		CacheVolume:   flags.trivyCache,
		ExtraArgs:     flags.trivyArgs,
		Timeout:       flags.trivyTimeout,
	})
	summary, scanErr := scanner.Scan(ctx, imageRef)
	if scanErr != nil {
		summary = failedScan(scanErr)
	}
	report.Scan = summary
	return report, nil
}

// failedScan описывает сбой сканера так, чтобы в отчёте были и причина,
// и подсказка, и команда для воспроизведения. Отсутствие Trivy не должно ломать
// анализ слоёв, поэтому ошибка попадает в отчёт, а не возвращается наружу.
func failedScan(scanErr error) *analyze.ScanSummary {
	summary := &analyze.ScanSummary{Source: "trivy", Error: scanErr.Error()}
	var scanError *trivy.ScanError
	if errors.As(scanErr, &scanError) {
		summary.Source = scanError.Source
		summary.Command = scanError.Command
	}
	return summary
}

func writeReport(writer io.Writer, report *analyze.Report, format string) error {
	if format == formatJSON {
		return render.JSON(writer, report)
	}
	return render.Table(writer, report)
}

// exceedsThreshold проверяет замечания по сборке и результаты сканирования.
func exceedsThreshold(report *analyze.Report, threshold analyze.Severity) bool {
	if highest, found := report.HighestFindingSeverity(); found && highest.AtLeast(threshold) {
		return true
	}
	if report.Scan == nil {
		return false
	}
	for name, count := range report.Scan.BySeverity {
		if count > 0 && analyze.Severity(name).AtLeast(threshold) {
			return true
		}
	}
	return false
}
