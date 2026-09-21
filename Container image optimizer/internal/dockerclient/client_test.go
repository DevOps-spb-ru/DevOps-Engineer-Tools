package dockerclient

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Проверка на этапе компиляции: клиент реализует интерфейс, которым пользуется CLI.
var _ ImageReader = (*Client)(nil)

func TestDescribeError(t *testing.T) {
	tests := []struct {
		name        string
		ref         string
		err         error
		wantSubstrs []string
	}{
		{
			name:        "недоступный демон",
			ref:         "alpine:3.20",
			err:         context.DeadlineExceeded,
			wantSubstrs: []string{"docker-демон недоступен", "alpine:3.20", "DOCKER_HOST"},
		},
		{
			name:        "прочая ошибка API",
			ref:         "myapp:1.0",
			err:         errors.New("manifest unknown"),
			wantSubstrs: []string{"не удалось получить данные образа", "myapp:1.0", "manifest unknown"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := describeError(test.ref, test.err).Error()
			for _, want := range test.wantSubstrs {
				if !strings.Contains(message, want) {
					t.Errorf("сообщение %q не содержит %q", message, want)
				}
			}
		})
	}
}

func TestClientCloseWithoutConnection(t *testing.T) {
	// Close на пустом клиенте не должен паниковать: так безопасно вызывать его в defer.
	var client *Client
	if err := client.Close(); err != nil {
		t.Errorf("Close() вернул ошибку: %v", err)
	}
}

func TestNewReturnsUsableClient(t *testing.T) {
	// New не подключается к демону, поэтому клиент создаётся даже без Docker;
	// проверяем, что он корректно закрывается.
	client, err := New()
	if err != nil {
		t.Fatalf("New: неожиданная ошибка: %v", err)
	}

	if err := client.Close(); err != nil {
		t.Errorf("Close() вернул ошибку: %v", err)
	}
}
