package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
)

func TestLoginAndLogout(t *testing.T) {
	server, _ := newTestServer(t, nil)
	cookie := login(t, server)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie сессии без HttpOnly/SameSite=Strict: %+v", cookie)
	}

	// Главная страница показывает базу из хранилища.
	recorder := get(server, "/", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("главная: код %d", recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "fse-1234") {
		t.Fatalf("на главной нет базы fse-1234: %s", body)
	}

	// Выход требует токена формы и отзывает сессию.
	csrf := csrfToken(t, server, cookie, "/")
	if recorder := postForm(server, "/logout", cookie, url.Values{"csrf": {"чужой токен"}}); recorder.Code != http.StatusForbidden {
		t.Fatalf("выход с чужим токеном: код %d, ожидался 403", recorder.Code)
	}
	if recorder := postForm(server, "/logout", cookie, url.Values{"csrf": {csrf}}); recorder.Code != http.StatusSeeOther {
		t.Fatalf("выход: код %d, ожидался 303", recorder.Code)
	}
	if recorder := get(server, "/", cookie); recorder.Code != http.StatusSeeOther {
		t.Fatalf("после выхода главная отвечает %d, ожидался редирект на вход", recorder.Code)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	server, _ := newTestServer(t, nil)
	form := url.Values{"login": {"ivan"}, "password": {"не тот пароль"}}
	recorder := postForm(server, "/login", nil, form)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("неверный пароль: код %d, ожидался 401", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), testPassword) {
		t.Fatal("на странице ошибки не должно быть пароля")
	}
}

func TestPagesRequireSession(t *testing.T) {
	server, _ := newTestServer(t, nil)
	for _, path := range []string{"/", "/backups", "/jobs"} {
		if recorder := get(server, path, nil); recorder.Code != http.StatusSeeOther {
			t.Errorf("страница %s без сессии: код %d, ожидался редирект", path, recorder.Code)
		}
	}
}

func TestOperationRequiresCSRF(t *testing.T) {
	server, operations := newTestServer(t, nil)
	cookie := login(t, server)
	recorder := postForm(server, "/operations/backup", cookie, url.Values{"database": {"fse-1234"}})
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("без токена формы: код %d, ожидался 403", recorder.Code)
	}
	if operations.lastBackup.Database != "" {
		t.Fatalf("операция выполнена без токена формы: %+v", operations.lastBackup)
	}
}

func TestBackupOperationRedirectsToJournal(t *testing.T) {
	server, operations := newTestServer(t, nil)
	cookie := login(t, server)
	csrf := csrfToken(t, server, cookie, "/")
	form := url.Values{"csrf": {csrf}, "database": {"fse-1234"}, "tag": {"before-release"}, "note": {"проверка"}}
	recorder := postForm(server, "/operations/backup", cookie, form)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("операция backup: код %d (%s)", recorder.Code, recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); !strings.Contains(location, "msg=backup") {
		t.Fatalf("после операции ожидался редирект в журнал с сообщением, получено %q", location)
	}
	if operations.lastBackup.Database != "fse-1234" || operations.lastBackup.Tag != "before-release" {
		t.Fatalf("операция получила %+v", operations.lastBackup)
	}
	if operations.lastBackup.Operator != "ivan (web)" {
		t.Fatalf("оператор %q, ожидался «ivan (web)»", operations.lastBackup.Operator)
	}
}

func TestReadOnlyRejectsOperations(t *testing.T) {
	server, operations := newTestServer(t, func(cfg *config.Configuration) { cfg.Server.ReadOnly = true })
	cookie := login(t, server)
	csrf := csrfToken(t, server, cookie, "/")
	form := url.Values{"csrf": {csrf}, "database": {"fse-1234"}}
	recorder := postForm(server, "/operations/backup", cookie, form)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("режим «только просмотр»: код %d, ожидался 409", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "read_only") {
		t.Fatalf("в отказе нет причины: %s", recorder.Body.String())
	}
	if operations.lastBackup.Database != "" {
		t.Fatal("в режиме «только просмотр» операция не должна выполняться")
	}
}

func TestRejectedOperationShowsConflict(t *testing.T) {
	server, operations := newTestServer(t, nil)
	operations.operationErr = backup.Rejected("база prod-1 не входит в список обслуживаемых")
	cookie := login(t, server)
	csrf := csrfToken(t, server, cookie, "/")
	form := url.Values{"csrf": {csrf}, "database": {"prod-1"}}
	recorder := postForm(server, "/operations/backup", cookie, form)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("отказ операции: код %d, ожидался 409", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "prod-1") {
		t.Fatalf("в отказе нет причины: %s", recorder.Body.String())
	}
}

func TestPruneDryRunShowsPlan(t *testing.T) {
	server, operations := newTestServer(t, nil)
	operations.pruneResults = []backup.PruneResult{{Database: "fse-1234", DryRun: true}}
	cookie := login(t, server)
	csrf := csrfToken(t, server, cookie, "/")
	form := url.Values{"csrf": {csrf}, "database": {""}, "dry_run": {"1"}}
	recorder := postForm(server, "/operations/prune", cookie, form)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("уборка «на бумаге»: код %d (%s)", recorder.Code, recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); !strings.Contains(location, "msg=prune-dry") {
		t.Fatalf("ожидался редирект с сообщением о плане, получено %q", location)
	}
	if !operations.lastPrune.DryRun {
		t.Fatal("уборка выполнена не «на бумаге»")
	}
}
