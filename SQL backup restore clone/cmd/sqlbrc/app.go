package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/backup"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/store"
)

// appState — общее состояние CLI: адреса вывода, путь к конфигу и флаги журнала.
// Команды получают appState, а не читают флаги напрямую: так проверки CLI
// работают с буферами вывода, а не с os.Stdout.
type appState struct {
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	configPath string
	logLevel   string
	logFormat  string
}

// validateFlags проверяет флаги журнала до запуска команды: опечатка в
// --log-level должна выясняться сразу, а не в журнале после первой операции.
func (a *appState) validateFlags(_ *cobra.Command, _ []string) error {
	if a.logLevel != "" && !config.IsValidLevel(a.logLevel) {
		return fmt.Errorf("неизвестный --log-level %q (допустимы %s, %s, %s, %s)",
			a.logLevel, config.LevelDebug, config.LevelInfo, config.LevelWarn, config.LevelError)
	}
	if a.logFormat != "" && !config.IsValidFormat(a.logFormat) {
		return fmt.Errorf("неизвестный --log-format %q (допустимы %s и %s)",
			a.logFormat, config.FormatText, config.FormatJSON)
	}
	return nil
}

// readConfig читает конфиг и применяет переопределения из флагов CLI, но не
// проверяет его: `doctor` показывает замечания к конфигу отдельной строкой
// отчёта, а не завершается одним сообщением об ошибке до прогона проверок.
// Команды, которые выполняют операции, используют config.Load с проверкой.
func (a *appState) readConfig() (config.Configuration, string, error) {
	cfg, path, err := config.Read(a.configPath)
	if err != nil {
		return config.Configuration{}, path, err
	}
	if a.logLevel != "" {
		cfg.Logging.Level = a.logLevel
	}
	if a.logFormat != "" {
		cfg.Logging.Format = a.logFormat
	}
	return cfg, path, nil
}

// service собирает сервис операций: клиент PostgreSQL, каталог бэкапов, очередь
// задач и журнал. В отличие от readConfig, здесь конфиг проверяется (config.Load):
// операция не должна начинаться с неполными настройками.
func (a *appState) service() (*backup.Service, error) {
	cfg, _, err := config.Load(a.configPath)
	if err != nil {
		return nil, err
	}
	client := pg.NewClient(cfg.Postgres.ClientConfig(), nil, a.stderr)
	storage := store.New(cfg.Storage.Dir, cfg.Databases.Pattern, nil)
	service, err := backup.New(backup.Options{
		Config: cfg,
		PG:     client,
		Store:  storage,
	})
	if err != nil {
		return nil, err
	}
	return service, nil
}

// requireDatabase проверяет, что имя базы задано: без него команда не знает, что
// делать, и подсказка вместо пустой ошибки экономит один запуск --help.
func requireDatabase(database string) error {
	if strings.TrimSpace(database) == "" {
		return errors.New("не задана база: укажите --db <имя>")
	}
	return nil
}

// operator возвращает имя того, кто запустил команду: значение попадает в журнал
// задач, поэтому по записи видно, кто снял бэкап или развернул стенд. При запуске
// через sudo исходный пользователь лежит в SUDO_USER.
func operator() string {
	for _, name := range []string{"SQLBRC_OPERATOR", "SUDO_USER", "USER", "LOGNAME", "USERNAME"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}
