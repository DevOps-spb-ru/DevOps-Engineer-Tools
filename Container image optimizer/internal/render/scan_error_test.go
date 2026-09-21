package render

import (
	"fmt"
	"strings"
	"testing"
)

func TestErrorLinesDropsStackFrames(t *testing.T) {
	message := strings.Join([]string{
		"2026-09-21T10:05:57Z\tFATAL\tFatal error\trun error: image scan error",
		"  - unable to initialize artifact:",
		"github.com/aquasecurity/trivy/pkg/commands/artifact.createLocalService",
		"        /home/runner/work/trivy/trivy/pkg/commands/artifact/scanner.go:261",
		"\t* remote error: GET https://registry.example/v2/app/manifests/1.0: UNAUTHORIZED",
	}, "\n")

	lines := errorLines(message)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "UNAUTHORIZED") {
		t.Errorf("в строках ошибки потеряна причина: %v", lines)
	}
	for _, noise := range []string{".go:", "createLocalService", "trivy/pkg"} {
		if strings.Contains(joined, noise) {
			t.Errorf("в отчёт попал кадр трассировки %q: %v", noise, lines)
		}
	}
	wantFirst := "2026-09-21T10:05:57Z FATAL Fatal error run error: image scan error"
	if lines[0] != wantFirst {
		t.Errorf("первая строка = %q, ожидалось %q", lines[0], wantFirst)
	}
}

func TestErrorLinesElidesLongMessages(t *testing.T) {
	raw := make([]string, 0, 20)
	for i := 1; i <= 20; i++ {
		raw = append(raw, fmt.Sprintf("строка ошибки %d", i))
	}

	lines := errorLines(strings.Join(raw, "\n"))
	if len(lines) != maxErrorLines {
		t.Fatalf("строк = %d, ожидалось %d", len(lines), maxErrorLines)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "пропущено строк: 8") {
		t.Errorf("нет отметки о пропущенных строках: %v", lines)
	}
	if !strings.Contains(lines[len(lines)-1], "строка ошибки 20") {
		t.Errorf("потерян конец сообщения: %v", lines)
	}
}

func TestErrorLinesHandlesEmptyMessage(t *testing.T) {
	lines := errorLines("   \n\t\n")
	if len(lines) != 1 || lines[0] != "нет данных" {
		t.Errorf("errorLines пустого сообщения = %v", lines)
	}
}

func TestAbbreviateLine(t *testing.T) {
	long := strings.Repeat("a", 200) + "КОНЕЦ"
	got := abbreviateLine(long, 60)
	if size := len([]rune(got)); size > 60 {
		t.Errorf("длина %d больше лимита: %q", size, got)
	}
	if !strings.HasPrefix(got, "aaaa") || !strings.HasSuffix(got, "КОНЕЦ") {
		t.Errorf("сжатие затронуло не только середину: %q", got)
	}

	short := "короткая строка"
	if got := abbreviateLine(short, 60); got != short {
		t.Errorf("abbreviateLine(%q) = %q, ожидалось без изменений", short, got)
	}
}

func TestHintForScanError(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		imageRef string
		want     string
	}{
		{
			name: "образ не найден ни локально, ни в реестре",
			message: "unable to find the specified image \"app:5.4.1\" in [\"docker\" \"containerd\" \"podman\" \"remote\"]: " +
				"4 errors occurred:\n\t* remote error: UNAUTHORIZED",
			want: "--trivy-docker-socket",
		},
		{
			name:    "нет авторизации в реестре",
			message: "remote error: UNAUTHORIZED: access to the requested resource is not authorized",
			want:    "TRIVY_USERNAME и TRIVY_PASSWORD",
		},
		{
			name:    "креды недоступны в контейнере",
			message: `error getting credentials - err: exec: "docker-credential-desktop": executable file not found`,
			want:    "TRIVY_USERNAME и TRIVY_PASSWORD",
		},
		{
			name:    "нет доступа к сокету демона",
			message: "failed to connect to the docker API at unix:///var/run/docker.sock",
			want:    "--trivy-docker-socket",
		},
		{
			name:     "локальная копия образа неполная",
			message:  "failed to get the layer: file blobs/sha256/aad6289 not found in tar",
			imageRef: "app:5.4.1",
			want:     "docker rmi app:5.4.1 && docker pull app:5.4.1",
		},
		{
			name:    "сертификат реестра",
			message: "tls: failed to verify certificate: x509: certificate signed by unknown authority",
			want:    "--trivy-arg=--insecure",
		},
		{
			name:    "нет сети",
			message: "dial tcp: lookup ghcr.io: no such host",
			want:    "проверьте DNS",
		},
		{
			name:    "база уязвимостей",
			message: "failed to download vulnerability DB",
			want:    "базу уязвимостей",
		},
		{
			name:    "превышен лимит времени",
			message: "context deadline exceeded",
			want:    "--trivy-timeout",
		},
		{
			name:    "неизвестная причина",
			message: "что-то пошло не так",
			want:    "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hint := hintForScanError(test.message, test.imageRef)
			if test.want == "" {
				if hint != "" {
					t.Errorf("hintForScanError(%q) = %q, ожидалась пустая подсказка", test.message, hint)
				}
				return
			}
			if !strings.Contains(hint, test.want) {
				t.Errorf("hintForScanError(%q) = %q, ожидалось вхождение %q", test.message, hint, test.want)
			}
		})
	}
}

func TestIncompleteCopyHintPrintsRecoveryCommands(t *testing.T) {
	hint := hintForScanError("file blobs/sha256/69d562d8 not found in tar", "repo/app:5.4.1")
	for _, want := range []string{
		"docker rmi repo/app:5.4.1 && docker pull repo/app:5.4.1",
		"cio analyze --trivy-image-src remote repo/app:5.4.1",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("в подсказке нет фрагмента %q: %q", want, hint)
		}
	}
	if strings.Contains(hint, "перекачайте образ") {
		t.Errorf("вернулся неверный совет про docker pull: %q", hint)
	}

	// Без имени образа подставляется заглушка: подсказка остаётся понятной.
	placeholder := hintForScanError("unable to get uncompressed layer: file not found", "")
	if !strings.Contains(placeholder, "<образ>") {
		t.Errorf("нет заглушки имени образа: %q", placeholder)
	}
}
