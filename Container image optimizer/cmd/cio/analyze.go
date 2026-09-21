package main

import (
	"context"
	"fmt"
	"io"
	"os"

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
	format        string
	output        string
	top           int
	minLayerSize  string
	hugeLayerSize string
	noTrivy       bool
	trivyBin      string
	trivyImage    string
	failOn        string
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
		file, createErr := os.Create(flags.output)
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
	})
	summary, scanErr := scanner.Scan(ctx, imageRef)
	if scanErr != nil {
		// Отсутствие Trivy не должно ломать анализ размера слоёв: пишем причину в отчёт.
		summary = &analyze.ScanSummary{Source: "trivy", Error: scanErr.Error()}
	}
	report.Scan = summary
	return report, nil
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
