package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
)

func TestSecurityHeadersAndHealth(t *testing.T) {
	server, _ := newTestServer(t, nil)
	recorder := get(server, "/healthz", nil)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "ok" {
		t.Fatalf("healthz: код %d, тело %q", recorder.Code, recorder.Body.String())
	}
	header := recorder.Header()
	if header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("нет X-Content-Type-Options: %v", header)
	}
	if !strings.Contains(header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("нет политики содержимого: %v", header)
	}
	if header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("нет X-Frame-Options: %v", header)
	}
}

func TestBodyLimitRejectsHugeForm(t *testing.T) {
	server, _ := newTestServer(t, func(cfg *config.Configuration) { cfg.Server.MaxBodyBytes = 4096 })
	cookie := login(t, server)
	csrf := csrfToken(t, server, cookie, "/")
	form := url.Values{"csrf": {csrf}, "database": {"fse-1234"}, "note": {strings.Repeat("x", 8192)}}
	recorder := postForm(server, "/operations/backup", cookie, form)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("превышение размера запроса: код %d, ожидался 400", recorder.Code)
	}
}

func TestClientIPParsesForwardedHeader(t *testing.T) {
	server, _ := newTestServer(t, func(cfg *config.Configuration) {
		cfg.Server.TrustedProxies = []string{"10.0.0.1", "192.168.7.0/24"}
	})
	cases := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{name: "прямое подключение", remoteAddr: "192.168.7.50:5555", want: "192.168.7.50"},
		{name: "доверенный прокси", remoteAddr: "10.0.0.1:5555", forwarded: "203.0.113.7, 10.0.0.1", want: "203.0.113.7"},
		{name: "подсеть в списке", remoteAddr: "192.168.7.2:5555", forwarded: "203.0.113.8", want: "203.0.113.8"},
		{name: "чужой прокси", remoteAddr: "203.0.113.9:5555", forwarded: "10.1.1.1", want: "203.0.113.9"},
		{name: "адрес без порта", remoteAddr: "192.168.7.50", want: "192.168.7.50"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			request.RemoteAddr = testCase.remoteAddr
			if testCase.forwarded != "" {
				request.Header.Set("X-Forwarded-For", testCase.forwarded)
			}
			if got := server.clientIP(request); got != testCase.want {
				t.Fatalf("clientIP = %q, ожидалось %q", got, testCase.want)
			}
		})
	}
}

func TestJobsLimitIsClamped(t *testing.T) {
	cases := map[int]int{0: defaultJobsLimit, -5: defaultJobsLimit, 10: 10, maxJobsLimit + 1: maxJobsLimit}
	for requested, want := range cases {
		if got := jobsLimit(requested); got != want {
			t.Errorf("jobsLimit(%d) = %d, ожидалось %d", requested, got, want)
		}
	}
}

func TestNoticeTextIgnoresUnknownCode(t *testing.T) {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/jobs?msg=%3Cscript%3E&job=1", nil)
	if got := noticeText(request, "<script>"); got != "" {
		t.Fatalf("неизвестный код сообщения принят: %q", got)
	}
	if got := noticeText(request, ""); got != "" {
		t.Fatalf("пустой код дал сообщение: %q", got)
	}
	if got := noticeText(request, "backup"); !strings.Contains(got, "Задача: 1") {
		t.Fatalf("сообщение без идентификатора задачи: %q", got)
	}
}

func TestNewRejectsIncompleteOptions(t *testing.T) {
	server, operations := newTestServer(t, nil)
	authenticator := server.auth
	cases := []struct {
		name    string
		options Options
		substr  string
	}{
		{
			name:    "нет операций",
			options: Options{Config: server.cfg, Auth: authenticator},
			substr:  "не заданы операции",
		},
		{
			name:    "нет проверки входа",
			options: Options{Config: server.cfg, Operations: operations},
			substr:  "не задана проверка входа",
		},
		{
			name:    "нет адреса",
			options: Options{Config: config.Configuration{}, Operations: operations, Auth: authenticator},
			substr:  "server.listen",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := New(testCase.options); err == nil {
				t.Fatal("ожидалась ошибка, получено nil")
			} else if !strings.Contains(err.Error(), testCase.substr) {
				t.Fatalf("ошибка %q не содержит %q", err, testCase.substr)
			}
		})
	}
}
