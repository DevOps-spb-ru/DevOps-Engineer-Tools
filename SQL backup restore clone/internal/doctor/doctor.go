// Package doctor проверяет готовность сервера к работе sqlbrc: доступ к PostgreSQL
// под служебным пользователем, каталоги бэкапов и права, локали, свободное место,
// чтение архивов пользователем postgres (AppArmor), юнит systemd и безопасность
// веб-интерфейса. Проверки ничего не меняют — это read-only прогон.
package doctor

import (
	"context"
	"fmt"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// Level — уровень результата проверки.
type Level string

const (
	// LevelOK — проверка пройдена.
	LevelOK Level = "ok"
	// LevelWarn — сервис заработает, но есть риск (например, каталог открыт на запись всем).
	LevelWarn Level = "warn"
	// LevelError — так сервис работать не сможет.
	LevelError Level = "error"
)

// DefaultServiceUser — пользователь, под которым работает сервис: имя нужно
// в подсказках про права на каталоги и правила sudoers.
const DefaultServiceUser = "sqlbrc"

// DefaultUnitPaths — где искать юнит systemd сервиса (Debian).
var DefaultUnitPaths = []string{
	"/etc/systemd/system/sqlbrc.service",
	"/lib/systemd/system/sqlbrc.service",
}

// Check — результат одной проверки.
type Check struct {
	// Name — короткое имя проверки (sudo, storage-dir, locales, ...).
	Name string `json:"name"`
	// Level — ok, warn или error.
	Level Level `json:"level"`
	// Message — что именно проверено.
	Message string `json:"message"`
	// Hint — что сделать, если проверка не пройдена.
	Hint string `json:"hint,omitempty"`
}

// Report — результат прогона проверок.
type Report struct {
	// GeneratedAt — когда выполнялся прогон.
	GeneratedAt time.Time `json:"generated_at"`
	// ConfigPath — прочитанный конфиг (пусто, если прогон шёл без него).
	ConfigPath string `json:"config_path,omitempty"`
	// Checks — проверки в порядке выполнения.
	Checks []Check `json:"checks"`
}

// add добавляет результат проверки.
func (r *Report) add(name string, level Level, message, hint string) {
	r.Checks = append(r.Checks, Check{Name: name, Level: level, Message: message, Hint: hint})
}

// count считает проверки с указанным уровнем.
func (r Report) count(level Level) int {
	total := 0
	for _, check := range r.Checks {
		if check.Level == level {
			total++
		}
	}
	return total
}

// Errors возвращает число ошибок.
func (r Report) Errors() int { return r.count(LevelError) }

// Warnings возвращает число предупреждений.
func (r Report) Warnings() int { return r.count(LevelWarn) }

// ExitCode возвращает код возврата: 1, если есть ошибки. Предупреждения код не
// меняют — они не мешают работать, но видны в отчёте.
func (r Report) ExitCode() int {
	if r.Errors() > 0 {
		return 1
	}
	return 0
}

// PG — часть возможностей pg.Client, нужная проверкам.
type PG interface {
	Ping(ctx context.Context) error
	ServerVersion(ctx context.Context) (pg.Version, error)
	ClientVersions(ctx context.Context) (map[string]pg.Version, error)
	RoleInfo(ctx context.Context) (pg.Role, error)
	ListDatabases(ctx context.Context) ([]pg.Database, error)
}

// Options — зависимости и настройки прогона. Все внешние эффекты идут через
// интерфейсы, поэтому проверки тестируются без PostgreSQL, root и реальных каталогов.
type Options struct {
	// Cfg — конфигурация сервиса.
	Cfg config.Configuration
	// ConfigPath — путь к прочитанному конфигу (попадает в отчёт).
	ConfigPath string
	// PG — клиент PostgreSQL.
	PG PG
	// FS — файловые операции.
	FS FS
	// Runner — запуск внешних команд (sudo, locale).
	Runner pg.CommandRunner
	// UnitPaths — где искать юнит systemd (по умолчанию DefaultUnitPaths).
	UnitPaths []string
	// Now подменяет текущее время в тестах.
	Now func() time.Time
}

// Run выполняет все проверки и возвращает отчёт. Проверки не прерывают друг
// друга: администратор должен увидеть состояние сервера целиком за один запуск.
func Run(ctx context.Context, opts Options) Report {
	opts = withDefaults(opts)
	report := Report{GeneratedAt: opts.Now(), ConfigPath: opts.ConfigPath}
	if err := opts.Cfg.Validate(); err != nil {
		report.add("config", LevelError, err.Error(),
			"исправьте конфиг или возьмите за основу deploy/config.example.yaml")
		return report
	}
	report.add("config", LevelOK, fmt.Sprintf("конфиг корректен: режим %s, каталог бэкапов %s",
		opts.Cfg.Postgres.Mode, opts.Cfg.Storage.Dir), "")

	checkSudo(ctx, &report, opts)
	server := checkServer(ctx, &report, opts)
	checkClients(ctx, &report, opts, server)
	checkRole(ctx, &report, opts)
	checkBackupDir(&report, opts)
	checkStateDir(&report, opts)
	checkLocales(ctx, &report, opts)
	checkArchiveAccess(ctx, &report, opts)
	checkUnit(&report, opts)
	checkWeb(&report, opts)
	checkRetention(ctx, &report, opts)
	return report
}

// withDefaults подставляет значения, которых нет в Options.
func withDefaults(opts Options) Options {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if len(opts.UnitPaths) == 0 {
		opts.UnitPaths = DefaultUnitPaths
	}
	if opts.FS == nil {
		opts.FS = OSFS{}
	}
	if opts.Runner == nil {
		opts.Runner = pg.ExecRunner{}
	}
	return opts
}
