// Package web — веб-интерфейс и API сервиса sqlbrc.
//
// Интерфейс не работает с PostgreSQL и файлами напрямую: он обращается к
// операциям сервиса (internal/backup), поэтому все проверки — «своя» ли база,
// `server.read_only`, свободное место, контрольная сумма архива — выполняются
// в одном месте, а страница показывает готовый результат. Так же устроен API:
// это те же операции, только ответ в JSON.
//
// Аутентификация разделена по способу входа (см. internal/auth): браузер
// получает cookie-сессию и CSRF-токен, клиент API — токен из auth.token_file.
// Изменяющие запросы из браузера проверяют CSRF-токен: иначе сторонний сайт
// мог бы отправить форму с восстановлением базы от имени вошедшего человека.
package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/auth"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// Operations — операции сервиса, доступные интерфейсу. Интерфейс (а не
// *backup.Service) нужен тестам: страницы проверяются без PostgreSQL, без
// каталога бэкапов и без очереди задач.
type Operations interface {
	Backup(ctx context.Context, opts backup.BackupOptions) (backup.Result, error)
	Restore(ctx context.Context, opts backup.RestoreOptions) (backup.Result, error)
	Clone(ctx context.Context, opts backup.CloneOptions) (backup.Result, error)
	Prune(ctx context.Context, opts backup.PruneOptions) ([]backup.PruneResult, error)
	Backups(database string) ([]store.Backup, error)
	Databases() ([]string, error)
	Jobs(limit int) ([]jobs.Job, error)
}

// defaultJobsLimit — сколько последних задач показывает журнал, если клиент не
// попросил иначе: список читается целиком из файла, поэтому предел нужен.
const defaultJobsLimit = 50

// maxJobsLimit — предел выборки журнала: запрос «дайте всё» на большом журнале
// занял бы всю память процесса.
const maxJobsLimit = 500

// Options — зависимости веб-сервера.
type Options struct {
	// Config — конфигурация сервиса: адрес, режим «только просмотр», пределы
	// размера запроса и время жизни сессии.
	Config config.Configuration
	// Operations — операции сервиса.
	Operations Operations
	// Auth — проверка паролей, сессий и токенов.
	Auth *auth.Authenticator
	// Log — журнал сервиса: строки о запросах и операциях. Если не задан,
	// сообщения уходят в никуда — так работает `doctor`, где сервис не поднят.
	Log io.Writer
	// Version — версия сборки: показывается в подвале страниц.
	Version string
	// Now подменяет текущее время в тестах.
	Now func() time.Time
}

// Server — обработчики веб-интерфейса и API.
type Server struct {
	cfg        config.Configuration
	operations Operations
	auth       *auth.Authenticator
	log        io.Writer
	version    string
	now        func() time.Time

	templates *templates
	handler   http.Handler
}

// New собирает сервер и его маршруты. Ошибка означает, что интерфейс не сможет
// работать: не заданы операции сервиса, проверка входа или шаблоны страниц
// не разобраны.
func New(opts Options) (*Server, error) {
	if opts.Operations == nil {
		return nil, errors.New("не заданы операции сервиса")
	}
	if opts.Auth == nil {
		return nil, errors.New("не задана проверка входа (auth)")
	}
	if opts.Config.Server.Listen == "" {
		return nil, errors.New("не задан адрес веб-интерфейса (server.listen)")
	}
	parsed, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	server := &Server{
		cfg:        opts.Config,
		operations: opts.Operations,
		auth:       opts.Auth,
		log:        opts.Log,
		version:    opts.Version,
		now:        now,
		templates:  parsed,
	}
	server.handler = server.routes()
	return server, nil
}

// Handler возвращает обработчик для http.Server: тесты поднимают его через
// httptest, а `sqlbrc serve` — на реальном адресе.
func (s *Server) Handler() http.Handler { return s.handler }

// ReadOnly отвечает, запрещены ли изменяющие операции конфигом
// (server.read_only). Проверка нужна интерфейсу, чтобы отдать понятный отказ
// раньше, чем операция дойдёт до сервиса.
func (s *Server) ReadOnly() bool { return s.cfg.Server.ReadOnly }

// routes собирает маршруты интерфейса и API.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Страницы веб-интерфейса: доступ по cookie-сессии.
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /backups", s.handleBackups)
	mux.HandleFunc("GET /jobs", s.handleJobs)
	mux.HandleFunc("POST /operations/backup", s.handleBackup)
	mux.HandleFunc("POST /operations/restore", s.handleRestore)
	mux.HandleFunc("POST /operations/clone", s.handleClone)
	mux.HandleFunc("POST /operations/prune", s.handlePrune)

	// API: доступ по токену из auth.token_file.
	mux.HandleFunc("GET /api/v1/backups", s.handleAPIListBackups)
	mux.HandleFunc("GET /api/v1/jobs", s.handleAPIListJobs)
	mux.HandleFunc("POST /api/v1/backup", s.handleAPIBackup)
	mux.HandleFunc("POST /api/v1/restore", s.handleAPIRestore)
	mux.HandleFunc("POST /api/v1/clone", s.handleAPIClone)
	mux.HandleFunc("POST /api/v1/prune", s.handleAPIPrune)

	// Проверка живости нужна systemd и мониторингу: она не раскрывает данные
	// сервиса, поэтому аутентификации не требует.
	mux.HandleFunc("GET /healthz", s.handleHealth)

	return s.withRecovery(s.withHeaders(s.withBodyLimit(mux)))
}

// withBodyLimit ограничивает размер тела запроса: формы сервиса больших тел не
// передают, а «залитый» запрос иначе занял бы память процесса. Превышение
// предела обработчик видит как ошибку чтения тела.
func (s *Server) withBodyLimit(next http.Handler) http.Handler {
	limit := s.cfg.Server.MaxBodyBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// withHeaders добавляет защитные заголовки ответа. Страницы интерфейса не
// подгружают ничего со стороны: стили описаны в самих шаблонах, скриптов нет,
// поэтому политика содержимого закрыта максимально.
func (s *Server) withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Content-Security-Policy",
			"default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// withRecovery не даёт панике в обработчике уронить весь сервис: ошибка
// попадает в журнал, клиент получает понятный ответ, а остальные запросы
// продолжают обслуживаться.
func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logf("паника при обработке %s %s: %v", r.Method, r.URL.Path, recovered)
				http.Error(w, "внутренняя ошибка сервиса", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// sessionFrom возвращает сессию из cookie запроса.
func (s *Server) sessionFrom(r *http.Request) (auth.Session, bool) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		return auth.Session{}, false
	}
	session, err := s.auth.Session(cookie.Value)
	if err != nil {
		return auth.Session{}, false
	}
	return session, true
}

// requireSession пропускает запрос к странице с живой сессией, а без неё
// отправляет на форму входа.
func (s *Server) requireSession(next func(http.ResponseWriter, *http.Request, auth.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := s.sessionFrom(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, session)
	}
}

// tokenIdentity читает токен API из заголовка «Authorization: Bearer …».
func (s *Server) tokenIdentity(r *http.Request) (auth.Identity, error) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return auth.Identity{}, auth.ErrTokenInvalid
	}
	return s.auth.Token(strings.TrimSpace(header[len(prefix):]))
}

// requireToken пропускает запрос к API с верным токеном.
func (s *Server) requireToken(next func(http.ResponseWriter, *http.Request, auth.Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, err := s.tokenIdentity(r)
		if err != nil {
			s.writeAPIError(w, http.StatusUnauthorized, errorUnauthorized,
				"нужен токен API: заголовок «Authorization: Bearer <токен>»")
			return
		}
		next(w, r, identity)
	}
}

// ensureWritable отвечает, разрешены ли изменяющие операции. Проверка стоит до
// вызова операции: так отказ виден сразу, а сообщение совпадает с тем, что
// вернул бы сервис.
func (s *Server) ensureWritable() error {
	if s.cfg.Server.ReadOnly {
		return backup.Rejected("сервис в режиме «только просмотр» (server.read_only): операции изменения запрещены")
	}
	return nil
}

// logf пишет строку в журнал сервиса: время в UTC, затем сообщение. Формат
// совпадает с остальными строками сервиса, поэтому журнал читается одним
// взглядом, а `--log-format json` подставляется на уровне записи в поток.
func (s *Server) logf(format string, args ...any) {
	if s.log == nil {
		return
	}
	_, _ = fmt.Fprintf(s.log, "%s %s\n", s.now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// handleHealth отвечает на проверку живости сервиса. Данных она не раскрывает,
// поэтому аутентификации не требует: иначе мониторингу пришлось бы выдавать
// токен.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

// jobsLimit приводит запрошенный предел выборки журнала к допустимому.
func jobsLimit(requested int) int {
	switch {
	case requested <= 0:
		return defaultJobsLimit
	case requested > maxJobsLimit:
		return maxJobsLimit
	default:
		return requested
	}
}
