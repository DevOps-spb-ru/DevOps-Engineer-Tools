package dockerclient

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
)

func TestHistoryLayersMapping(t *testing.T) {
	// Ответ Docker API преобразуется в слои отчёта без потери полей:
	// именно эти поля правил анализа используются дальше (CreatedBy, Size).
	result := client.ImageHistoryResult{Items: []image.HistoryResponseItem{
		{
			ID:        "sha256:aaa",
			Created:   1758445200,
			CreatedBy: "RUN apk add --no-cache curl",
			Comment:   "buildkit.dockerfile.v0",
			Size:      12345678,
		},
		{
			ID:        "sha256:bbb",
			Created:   1758445000,
			CreatedBy: "ENV TOKEN=supersecret",
			Comment:   "<missing>",
			Size:      0,
		},
	}}

	layers := historyLayers(result)
	if len(layers) != 2 {
		t.Fatalf("получено %d слоёв, ожидалось 2", len(layers))
	}
	first := layers[0]
	if first.ID != "sha256:aaa" || first.Created != 1758445200 ||
		first.CreatedBy != "RUN apk add --no-cache curl" ||
		first.Comment != "buildkit.dockerfile.v0" || first.Size != 12345678 {
		t.Errorf("слой разобран неверно: %+v", first)
	}
	if layers[1].Comment != "<missing>" {
		t.Errorf("комментарий слоя потерян: %+v", layers[1])
	}
}

func TestHistoryLayersEmptyResponse(t *testing.T) {
	// Пустая история — не nil: вызывающий код не должен проверять слайс на nil.
	layers := historyLayers(client.ImageHistoryResult{})
	if layers == nil {
		t.Fatal("historyLayers вернул nil для пустого ответа")
	}
	if len(layers) != 0 {
		t.Errorf("получено %d слоёв, ожидалось 0", len(layers))
	}
}

func TestImageMetaMapping(t *testing.T) {
	result := client.ImageInspectResult{InspectResponse: image.InspectResponse{
		ID:           "sha256:ccc",
		RepoTags:     []string{"alpine:3.20"},
		RepoDigests:  []string{"alpine@sha256:ddd"},
		Architecture: "amd64",
		Os:           "linux",
		Created:      "2026-09-21T10:00:00Z",
		Size:         7654321,
	}}

	meta := imageMeta(result)
	if meta.ID != "sha256:ccc" || meta.Architecture != "amd64" || meta.OS != "linux" {
		t.Errorf("метаданные образа разобраны неверно: %+v", meta)
	}
	if meta.Created != "2026-09-21T10:00:00Z" || meta.Size != 7654321 {
		t.Errorf("дата или размер образа потеряны: %+v", meta)
	}
	if len(meta.RepoTags) != 1 || meta.RepoTags[0] != "alpine:3.20" {
		t.Errorf("теги образа потеряны: %+v", meta.RepoTags)
	}
	if len(meta.RepoDigests) != 1 || meta.RepoDigests[0] != "alpine@sha256:ddd" {
		t.Errorf("дайджесты образа потеряны: %+v", meta.RepoDigests)
	}
}

// TestAPIErrorsMentionImage проверяет, что недоступный демон даёт ошибку
// с именем образа: иначе пользователь не понимает, какой образ не прочитан.
func TestAPIErrorsMentionImage(t *testing.T) {
	// Порт 1 закрыт: подключение к демону гарантированно не удастся.
	cli, err := client.New(client.WithHost("tcp://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	reader := newWithClient(cli)
	defer func() { _ = reader.Close() }()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "история образа",
			call: func() error {
				_, callErr := reader.ImageHistory(context.Background(), "alpine:3.20")
				return callErr
			},
		},
		{
			name: "метаданные образа",
			call: func() error {
				_, callErr := reader.ImageInspect(context.Background(), "alpine:3.20")
				return callErr
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			callErr := test.call()
			if callErr == nil {
				t.Fatal("ожидалась ошибка обращения к демону")
			}
			message := callErr.Error()
			if !strings.Contains(message, "alpine:3.20") {
				t.Errorf("в ошибке нет имени образа: %q", message)
			}
			if !strings.Contains(message, "docker-демон недоступен") &&
				!strings.Contains(message, "не удалось получить данные образа") {
				t.Errorf("ошибка без понятной причины: %q", message)
			}
		})
	}
}
