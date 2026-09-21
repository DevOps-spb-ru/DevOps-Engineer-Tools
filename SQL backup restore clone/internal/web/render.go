package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/jobs"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

// pageNames — страницы интерфейса: имя совпадает с именем файла шаблона.
var pageNames = []string{"login", "index", "backups", "jobs"}

// templates — разобранные страницы. Свой набор шаблонов нужен каждой странице:
// html/template выбирает определение по имени, а содержимое страницы
// подставляется в макет через {{template "content" .}}.
type templates struct {
	pages map[string]*template.Template
}

// parseTemplates разбирает встроенные шаблоны. Ошибка означает опечатку в
// шаблоне: сервис должен узнать об этом при старте, а не при первом заходе в
// интерфейс.
func parseTemplates() (*templates, error) {
	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		parsed, err := template.New(name).Funcs(templateFuncs()).ParseFS(templateFS,
			"templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("не удалось разобрать шаблон %s: %w", name, err)
		}
		pages[name] = parsed
	}
	return &templates{pages: pages}, nil
}

// render отдаёт страницу с заданным кодом ответа. Код важен для отказов: клиент
// должен видеть отказ, а не успешно показанную форму. Ошибка означает, что ответ
// уже начат: сообщить о ней клиенту нельзя, поэтому её смотрит вызывающий код.
func (t *templates) render(w http.ResponseWriter, name string, status int, data any) error {
	parsed, ok := t.pages[name]
	if !ok {
		return fmt.Errorf("неизвестная страница %q", name)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	return parsed.ExecuteTemplate(w, "layout", data)
}

// templateFuncs — функции, доступные шаблонам: размеры, время и названия
// состояний. Форматирование живёт здесь, а не в разметке: шаблон должен
// читаться как страница, а не как код.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"size":      bytesize.HumanSize,
		"time":      formatTime,
		"status":    statusRU,
		"jobStatus": jobStatusRU,
		"jobKind":   jobKindRU,
	}
}

// formatTime печатает время в UTC: журнал и имена бэкапов уже в UTC, поэтому
// читателю не нужно держать в голове пояс сервера.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

// statusRU переводит состояние бэкапа в текст страницы. Метаданных нет —
// значит архив появился не из сервиса или файл метаданных потерян.
func statusRU(status store.Status) string {
	if status == store.StatusComplete {
		return "готов"
	}
	return "без метаданных"
}

// jobStatusRU переводит состояние задачи.
func jobStatusRU(status jobs.Status) string {
	switch status {
	case jobs.StatusDone:
		return "выполнено"
	case jobs.StatusFailed:
		return "ошибка"
	case jobs.StatusRunning:
		return "выполняется"
	default:
		return string(status)
	}
}

// jobKindRU переводит вид задачи.
func jobKindRU(kind jobs.Kind) string {
	switch kind {
	case jobs.KindBackup:
		return "бэкап"
	case jobs.KindRestore:
		return "восстановление"
	case jobs.KindClone:
		return "клонирование"
	case jobs.KindPrune:
		return "уборка"
	default:
		return string(kind)
	}
}
