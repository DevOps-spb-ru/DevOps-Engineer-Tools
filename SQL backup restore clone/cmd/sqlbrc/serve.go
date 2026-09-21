package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/auth"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/web"
)

// shutdownTimeout — сколько сервис ждёт завершения активных запросов при
// остановке. Операции идут минутами, но запросы веб-интерфейса ждут только
// ответа: сама операция продолжается в задаче очереди.
const shutdownTimeout = 10 * time.Second

// readHeaderTimeout защищает от медленного чтения заголовков: без него одна
// «залипшая» сессия занимает рабочее соединение.
const readHeaderTimeout = 10 * time.Second

// newServeCommand собирает команду `serve`: веб-интерфейс и API поверх тех же
// операций, что и CLI. Свой сервер нужен потому, что CLI выполняет одну
// операцию и завершается, а интерфейс должен работать постоянно.
func newServeCommand(app *appState) *cobra.Command {
	var listen string
	command := &cobra.Command{
		Use:   "serve",
		Short: "Запустить веб-интерфейс и API",
		Long: `Веб-интерфейс и API для тех же операций, что и в CLI.

Вход в интерфейс — по логину и паролю из auth.users (bcrypt-хэш создаёт
команда hash-password), API — по токену из auth.token_file. Режим
server.read_only запрещает изменяющие операции и в интерфейсе, и в API.

Остановка — по SIGINT или SIGTERM (Ctrl+C): сервис завершает активные запросы
и выходит; операции, уже поставленные в очередь, продолжают выполняться, потому
что запись задачи в журнал не зависит от того, кто её запустил.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, path, err := config.Load(app.configPath)
			if err != nil {
				return err
			}
			if listen != "" {
				cfg.Server.Listen = listen
			}
			authenticator, err := buildAuth(cfg)
			if err != nil {
				return err
			}
			service, err := app.service()
			if err != nil {
				return err
			}
			server, err := web.New(web.Options{
				Config:     cfg,
				Operations: service,
				Auth:       authenticator,
				Log:        app.stderr,
				Version:    version,
			})
			if err != nil {
				return err
			}
			httpServer := &http.Server{
				Addr:              cfg.Server.Listen,
				Handler:           server.Handler(),
				ReadHeaderTimeout: readHeaderTimeout,
			}
			return serveHTTP(cmd.Context(), app, httpServer, path, cfg.Server.TLS)
		},
	}
	command.Flags().StringVar(&listen, "listen", "",
		"адрес веб-интерфейса, например 127.0.0.1:8088 (переопределяет server.listen)")
	return command
}

// serveHTTP запускает сервер и останавливает его по отмене контекста (SIGINT или
// SIGTERM). Ошибка запуска возвращается сразу: молчащий сервис, который «вроде
// бы поднялся», ищут дольше, чем читают сообщение о занятом порте. TLS
// включается, когда в конфиге заданы сертификат и ключ.
func serveHTTP(ctx context.Context, app *appState, server *http.Server, configPath string, tls config.TLSConfig) error {
	if warning := insecureListenWarning(server.Addr, tls.Enabled()); warning != "" {
		_, _ = fmt.Fprintf(app.stderr, "внимание: %s\n", warning)
	}
	failed := make(chan error, 1)
	go func() {
		if tls.Enabled() {
			failed <- server.ListenAndServeTLS(tls.CertFile, tls.KeyFile)
			return
		}
		failed <- server.ListenAndServe()
	}()
	_, _ = fmt.Fprintf(app.stderr, "sqlbrc слушает %s (конфиг %s, вход: auth.users)\n", server.Addr, configPath)

	select {
	case err := <-failed:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("веб-интерфейс остановлен: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("остановка веб-интерфейса: %w", err)
		}
		_, _ = fmt.Fprintln(app.stderr, "sqlbrc остановлен")
		return nil
	}
}

// insecureListenWarning отвечает, нужно ли предупредить администратора о
// небезопасной настройке: интерфейс слушает не loopback без TLS. Конфиг такую
// настройку пропускает только при server.allow_insecure, но напомнить о ней при
// запуске полезно: предупреждение попадает в журнал сервиса.
func insecureListenWarning(listen string, tlsEnabled bool) string {
	if tlsEnabled {
		return ""
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	if config.IsLoopbackHost(host) {
		return ""
	}
	return fmt.Sprintf(
		"интерфейс слушает %s: без TLS доступ ограничивает только сеть (nftables или reverse-proxy)",
		listen)
}

// buildAuth собирает проверку входа: пользователи берутся из конфига, токены —
// из auth.token_file. Если файл задан, а прочитать его нельзя, сервис не
// стартует: иначе API молча остался бы без доступа, и это выяснилось бы уже на
// первом запросе клиента.
func buildAuth(cfg config.Configuration) (*auth.Authenticator, error) {
	var tokens []auth.Token
	if path := strings.TrimSpace(cfg.Auth.TokenFile); path != "" {
		read, err := auth.ReadTokens(path)
		if err != nil {
			return nil, err
		}
		tokens = read
	}
	return auth.New(auth.Options{
		Users:            cfg.Auth.Users,
		Tokens:           tokens,
		SessionTTL:       cfg.Auth.SessionTTL.Duration(),
		LoginBackoff:     cfg.Auth.LoginBackoff.Duration(),
		MaxLoginAttempts: cfg.Auth.MaxLoginAttempts,
	})
}
