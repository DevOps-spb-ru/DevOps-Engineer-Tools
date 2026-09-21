package pg

import (
	"context"
	"fmt"
	"time"
)

// Значения по умолчанию для ожидания готовности сервера. После DROP и CREATE
// базы сервер отвечает не мгновенно, а первое подключение pg_restore к ещё
// неготовому кластеру падает с невнятной ошибкой подключения.
const (
	// DefaultReadyTimeout — сколько ждать ответа сервера.
	DefaultReadyTimeout = 30 * time.Second
	// DefaultReadyInterval — пауза между проверками готовности.
	DefaultReadyInterval = 500 * time.Millisecond
)

// ReadyOptions — параметры ожидания готовности сервера.
type ReadyOptions struct {
	// Timeout — предел ожидания (по умолчанию DefaultReadyTimeout).
	Timeout time.Duration
	// Interval — пауза между проверками (по умолчанию DefaultReadyInterval).
	Interval time.Duration
}

// WaitForReady ждёт, пока сервер начнёт отвечать на pg_isready. Проверка идёт
// через pg_isready, а не через запрос к каталогу: она не требует пароля и
// работает, даже когда роль ещё не может войти.
func (c *Client) WaitForReady(ctx context.Context, opts ReadyOptions) error {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultReadyTimeout
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultReadyInterval
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		if err := c.Ping(waitCtx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
			// Причину последней неудачной попытки показываем целиком: по ней
			// видно, ждать ли дальше или сервер вообще не поднят.
			return fmt.Errorf("сервер PostgreSQL не ответил за %s: %w", timeout, lastErr)
		case <-time.After(interval):
		}
	}
}
