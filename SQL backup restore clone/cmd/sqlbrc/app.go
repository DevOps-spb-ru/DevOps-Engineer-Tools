package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
)

// appState — общее состояние CLI: адреса вывода, путь к конфигу и флаги журнала.
// Команды получают appState, а не читают флаги напрямую: так проверки CLI
// работают с буферами вывода, а не с os.Stdout.
type appState struct {
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
