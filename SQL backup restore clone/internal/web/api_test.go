package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
)

// apiRequest отправляет запрос к API с токеном (или без него).
func apiRequest(server *Server, method, path, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

// decodeAPIError разбирает тело ошибки.
func decodeAPIError(t *testing.T, recorder *httptest.ResponseRecorder) apiError {
	t.Helper()
	var parsed apiError
	if err := json.Unmarshal(recorder.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("тело ошибки не разобрано (%s): %v", recorder.Body.String(), err)
	}
	return parsed
}

func TestAPIRequiresToken(t *testing.T) {
	server, operations := newTestServer(t, nil)
	recorder := apiRequest(server, http.MethodPost, "/api/v1/backup", "", `{"database":"fse-1234"}`)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("без токена: код %d, ожидался 401", recorder.Code)
	}
	if parsed := decodeAPIError(t, recorder); parsed.Error.Type != errorUnauthorized {
		t.Fatalf("тип ошибки %q, ожидался %q", parsed.Error.Type, errorUnauthorized)
	}
	if operations.lastBackup.Database != "" {
		t.Fatal("без токена операция выполняться не должна")
	}
	// Чужой токен — тот же отказ: иначе по ответу можно подбирать токены.
	if recorder := apiRequest(server, http.MethodPost, "/api/v1/backup", "не тот токен", "{}"); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("чужой токен: код %d, ожидался 401", recorder.Code)
	}
}

func TestAPIBackupCreatesJob(t *testing.T) {
	server, operations := newTestServer(t, nil)
	recorder := apiRequest(server, http.MethodPost, "/api/v1/backup", testToken,
		`{"database":"fse-1234","tag":"before-release"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("операция backup: код %d (%s)", recorder.Code, recorder.Body.String())
	}
	var response operationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("ответ не разобран: %v", err)
	}
	if response.Job.Kind != jobs.KindBackup || response.Job.Database != "fse-1234" {
		t.Fatalf("задача в ответе: %+v", response.Job)
	}
	if response.Job.Operator != "ci (api)" {
		t.Fatalf("оператор %q, ожидался «ci (api)»", response.Job.Operator)
	}
	if operations.lastBackup.Tag != "before-release" {
		t.Fatalf("операция получила %+v", operations.lastBackup)
	}
}

func TestAPIRestoreSendsSkipCleanAndRejectsForeignBase(t *testing.T) {
	server, operations := newTestServer(t, nil)
	recorder := apiRequest(server, http.MethodPost, "/api/v1/restore", testToken,
		`{"database":"fse-1234","backup_id":"fse-1234/20260921T020000Z","no_clean":true}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("восстановление: код %d (%s)", recorder.Code, recorder.Body.String())
	}
	if !operations.lastRestore.SkipClean || operations.lastRestore.BackupID == "" {
		t.Fatalf("операция получила %+v", operations.lastRestore)
	}

	operations.operationErr = backup.Rejected("база prod-1 не входит в список обслуживаемых")
	recorder = apiRequest(server, http.MethodPost, "/api/v1/restore", testToken, `{"database":"prod-1"}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("чужая база: код %d, ожидался 409", recorder.Code)
	}
	if parsed := decodeAPIError(t, recorder); parsed.Error.Type != errorRejected {
		t.Fatalf("тип ошибки %q, ожидался %q", parsed.Error.Type, errorRejected)
	}
}

// TestAPIRejectsCookieSession проверяет, что сессия браузера не даёт доступ к
// API: иначе защита от CSRF теряла бы смысл — сторонний сайт мог бы отправлять
// запросы с cookie человека.
func TestAPIRejectsCookieSession(t *testing.T) {
	server, _ := newTestServer(t, nil)
	cookie := login(t, server)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/backup",
		strings.NewReader(`{"database":"fse-1234"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("API с cookie-сессией: код %d, ожидался 401", recorder.Code)
	}
}
