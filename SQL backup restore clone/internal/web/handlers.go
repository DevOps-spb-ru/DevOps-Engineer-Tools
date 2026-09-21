package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/auth"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// page — данные страницы. Одна структура на все страницы: шаблоны берут только
// нужные поля, а обработчикам не нужен десяток типов ради пары значений.
type page struct {
	// Общая часть: заголовок, шапка макета и сообщения.
	Title     string
	Login     string
	ReadOnly  bool
	Version   string
	CSRFToken string
	Notice    string
	Error     string

	// Данные страниц.
	Databases  []databaseView
	Backups    []store.Backup
	Jobs       []jobs.Job
	Database   string
	StorageDir string
	KeepLast   int
	KeepDays   int
	Limit      int
}

// databaseView — строка списка баз: сколько у базы бэкапов и какой из них свежий.
type databaseView struct {
	Name   string
	Count  int
	Latest store.Backup
}

// notices — тексты сообщений после операции. Сообщение выбирается по коду в
// адресе, а не передаётся текстом: иначе через адрес можно было бы показать
// пользователю любой текст «от имени сервиса».
var notices = map[string]string{
	"backup":    "Бэкап снят.",
	"restore":   "База восстановлена из бэкапа.",
	"clone":     "Стенд создан из бэкапа.",
	"prune":     "Уборка выполнена: лишние бэкапы удалены по политике хранения.",
	"prune-dry": "Уборка не выполнялась: план удаления записан в журнал задач.",
}

// basePage заполняет общую часть страницы.
func (s *Server) basePage(session auth.Session, title string) page {
	return page{
		Title:      title,
		Login:      session.Login,
		ReadOnly:   s.cfg.Server.ReadOnly,
		Version:    s.version,
		CSRFToken:  session.CSRFToken,
		StorageDir: s.cfg.Storage.Dir,
		KeepLast:   s.cfg.Storage.KeepLast,
		KeepDays:   s.cfg.Storage.KeepDays,
	}
}

// errorPage возвращает страницу с сообщением об ошибке.
func errorPage(data page, err error) page {
	data.Error = err.Error()
	return data
}

// writePage отдаёт страницу, а ошибку шаблона пишет в журнал: клиент уже
// получил код ответа, и сказать ему больше нечего.
func (s *Server) writePage(w http.ResponseWriter, name string, status int, data page) {
	if err := s.templates.render(w, name, status, data); err != nil {
		s.logf("не удалось отобразить страницу %s: %v", name, err)
	}
}

// noticeText собирает сообщение для страницы: текст берётся из фиксированного
// списка по коду, идентификатор задачи к нему добавляется отдельно.
func noticeText(r *http.Request, code string) string {
	if code == "" {
		return ""
	}
	text, ok := notices[code]
	if !ok {
		return ""
	}
	if job := strings.TrimSpace(r.URL.Query().Get("job")); job != "" {
		return text + " Задача: " + job + "."
	}
	return text
}

// handleLoginForm показывает форму входа. Вошедшего пользователя отправляем на
// главную: форма ему уже не нужна.
func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sessionFrom(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	data := page{Title: "Вход", Version: s.version}
	if r.URL.Query().Get("expired") != "" {
		data.Error = "Сессия истекла: войдите заново."
	}
	s.writePage(w, "login", http.StatusOK, data)
}

// handleLogin проверяет пароль и выдаёт cookie сессии. Отдельного сообщения о
// заблокированном входе достаточно: точное время разблокировки клиенту знать
// незачем, оно попадает в журнал сервиса.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.writePage(w, "login", http.StatusBadRequest,
			page{Title: "Вход", Version: s.version, Error: "не удалось разобрать форму: " + err.Error()})
		return
	}
	login := strings.TrimSpace(r.PostFormValue("login"))
	session, err := s.auth.Login(login, r.PostFormValue("password"))
	if err != nil {
		status := http.StatusUnauthorized
		message := "Неверный логин или пароль."
		if errors.Is(err, auth.ErrLocked) {
			status = http.StatusTooManyRequests
			message = "Слишком много неудачных попыток: вход временно заблокирован."
		}
		s.logf("неудачный вход %q с %s: %v", login, s.clientIP(r), err)
		s.writePage(w, "login", status, page{Title: "Вход", Version: s.version, Error: message})
		return
	}
	s.logf("вход выполнен: %s с %s", session.Login, s.clientIP(r))
	s.setSessionCookie(w, session)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout завершает сессию. Токен формы проверяется и здесь: выход —
// изменяющий запрос, и без проверки сторонний сайт мог бы разлогинить человека.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.requireSession(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		if !s.checkCSRF(w, r, session) {
			return
		}
		s.auth.Logout(session.ID)
		s.clearSessionCookie(w)
		s.logf("выход выполнен: %s", session.Login)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})(w, r)
}

// handleIndex показывает базы хранилища со свежим бэкапом каждой.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.requireSession(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		data := s.basePage(session, "Базы и бэкапы")
		data.Notice = noticeText(r, r.URL.Query().Get("msg"))
		names, err := s.operations.Databases()
		if err != nil {
			s.writePage(w, "index", http.StatusInternalServerError, errorPage(data, err))
			return
		}
		views := make([]databaseView, 0, len(names))
		for _, name := range names {
			backups, err := s.operations.Backups(name)
			if err != nil {
				s.writePage(w, "index", http.StatusInternalServerError, errorPage(data, err))
				return
			}
			view := databaseView{Name: name, Count: len(backups)}
			if len(backups) > 0 {
				view.Latest = backups[0]
			}
			views = append(views, view)
		}
		sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
		data.Databases = views
		s.writePage(w, "index", http.StatusOK, data)
	})(w, r)
}

// handleBackups показывает бэкапы одной базы или всех баз хранилища.
func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	s.requireSession(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		data := s.basePage(session, "Бэкапы")
		data.Database = strings.TrimSpace(r.URL.Query().Get("db"))
		data.Notice = noticeText(r, r.URL.Query().Get("msg"))
		backups, err := s.operations.Backups(data.Database)
		if err != nil {
			s.writePage(w, "backups", http.StatusInternalServerError, errorPage(data, err))
			return
		}
		data.Backups = backups
		s.writePage(w, "backups", http.StatusOK, data)
	})(w, r)
}

// handleJobs показывает журнал задач.
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	s.requireSession(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		limit := jobsLimit(queryInt(r, "limit"))
		data := s.basePage(session, "Журнал задач")
		data.Limit = limit
		data.Notice = noticeText(r, r.URL.Query().Get("msg"))
		listed, err := s.operations.Jobs(limit)
		if err != nil {
			s.writePage(w, "jobs", http.StatusInternalServerError, errorPage(data, err))
			return
		}
		data.Jobs = listed
		s.writePage(w, "jobs", http.StatusOK, data)
	})(w, r)
}

// handleBackup запускает снятие бэкапа.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	s.operationHandler(w, r, "backup", func(ctx context.Context, session auth.Session, r *http.Request) (jobs.Job, error) {
		result, err := s.operations.Backup(ctx, backup.BackupOptions{
			Database: strings.TrimSpace(r.PostFormValue("database")),
			Tag:      strings.TrimSpace(r.PostFormValue("tag")),
			Note:     strings.TrimSpace(r.PostFormValue("note")),
			Operator: session.Operator(),
		})
		return result.Job, err
	})
}

// handleRestore запускает восстановление базы из бэкапа.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	s.operationHandler(w, r, "restore", func(ctx context.Context, session auth.Session, r *http.Request) (jobs.Job, error) {
		result, err := s.operations.Restore(ctx, backup.RestoreOptions{
			Database: strings.TrimSpace(r.PostFormValue("database")),
			BackupID: strings.TrimSpace(r.PostFormValue("backup_id")),
			Operator: session.Operator(),
		})
		return result.Job, err
	})
}

// handleClone запускает клонирование базы в новый стенд.
func (s *Server) handleClone(w http.ResponseWriter, r *http.Request) {
	s.operationHandler(w, r, "clone", func(ctx context.Context, session auth.Session, r *http.Request) (jobs.Job, error) {
		result, err := s.operations.Clone(ctx, backup.CloneOptions{
			From:     strings.TrimSpace(r.PostFormValue("from")),
			To:       strings.TrimSpace(r.PostFormValue("to")),
			BackupID: strings.TrimSpace(r.PostFormValue("backup_id")),
			Operator: session.Operator(),
		})
		return result.Job, err
	})
}

// handlePrune применяет политику хранения. Уборка — единственная операция,
// которая возвращает итоги по нескольким базам, поэтому она обрабатывается
// отдельно от operationHandler.
func (s *Server) handlePrune(w http.ResponseWriter, r *http.Request) {
	s.requireSession(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		if !s.checkCSRF(w, r, session) {
			return
		}
		if err := s.ensureWritable(); err != nil {
			s.failOperation(w, session, "prune", err)
			return
		}
		dryRun := r.PostFormValue("dry_run") != ""
		results, err := s.operations.Prune(r.Context(), backup.PruneOptions{
			Database: strings.TrimSpace(r.PostFormValue("database")),
			DryRun:   dryRun,
			Operator: session.Operator(),
		})
		if err != nil {
			s.failOperation(w, session, "prune", err)
			return
		}
		code := "prune"
		if dryRun {
			code = "prune-dry"
		}
		s.logf("уборка (%s): баз %d, к удалению или удалено %d бэкапов",
			session.Operator(), len(results), removedCount(results))
		http.Redirect(w, r, "/jobs?msg="+code, http.StatusSeeOther)
	})(w, r)
}

// operationHandler выполняет изменяющую операцию: проверяет токен формы и режим
// «только просмотр», вызывает операцию и переводит результат в страницу.
func (s *Server) operationHandler(w http.ResponseWriter, r *http.Request, action string,
	call func(ctx context.Context, session auth.Session, r *http.Request) (jobs.Job, error)) {
	s.requireSession(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		if !s.checkCSRF(w, r, session) {
			return
		}
		if err := s.ensureWritable(); err != nil {
			s.failOperation(w, session, action, err)
			return
		}
		job, err := call(r.Context(), session, r)
		if err != nil {
			s.failOperation(w, session, action, err)
			return
		}
		s.logf("операция %s выполнена (%s): задача %s", action, session.Operator(), job.ID)
		http.Redirect(w, r, "/jobs?msg="+action+"&job="+url.QueryEscape(job.ID), http.StatusSeeOther)
	})(w, r)
}

// failOperation показывает отказ операции. Отказ по правилам (сервис не тронул
// данные) отличается от внутренней ошибки кодом ответа: 409 против 500.
func (s *Server) failOperation(w http.ResponseWriter, session auth.Session, action string, err error) {
	status := http.StatusInternalServerError
	if backup.IsRejected(err) {
		status = http.StatusConflict
	}
	s.logf("операция %s (%s) не выполнена: %v", action, session.Operator(), err)
	s.writePage(w, "index", status, errorPage(s.basePage(session, "Операция не выполнена"), err))
}

// checkCSRF разбирает форму и сверяет токен сессии.
func (s *Server) checkCSRF(w http.ResponseWriter, r *http.Request, session auth.Session) bool {
	if err := r.ParseForm(); err != nil {
		s.writePage(w, "index", http.StatusBadRequest,
			errorPage(s.basePage(session, "Запрос отклонён"), fmt.Errorf("не удалось разобрать форму: %w", err)))
		return false
	}
	if !session.VerifyCSRF(r.PostFormValue("csrf")) {
		s.writePage(w, "index", http.StatusForbidden,
			errorPage(s.basePage(session, "Запрос отклонён"),
				errors.New("токен формы не совпал: обновите страницу и повторите действие")))
		return false
	}
	return true
}

// queryInt читает целое из параметра адреса; мусор означает «значение по
// умолчанию», а не ошибку: адрес страницы правят руками, и падать из-за него
// незачем.
func queryInt(r *http.Request, name string) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(name)))
	if err != nil {
		return 0
	}
	return value
}

// removedCount считает бэкапы в итогах уборки: при «только показать» это план,
// иначе — фактически удалённые файлы.
func removedCount(results []backup.PruneResult) int {
	removed := 0
	for _, result := range results {
		removed += len(result.Removed)
	}
	return removed
}
