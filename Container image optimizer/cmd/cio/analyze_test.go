package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/trivy"
)

func TestOpenReportFileUsesOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	file, err := openReportFile(path)
	if err != nil {
		t.Fatalf("openReportFile: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("файл отчёта не создан: %v", err)
	}
	if runtime.GOOS == "windows" {
		// В Windows POSIX-права не действуют: проверяем только создание файла.
		return
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("права файла отчёта = %o, ожидались 600", got)
	}
}

func TestOpenReportFileTruncatesPreviousReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, []byte("старый отчёт с длинной строкой"), 0o600); err != nil {
		t.Fatalf("подготовка файла: %v", err)
	}

	file, err := openReportFile(path)
	if err != nil {
		t.Fatalf("openReportFile: %v", err)
	}
	if _, err := file.WriteString("новый"); err != nil {
		t.Fatalf("запись отчёта: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение отчёта: %v", err)
	}
	if string(content) != "новый" {
		t.Errorf("в файле остались данные прошлого отчёта: %q", content)
	}
}

func TestOpenReportFileRejectsMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "нет-такого-каталога", "report.json")
	if _, err := openReportFile(path); err == nil {
		t.Fatal("ожидалась ошибка создания файла в несуществующем каталоге")
	}
}

func TestTrivySocketPathRejectsRemoteDaemon(t *testing.T) {
	_, err := trivySocketPath("tcp://192.0.2.10:2375")
	if err == nil {
		t.Fatal("ожидалась ошибка: у удалённого демона нет сокета для монтирования")
	}
	for _, want := range []string{"DOCKER_HOST", "tcp://192.0.2.10:2375", "--trivy-image-src remote"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("сообщение %q не содержит %q", err.Error(), want)
		}
	}
}

func TestTrivySocketPathForLocalDaemon(t *testing.T) {
	path, err := trivySocketPath("")
	if err != nil {
		t.Fatalf("trivySocketPath: %v", err)
	}
	if !strings.Contains(path, "docker.sock") {
		t.Errorf("путь к сокету = %q, ожидался путь к docker.sock", path)
	}
}

func TestFailedScanKeepsScannerContext(t *testing.T) {
	scanErr := &trivy.ScanError{
		Source:  "docker:" + trivy.DefaultImage,
		Command: "docker run --rm " + trivy.DefaultImage + " image --format json alpine:3.20",
		Err:     errors.New("registry unreachable"),
	}

	summary := failedScan(scanErr)
	if summary.Source != scanErr.Source {
		t.Errorf("Source = %q, ожидалось %q", summary.Source, scanErr.Source)
	}
	if summary.Command != scanErr.Command {
		t.Errorf("Command = %q, ожидалось %q", summary.Command, scanErr.Command)
	}
	if !strings.Contains(summary.Error, "registry unreachable") {
		t.Errorf("причина сбоя потеряна: %q", summary.Error)
	}
}

func TestFailedScanWithoutScannerContext(t *testing.T) {
	summary := failedScan(errors.New("trivy не найден"))
	if summary.Source != "trivy" {
		t.Errorf("Source = %q, ожидалось %q", summary.Source, "trivy")
	}
	if summary.Command != "" {
		t.Errorf("Command = %q, ожидалась пустая строка", summary.Command)
	}
}

// sampleReport строит настоящий отчёт через analyze.Build: форматы вывода
// проверяются на тех же данных, что и в работе утилиты.
func sampleReport() *analyze.Report {
	return analyze.Build(
		"alpine:3.20",
		analyze.ImageMeta{ID: "sha256:ccc", Size: 2 * 1024 * 1024, Architecture: "amd64", OS: "linux"},
		[]analyze.HistoryLayer{
			{ID: "sha256:aaa", CreatedBy: "RUN apk add --no-cache curl", Size: 1024 * 1024},
			{ID: "sha256:bbb", CreatedBy: "ENV TOKEN=supersecret", Size: 1024 * 1024},
		},
		analyze.Options{Top: 2},
	)
}

func TestWriteReportJSON(t *testing.T) {
	var buffer bytes.Buffer
	if err := writeReport(&buffer, sampleReport(), formatJSON); err != nil {
		t.Fatalf("writeReport(json): %v", err)
	}
	if !json.Valid(buffer.Bytes()) {
		t.Errorf("отчёт в формате json не разбирается как JSON:\n%s", buffer.String())
	}
	if !strings.Contains(buffer.String(), "alpine:3.20") {
		t.Errorf("в JSON-отчёте нет образа:\n%s", buffer.String())
	}
}

func TestWriteReportTable(t *testing.T) {
	var buffer bytes.Buffer
	if err := writeReport(&buffer, sampleReport(), formatTable); err != nil {
		t.Fatalf("writeReport(table): %v", err)
	}
	if buffer.Len() == 0 {
		t.Fatal("табличный отчёт пуст")
	}
	if !strings.Contains(buffer.String(), "alpine:3.20") {
		t.Errorf("в табличном отчёте нет образа:\n%s", buffer.String())
	}
}

func TestWriteReportUnknownFormatFallsBackToTable(t *testing.T) {
	// Неизвестный формат до writeReport не доходит (его проверяет runAnalyze),
	// поэтому здесь важно, что функция не паникует и отдаёт таблицу.
	var buffer bytes.Buffer
	if err := writeReport(&buffer, sampleReport(), "yaml"); err != nil {
		t.Fatalf("writeReport(yaml): %v", err)
	}
	if !strings.Contains(buffer.String(), "alpine:3.20") {
		t.Errorf("отчёт не построен:\n%s", buffer.String())
	}
}

func TestAnalyzeFlagDefaults(t *testing.T) {
	flags := newAnalyzeCommand().Flags()

	stringFlags := map[string]string{
		"format":          formatTable,
		"trivy-image":     trivy.DefaultImage,
		"trivy-cache":     trivy.DefaultCacheVolume,
		"min-layer-size":  "10MB",
		"huge-layer-size": "100MB",
		"fail-on":         "",
	}
	for name, want := range stringFlags {
		got, err := flags.GetString(name)
		if err != nil {
			t.Fatalf("флаг --%s: %v", name, err)
		}
		if got != want {
			t.Errorf("значение --%s = %q, ожидалось %q", name, got, want)
		}
	}

	if got, err := flags.GetDuration("trivy-timeout"); err != nil || got != trivy.DefaultTimeout {
		t.Errorf("--trivy-timeout = %s (%v), ожидалось %s", got, err, trivy.DefaultTimeout)
	}
	if got, err := flags.GetInt("top"); err != nil || got != analyze.DefaultTopLayers {
		t.Errorf("--top = %d (%v), ожидалось %d", got, err, analyze.DefaultTopLayers)
	}
}
