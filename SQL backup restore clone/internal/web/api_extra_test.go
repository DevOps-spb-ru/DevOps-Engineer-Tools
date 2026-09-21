package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/auth"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

func TestAPICloneAndInvalidBody(t *testing.T) {
	server, operations := newTestServer(t, nil)
	recorder := apiRequest(server, http.MethodPost, "/api/v1/clone", testToken, `{"from":"fse-1234","to":"fssd-7"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("клонирование: код %d (%s)", recorder.Code, recorder.Body.String())
	}
	if operations.lastClone.To != "fssd-7" {
		t.Fatalf("операция получила %+v", operations.lastClone)
	}

	// Лишнее поле — отказ: иначе опечатка в имени условия молча игнорируется.
	recorder = apiRequest(server, http.MethodPost, "/api/v1/clone", testToken, `{"from":"fse-1234","target":"fssd-7"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("неизвестное поле: код %d, ожидался 400", recorder.Code)
	}
	if parsed := decodeAPIError(t, recorder); parsed.Error.Type != errorInvalid {
		t.Fatalf("тип ошибки %q, ожидался %q", parsed.Error.Type, errorInvalid)
	}
}

func TestAPIReadOnlyForbidden(t *testing.T) {
	server, _ := newTestServer(t, func(cfg *config.Configuration) { cfg.Server.ReadOnly = true })
	recorder := apiRequest(server, http.MethodPost, "/api/v1/backup", testToken, `{"database":"fse-1234"}`)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("режим «только просмотр»: код %d, ожидался 403", recorder.Code)
	}
	if parsed := decodeAPIError(t, recorder); parsed.Error.Type != errorForbidden {
		t.Fatalf("тип ошибки %q, ожидался %q", parsed.Error.Type, errorForbidden)
	}
}

func TestAPIListBackupsAndJobs(t *testing.T) {
	server, _ := newTestServer(t, nil)
	recorder := apiRequest(server, http.MethodGet, "/api/v1/backups?db=fse-1234", testToken, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("список бэкапов: код %d (%s)", recorder.Code, recorder.Body.String())
	}
	var backups backupsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &backups); err != nil {
		t.Fatalf("ответ не разобран: %v", err)
	}
	if len(backups.Backups) != 1 || backups.Backups[0].ID == "" || backups.Backups[0].Size == "" {
		t.Fatalf("список бэкапов: %+v", backups.Backups)
	}

	recorder = apiRequest(server, http.MethodGet, "/api/v1/jobs?limit=1", testToken, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("журнал: код %d (%s)", recorder.Code, recorder.Body.String())
	}
	var listed jobsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("ответ не разобран: %v", err)
	}
	if len(listed.Jobs) != 1 || listed.Jobs[0].Kind != jobs.KindBackup {
		t.Fatalf("журнал: %+v", listed.Jobs)
	}
}

func TestAPIPruneReportsPlan(t *testing.T) {
	server, operations := newTestServer(t, nil)
	operations.pruneResults = []backup.PruneResult{{
		Database: "fse-1234",
		DryRun:   true,
		Removed:  []store.Backup{{ID: "fse-1234/20260901T020000Z", DB: "fse-1234", SizeBytes: 1024}},
	}}
	recorder := apiRequest(server, http.MethodPost, "/api/v1/prune", testToken, `{"dry_run":true}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("уборка: код %d (%s)", recorder.Code, recorder.Body.String())
	}
	var response pruneResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("ответ не разобран: %v", err)
	}
	if !response.DryRun || response.Removed != 1 || len(response.Databases) != 1 {
		t.Fatalf("ответ уборки: %+v", response)
	}
	if !operations.lastPrune.DryRun || operations.lastPrune.Operator != "ci (api)" {
		t.Fatalf("операция получила %+v", operations.lastPrune)
	}
}

// TestTokenIdentityOperator проверяет, что токен опознаётся как API-действие:
// по записи в журнале видно, что операцию запустил не человек в интерфейсе.
func TestTokenIdentityOperator(t *testing.T) {
	authenticator, err := auth.New(auth.Options{
		Tokens:           []auth.Token{{Login: "ci", Value: strings.Repeat("z", auth.MinTokenLength)}},
		SessionTTL:       time.Hour,
		LoginBackoff:     time.Second,
		MaxLoginAttempts: 3,
	})
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	verified, err := authenticator.Token(strings.Repeat("z", auth.MinTokenLength))
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got := verified.Operator(); got != "ci (api)" {
		t.Fatalf("Operator() = %q, ожидалось «ci (api)»", got)
	}
}
