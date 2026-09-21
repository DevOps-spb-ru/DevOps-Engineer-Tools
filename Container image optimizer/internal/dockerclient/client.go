// Package dockerclient инкапсулирует работу с Docker Engine API.
package dockerclient

import (
	"context"
	"errors"
	"fmt"

	"github.com/moby/moby/client"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/container-image-optimizer/internal/analyze"
)

// ImageReader — минимальная часть Docker API, необходимая утилите.
// Интерфейс позволяет подменять реализацию в тестах без docker-демона.
type ImageReader interface {
	// ImageHistory возвращает историю сборки образа.
	ImageHistory(ctx context.Context, ref string) ([]analyze.HistoryLayer, error)
	// ImageInspect возвращает метаданные образа.
	ImageInspect(ctx context.Context, ref string) (analyze.ImageMeta, error)
	// Close освобождает ресурсы клиента.
	Close() error
}

// Client — реализация ImageReader поверх Docker Engine API.
type Client struct {
	cli *client.Client
}

// New подключается к docker-демону. Адрес берётся из окружения (DOCKER_HOST и другие
// переменные, которые читает client.FromEnv), иначе используется адрес по умолчанию.
func New() (*Client, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать docker-клиент: %w", err)
	}
	return &Client{cli: cli}, nil
}

// ImageHistory реализует ImageReader.
func (c *Client) ImageHistory(ctx context.Context, ref string) ([]analyze.HistoryLayer, error) {
	result, err := c.cli.ImageHistory(ctx, ref)
	if err != nil {
		return nil, describeError(ref, err)
	}
	layers := make([]analyze.HistoryLayer, 0, len(result.Items))
	for _, item := range result.Items {
		layers = append(layers, analyze.HistoryLayer{
			ID:        item.ID,
			Created:   item.Created,
			CreatedBy: item.CreatedBy,
			Comment:   item.Comment,
			Size:      item.Size,
		})
	}
	return layers, nil
}

// ImageInspect реализует ImageReader.
func (c *Client) ImageInspect(ctx context.Context, ref string) (analyze.ImageMeta, error) {
	result, err := c.cli.ImageInspect(ctx, ref)
	if err != nil {
		return analyze.ImageMeta{}, describeError(ref, err)
	}
	return analyze.ImageMeta{
		ID:           result.ID,
		RepoTags:     result.RepoTags,
		RepoDigests:  result.RepoDigests,
		Architecture: result.Architecture,
		OS:           result.Os,
		Created:      result.Created,
		Size:         result.Size,
	}, nil
}

// Close реализует ImageReader.
func (c *Client) Close() error {
	if c == nil || c.cli == nil {
		return nil
	}
	return c.cli.Close()
}

// describeError добавляет понятную подсказку, если демон недоступен.
func describeError(ref string, err error) error {
	if client.IsErrConnectionFailed(err) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf(
			"docker-демон недоступен (образ %q): проверьте, что Docker запущен, и переменную DOCKER_HOST: %w",
			ref, err)
	}
	return fmt.Errorf("не удалось получить данные образа %q: %w", ref, err)
}
