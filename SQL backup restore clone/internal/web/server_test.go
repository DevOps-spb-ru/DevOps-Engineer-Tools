package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/auth"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/passwd"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// testPassword — пароль тестового пользователя; хэш считается один раз, потому
// что bcrypt намеренно медленный.
const testPassword = "s3cret-password"

var testHash = sync.OnceValue(func() string {
	hash, err := passwd.Hash(testPassword)
	if err != nil {
		panic(err)
	}
	return hash
})

// testToken — токен API тестового пользователя.
var testToken = strings.Repeat("t", auth.MinTokenLength)

// newTestServer собирает интерфейс с подменёнными операциями. tune правит
// конфиг до сборки: так проверяются режим «только просмотр», доверенные прокси
// и пределы размера запроса.
func newTestServer(t *testing.T, tune func(*config.Configuration)) (*Server, *fakeOperations) {
	t.Helper()
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:8088"
	cfg.Server.AllowInsecure = true
	cfg.Auth.Users = []config.User{{Login: "ivan", PasswordBcrypt: testHash()}}
	if tune != nil {
		tune(&cfg)
	}
	authenticator, err := auth.New(auth.Options{
		Users:            cfg.Auth.Users,
		Tokens:           []auth.Token{{Login: "ci", Value: testToken}},
		SessionTTL:       cfg.Auth.SessionTTL.Duration(),
		LoginBackoff:     cfg.Auth.LoginBackoff.Duration(),
		MaxLoginAttempts: cfg.Auth.MaxLoginAttempts,
	})
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	operations := newFakeOperations()
	operations.databases = []string{"fse-1234"}
	operations.backups["fse-1234"] = []store.Backup{{
		ID:        "fse-1234/20260921T020000Z",
		DB:        "fse-1234",
		Name:      "20260921T020000Z",
		CreatedAt: time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC),
		SizeBytes: 128_400_000,
		Status:    store.StatusComplete,
	}}
	operations.jobs = []jobs.Job{{
		ID:        "20260921T020000Z-9f8e7d6c",
		Kind:      jobs.KindBackup,
		Database:  "fse-1234",
		Status:    jobs.StatusDone,
		StartedAt: time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC),
		Operator:  "ivan (web)",
		Message:   "бэкап создан",
	}}
	server, err := New(Options{
		Config:     cfg,
		Operations: operations,
		Auth:       authenticator,
		Log:        io.Discard,
		Version:    "test",
	})
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	return server, operations
}

// login выполняет вход и возвращает cookie сессии.
func login(t *testing.T, server *Server) *http.Cookie {
	t.Helper()
	form := url.Values{"login": {"ivan"}, "password": {testPassword}}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/login",
		strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("вход: код %d, ожидался 303 (%s)", recorder.Code, recorder.Body.String())
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == auth.SessionCookieName {
			return cookie
		}
	}
	t.Fatal("cookie сессии не выдана")
	return nil
}

// csrfToken достаёт токен формы со страницы: без него изменяющие запросы
// отклоняются.
func csrfToken(t *testing.T, server *Server, cookie *http.Cookie, path string) string {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("страница %s: код %d", path, recorder.Code)
	}
	const marker = `name="csrf" value="`
	body := recorder.Body.String()
	index := strings.Index(body, marker)
	if index < 0 {
		t.Fatalf("на странице %s нет токена формы", path)
	}
	rest := body[index+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

// postForm отправляет форму с cookie сессии и токеном.
func postForm(server *Server, path string, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path,
		strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

// get отправляет GET с cookie сессии.
func get(server *Server, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}
