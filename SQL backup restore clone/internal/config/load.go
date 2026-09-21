package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
)

// Load читает конфиг и проверяет его: так конфигурация попадает в сервис —
// опечатка должна выясняться до первой операции.
func Load(path string) (Configuration, string, error) {
	cfg, resolved, err := Read(path)
	if err != nil {
		return Configuration{}, resolved, err
	}
	if err := cfg.Validate(); err != nil {
		return Configuration{}, resolved, fmt.Errorf("конфиг %s: %w", resolved, err)
	}
	return cfg, resolved, nil
}

// Read читает конфиг, не проверяя его. Проверку выполняет вызывающий код:
// `sqlbrc doctor` показывает замечания к конфигу отдельной строкой отчёта,
// а не завершается одним сообщением об ошибке до прогона проверок.
func Read(path string) (Configuration, string, error) {
	resolved, err := ResolvePath(path)
	if err != nil {
		return Configuration{}, "", err
	}
	cfg := Default()
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return Configuration{}, resolved, fmt.Errorf(
			"не удалось прочитать конфиг %s: %w (образец: deploy/config.example.yaml)", resolved, err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Configuration{}, resolved, fmt.Errorf("не удалось разобрать %s: %w", resolved, err)
	}
	// Файл без версии схемы (или пустой) читается по текущей схеме: иначе
	// минимальный конфиг вида "storage: {dir: ...}" не запускался бы.
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = SchemaVersion
	}
	if err := ApplyEnv(&cfg, os.LookupEnv); err != nil {
		return Configuration{}, resolved, err
	}
	return cfg, resolved, nil
}

// ResolvePath определяет, какой файл читать: аргумент командной строки, затем
// $SQLBRC_CONFIG, затем путь по умолчанию. Отсутствие файла — ошибка: сервис
// без конфига работать не должен, а подсказка указывает на образец.
func ResolvePath(path string) (string, error) {
	resolved := strings.TrimSpace(path)
	explicit := resolved != ""
	if !explicit {
		resolved = strings.TrimSpace(os.Getenv(EnvConfigPath))
	}
	if resolved == "" {
		resolved = DefaultConfigPath
	}
	if _, err := os.Stat(resolved); err != nil {
		// Подсказка указывает на образец и на способ задать другой путь: без неё
		// первая установка упирается в «конфиг не найден» без вариантов действий.
		hint := fmt.Sprintf("скопируйте образец: install -m 0640 deploy/config.example.yaml %s", resolved)
		if !explicit {
			hint = fmt.Sprintf("%s; путь задаётся флагом --config или переменной %s", hint, EnvConfigPath)
		}
		return "", fmt.Errorf("конфиг %s не найден: %w (%s)", resolved, err, hint)
	}
	return resolved, nil
}

// envOverride — одна переменная окружения, переопределяющая поле конфига.
type envOverride struct {
	name  string
	apply func(*Configuration, string) error
}

// envOverrides — поддерживаемые переменные окружения. Список фиксирован:
// видно, какие настройки меняются без правки файла (например, из unit-файла
// systemd), и нельзя случайно переопределить поле, для которого подстановки нет.
var envOverrides = []envOverride{
	{"SQLBRC_SERVER_LISTEN", func(c *Configuration, v string) error { c.Server.Listen = v; return nil }},
	{"SQLBRC_SERVER_READ_ONLY", func(c *Configuration, v string) error { return setBool(&c.Server.ReadOnly, v) }},
	{"SQLBRC_SERVER_ALLOW_INSECURE", func(c *Configuration, v string) error { return setBool(&c.Server.AllowInsecure, v) }},
	{"SQLBRC_SERVER_TLS_CERT_FILE", func(c *Configuration, v string) error { c.Server.TLS.CertFile = v; return nil }},
	{"SQLBRC_SERVER_TLS_KEY_FILE", func(c *Configuration, v string) error { c.Server.TLS.KeyFile = v; return nil }},
	{"SQLBRC_AUTH_TOKEN_FILE", func(c *Configuration, v string) error { c.Auth.TokenFile = v; return nil }},
	{"SQLBRC_POSTGRES_MODE", func(c *Configuration, v string) error { c.Postgres.Mode = v; return nil }},
	{"SQLBRC_POSTGRES_SUDO_USER", func(c *Configuration, v string) error { c.Postgres.SudoUser = v; return nil }},
	{"SQLBRC_POSTGRES_ROLE", func(c *Configuration, v string) error { c.Postgres.Role = v; return nil }},
	{"SQLBRC_POSTGRES_PASSWORD_FILE", func(c *Configuration, v string) error { c.Postgres.PasswordFile = v; return nil }},
	{"SQLBRC_POSTGRES_BIN_DIR", func(c *Configuration, v string) error { c.Postgres.BinDir = v; return nil }},
	{"SQLBRC_STORAGE_DIR", func(c *Configuration, v string) error { c.Storage.Dir = v; return nil }},
	{"SQLBRC_STORAGE_STATE_DIR", func(c *Configuration, v string) error { c.Storage.StateDir = v; return nil }},
	{"SQLBRC_STORAGE_KEEP_LAST", func(c *Configuration, v string) error { return setInt(&c.Storage.KeepLast, v) }},
	{"SQLBRC_STORAGE_KEEP_DAYS", func(c *Configuration, v string) error { return setInt(&c.Storage.KeepDays, v) }},
	{"SQLBRC_STORAGE_MIN_FREE_SPACE", func(c *Configuration, v string) error {
		return setSize(&c.Storage.MinFreeSpace, v)
	}},
	{"SQLBRC_DATABASES_PATTERN", func(c *Configuration, v string) error { c.Databases.Pattern = v; return nil }},
	{"SQLBRC_DATABASES_OWNER", func(c *Configuration, v string) error { c.Databases.Owner = v; return nil }},
	{"SQLBRC_LOG_LEVEL", func(c *Configuration, v string) error { c.Logging.Level = v; return nil }},
	{"SQLBRC_LOG_FORMAT", func(c *Configuration, v string) error { c.Logging.Format = v; return nil }},
}

// EnvNames возвращает имена поддерживаемых переменных окружения по алфавиту:
// список печатает справка `sqlbrc doctor` и проверяют тесты.
func EnvNames() []string {
	names := make([]string, 0, len(envOverrides))
	for _, override := range envOverrides {
		names = append(names, override.name)
	}
	sort.Strings(names)
	return names
}

// ApplyEnv накладывает переменные окружения поверх конфигурации. lookup
// подменяется в тестах: менять окружение процесса для этого не нужно.
func ApplyEnv(cfg *Configuration, lookup func(string) (string, bool)) error {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	for _, override := range envOverrides {
		value, ok := lookup(override.name)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			continue
		}
		if err := override.apply(cfg, value); err != nil {
			return fmt.Errorf("переменная %s=%q: %w", override.name, value, err)
		}
	}
	return nil
}

// setBool разбирает булево значение переменной окружения.
func setBool(target *bool, value string) error {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("ожидалось true или false: %w", err)
	}
	*target = parsed
	return nil
}

// setInt разбирает целое значение переменной окружения.
func setInt(target *int, value string) error {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("ожидалось целое число: %w", err)
	}
	*target = parsed
	return nil
}

// setSize разбирает размер вроде "5GB".
func setSize(target *bytesize.Size, value string) error {
	parsed, err := bytesize.ParseSize(value)
	if err != nil {
		return err
	}
	*target = bytesize.Size(parsed)
	return nil
}
