package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/auth"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// Коды ошибок API. Клиент различает случаи по полю type, а не по тексту:
// сообщение можно переформулировать, а код должен оставаться неизменным — это
// часть публичного контракта API.
const (
	errorInvalid      = "invalid_request"
	errorUnauthorized = "unauthorized"
	errorForbidden    = "forbidden"
	errorRejected     = "rejected"
	errorInternal     = "internal"
)

// apiErrorBody — тело ошибки API.
type apiErrorBody struct {
	// Type — машинный код: invalid_request, unauthorized, forbidden, rejected,
	// internal.
	Type string `json:"type"`
	// Message — текст для человека.
	Message string `json:"message"`
}

// apiError — ответ API с ошибкой.
type apiError struct {
	Error apiErrorBody `json:"error"`
}

// backupDTO — бэкап в ответе API. Поля повторяют store.Backup, но с именами в
// snake_case и готовым человекочитаемым размером: клиенту не нужно знать про
// bytesize.
type backupDTO struct {
	ID        string    `json:"id"`
	Database  string    `json:"database"`
	Tag       string    `json:"tag,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
	Size      string    `json:"size"`
	Status    string    `json:"status"`
	Note      string    `json:"note,omitempty"`
	JobID     string    `json:"job_id,omitempty"`
	SHA256    string    `json:"sha256,omitempty"`
}

// newBackupDTO переводит бэкап хранилища в ответ API.
func newBackupDTO(item store.Backup) backupDTO {
	return backupDTO{
		ID:        item.ID,
		Database:  item.DB,
		Tag:       item.Tag,
		CreatedAt: item.CreatedAt.UTC(),
		SizeBytes: item.SizeBytes,
		Size:      bytesize.HumanSize(item.SizeBytes),
		Status:    string(item.Status),
		Note:      item.Note,
		JobID:     item.JobID,
		SHA256:    item.SHA256,
	}
}

// newBackupDTOs переводит список бэкапов.
func newBackupDTOs(items []store.Backup) []backupDTO {
	dtos := make([]backupDTO, 0, len(items))
	for _, item := range items {
		dtos = append(dtos, newBackupDTO(item))
	}
	return dtos
}

// backupsResponse — ответ API со списком бэкапов.
type backupsResponse struct {
	Backups []backupDTO `json:"backups"`
}

// jobsResponse — ответ API с журналом задач.
type jobsResponse struct {
	Jobs []jobs.Job `json:"jobs"`
}

// operationResponse — ответ API на изменяющую операцию.
type operationResponse struct {
	Job     jobs.Job   `json:"job"`
	Backup  *backupDTO `json:"backup,omitempty"`
	Message string     `json:"message"`
}

// backupRequest — тело запроса на снятие бэкапа.
type backupRequest struct {
	Database string `json:"database"`
	Tag      string `json:"tag"`
	Note     string `json:"note"`
}

// restoreRequest — тело запроса на восстановление.
type restoreRequest struct {
	Database string `json:"database"`
	BackupID string `json:"backup_id"`
	NoClean  bool   `json:"no_clean"`
}

// cloneRequest — тело запроса на клонирование.
type cloneRequest struct {
	From     string `json:"from"`
	To       string `json:"to"`
	BackupID string `json:"backup_id"`
}

// pruneRequest — тело запроса на уборку.
type pruneRequest struct {
	Database string `json:"database"`
	DryRun   bool   `json:"dry_run"`
}

// pruneResponse — ответ API на уборку.
type pruneResponse struct {
	DryRun    bool        `json:"dry_run"`
	Removed   int         `json:"removed"`
	Databases []string    `json:"databases"`
	Backups   []backupDTO `json:"backups"`
}

// writeJSON отдаёт ответ в JSON. Ошибку кодирования сообщить клиенту уже
// нельзя — код ответа отправлен, — поэтому она попадает в журнал сервиса.
func (s *Server) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.logf("не удалось записать ответ API: %v", err)
	}
}

// writeAPIError отдаёт ошибку в формате API.
func (s *Server) writeAPIError(w http.ResponseWriter, status int, kind, message string) {
	s.writeJSON(w, status, apiError{Error: apiErrorBody{Type: kind, Message: message}})
}

// errInvalidBody означает, что тело запроса не разобрано: обработчик отвечает
// на такую ошибку кодом 400, а не 500.
var errInvalidBody = errors.New("тело запроса не разобрано")

// apiDecodeRequest читает тело запроса в target. Пустое тело допустимо: у
// операций есть значения по умолчанию (например, «последний бэкап базы»).
// Ошибка оборачивает errInvalidBody, чтобы вызывающий код отличал отказ разбора
// от сбоя операции.
func (s *Server) apiDecodeRequest(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	// Лишнее поле — почти всегда опечатка в имени: лучше отказать, чем молча
	// проигнорировать условие операции.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("%w: %w", errInvalidBody, err)
	}
	return nil
}

// handleAPIListBackups отдаёт список бэкапов: одной базы или всего хранилища.
func (s *Server) handleAPIListBackups(w http.ResponseWriter, r *http.Request) {
	s.requireToken(func(w http.ResponseWriter, r *http.Request, _ auth.Identity) {
		backups, err := s.operations.Backups(strings.TrimSpace(r.URL.Query().Get("db")))
		if err != nil {
			s.apiInternal(w, "список бэкапов", err)
			return
		}
		s.writeJSON(w, http.StatusOK, backupsResponse{Backups: newBackupDTOs(backups)})
	})(w, r)
}

// handleAPIListJobs отдаёт журнал задач.
func (s *Server) handleAPIListJobs(w http.ResponseWriter, r *http.Request) {
	s.requireToken(func(w http.ResponseWriter, r *http.Request, _ auth.Identity) {
		listed, err := s.operations.Jobs(jobsLimit(queryInt(r, "limit")))
		if err != nil {
			s.apiInternal(w, "журнал задач", err)
			return
		}
		s.writeJSON(w, http.StatusOK, jobsResponse{Jobs: listed})
	})(w, r)
}

// handleAPIBackup снимает бэкап.
func (s *Server) handleAPIBackup(w http.ResponseWriter, r *http.Request) {
	s.requireToken(func(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
		s.apiOperation(w, r, identity, "backup", func(r *http.Request) (backup.Result, error) {
			var request backupRequest
			if err := s.apiDecodeRequest(r, &request); err != nil {
				return backup.Result{}, err
			}
			return s.operations.Backup(r.Context(), backup.BackupOptions{
				Database: strings.TrimSpace(request.Database),
				Tag:      strings.TrimSpace(request.Tag),
				Note:     strings.TrimSpace(request.Note),
				Operator: identity.Operator(),
			})
		})
	})(w, r)
}

// handleAPIRestore восстанавливает базу из бэкапа.
func (s *Server) handleAPIRestore(w http.ResponseWriter, r *http.Request) {
	s.requireToken(func(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
		s.apiOperation(w, r, identity, "restore", func(r *http.Request) (backup.Result, error) {
			var request restoreRequest
			if err := s.apiDecodeRequest(r, &request); err != nil {
				return backup.Result{}, err
			}
			return s.operations.Restore(r.Context(), backup.RestoreOptions{
				Database:  strings.TrimSpace(request.Database),
				BackupID:  strings.TrimSpace(request.BackupID),
				SkipClean: request.NoClean,
				Operator:  identity.Operator(),
			})
		})
	})(w, r)
}

// handleAPIClone клонирует базу в новый стенд.
func (s *Server) handleAPIClone(w http.ResponseWriter, r *http.Request) {
	s.requireToken(func(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
		s.apiOperation(w, r, identity, "clone", func(r *http.Request) (backup.Result, error) {
			var request cloneRequest
			if err := s.apiDecodeRequest(r, &request); err != nil {
				return backup.Result{}, err
			}
			return s.operations.Clone(r.Context(), backup.CloneOptions{
				From:     strings.TrimSpace(request.From),
				To:       strings.TrimSpace(request.To),
				BackupID: strings.TrimSpace(request.BackupID),
				Operator: identity.Operator(),
			})
		})
	})(w, r)
}

// apiOperation выполняет изменяющую операцию API: проверяет режим «только
// просмотр», вызывает переданную операцию и отдаёт результат. Проверки и ответы
// у backup, restore и clone одинаковые, поэтому они собраны в одном месте.
func (s *Server) apiOperation(w http.ResponseWriter, r *http.Request, identity auth.Identity, action string,
	run func(*http.Request) (backup.Result, error)) {
	if !s.apiWritable(w, identity, action) {
		return
	}
	result, err := run(r)
	switch {
	case err == nil:
		s.writeOperationResult(w, identity, action, result)
	case errors.Is(err, errInvalidBody):
		s.writeAPIError(w, http.StatusBadRequest, errorInvalid, err.Error())
	default:
		s.apiOperationError(w, identity, action, err)
	}
}

// writeOperationResult отдаёт итог изменяющей операции: сама запись ответа и
// строка в журнале у backup, restore и clone одинаковые, поэтому она здесь.
func (s *Server) writeOperationResult(w http.ResponseWriter, identity auth.Identity, action string, result backup.Result) {
	s.logf("API: операция %s выполнена (%s): задача %s", action, identity.Operator(), result.Job.ID)
	s.writeJSON(w, http.StatusCreated, operationResponse{
		Job:     result.Job,
		Backup:  backupPointer(result.Backup),
		Message: result.Job.Message,
	})
}

// handleAPIPrune применяет политику хранения.
func (s *Server) handleAPIPrune(w http.ResponseWriter, r *http.Request) {
	s.requireToken(func(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
		if !s.apiWritable(w, identity, "prune") {
			return
		}
		var request pruneRequest
		if err := s.apiDecodeRequest(r, &request); err != nil {
			s.writeAPIError(w, http.StatusBadRequest, errorInvalid, err.Error())
			return
		}
		results, err := s.operations.Prune(r.Context(), backup.PruneOptions{
			Database: strings.TrimSpace(request.Database),
			DryRun:   request.DryRun,
			Operator: identity.Operator(),
		})
		if err != nil {
			s.apiOperationError(w, identity, "prune", err)
			return
		}
		response := pruneResponse{DryRun: request.DryRun}
		for _, result := range results {
			response.Databases = append(response.Databases, result.Database)
			response.Backups = append(response.Backups, newBackupDTOs(result.Removed)...)
		}
		response.Removed = len(response.Backups)
		s.logf("API: уборка выполнена (%s): баз %d, бэкапов %d", identity.Operator(), len(results), response.Removed)
		s.writeJSON(w, http.StatusOK, response)
	})(w, r)
}

// apiWritable отвечает, разрешена ли изменяющая операция. Отказ возвращается
// кодом 403: клиент видит, что дело в режиме сервиса, а не в его запросе.
func (s *Server) apiWritable(w http.ResponseWriter, identity auth.Identity, action string) bool {
	if err := s.ensureWritable(); err != nil {
		s.logf("API: операция %s (%s) отклонена: %v", action, identity.Operator(), err)
		s.writeAPIError(w, http.StatusForbidden, errorForbidden, err.Error())
		return false
	}
	return true
}

// apiOperationError переводит ошибку операции в ответ API: отказ по правилам
// отделяется от внутренней ошибки кодом 409 и типом rejected.
func (s *Server) apiOperationError(w http.ResponseWriter, identity auth.Identity, action string, err error) {
	status, kind := http.StatusInternalServerError, errorInternal
	if backup.IsRejected(err) {
		status, kind = http.StatusConflict, errorRejected
	}
	s.logf("API: операция %s (%s) не выполнена: %v", action, identity.Operator(), err)
	s.writeAPIError(w, status, kind, err.Error())
}

// apiInternal отвечает на ошибку чтения данных (список бэкапов, журнал).
func (s *Server) apiInternal(w http.ResponseWriter, action string, err error) {
	s.logf("API: %s: %v", action, err)
	s.writeAPIError(w, http.StatusInternalServerError, errorInternal, err.Error())
}

// backupPointer переводит бэкап в указатель: отсутствие бэкапа в ответе
// отличается от пустого объекта.
func backupPointer(item store.Backup) *backupDTO {
	if item.ID == "" {
		return nil
	}
	dto := newBackupDTO(item)
	return &dto
}
