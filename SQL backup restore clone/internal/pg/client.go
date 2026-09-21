package pg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Значения по умолчанию для Debian 12 с PostgreSQL 15 из штатного репозитория.
const (
	// DefaultSudoPath — путь к sudo.
	DefaultSudoPath = "/usr/bin/sudo"
	// DefaultSudoUser — пользователь-владелец кластера PostgreSQL.
	DefaultSudoUser = "postgres"
	// DefaultBinDir — каталог утилит PostgreSQL: /usr/bin/pg_dump -> pg_wrapper.
	DefaultBinDir = "/usr/bin"
	// DefaultDatabase — служебная БД для запросов к каталогу.
	DefaultDatabase = "postgres"
	// DefaultJobs — параллелизм pg_restore по умолчанию.
	DefaultJobs = 4
	// DefaultTimeout — предел времени на одну операцию: большой дамп идёт минутами,
	// но зависшая команда не должна держать задачу вечно.
	DefaultTimeout = 2 * time.Hour
)

// Config — параметры запуска утилит PostgreSQL и подключения к серверу.
type Config struct {
	// Mode — ModeSudo или ModeTCP.
	Mode string
	// SudoPath, SudoUser — способ запуска в режиме sudo.
	SudoPath string
	SudoUser string
	// BinDir — каталог утилит PostgreSQL.
	BinDir string
	// Host, Port, Role — параметры сетевого подключения (режим ModeTCP).
	Host string
	Port string
	Role string
	// Database — БД для служебных запросов.
	Database string
	// PasswordFile — файл с паролем роли (режим ModeTCP), передаётся через PGPASSFILE.
	PasswordFile string
	// Jobs — число параллельных процессов pg_restore.
	Jobs int
	// Timeout — предел времени на одну операцию.
	Timeout time.Duration
}

// WithDefaults подставляет значения по умолчанию: конфиг сервиса заполняет эти
// поля не всегда, а argv утилит собирается полностью.
func (c Config) WithDefaults() Config {
	if c.Mode == "" {
		c.Mode = ModeSudo
	}
	if c.SudoPath == "" {
		c.SudoPath = DefaultSudoPath
	}
	if c.SudoUser == "" {
		c.SudoUser = DefaultSudoUser
	}
	if c.BinDir == "" {
		c.BinDir = DefaultBinDir
	}
	if c.Database == "" {
		c.Database = DefaultDatabase
	}
	if c.Jobs <= 0 {
		c.Jobs = DefaultJobs
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	return c
}

// Prefix возвращает способ запуска утилит, соответствующий настройкам.
func (c Config) Prefix() Prefix {
	return Prefix{Mode: c.Mode, SudoPath: c.SudoPath, SudoUser: c.SudoUser, BinDir: c.BinDir}
}

// connConfig возвращает параметры сетевого подключения.
func (c Config) connConfig() ConnConfig {
	return ConnConfig{Host: c.Host, Port: c.Port, Role: c.Role}
}

// Validate проверяет сочетания параметров до первого запуска команды: ошибка
// в конфиге должна выясняться при старте, а не в момент восстановления бэкапа.
func (c Config) Validate() error {
	cfg := c.WithDefaults()
	if err := cfg.Prefix().Validate(); err != nil {
		return err
	}
	if err := ValidateIdentifier(cfg.Database, "postgres.database"); err != nil {
		return err
	}
	switch cfg.Mode {
	case ModeSudo:
		// sudo сбрасывает окружение, поэтому PGPASSFILE до postgres не дойдёт,
		// а peer-аутентификация пароль не спрашивает: такая пара настроек не работает.
		if cfg.PasswordFile != "" {
			return errors.New(
				"postgres.password_file не используется в режиме sudo: подключение идёт через локальный сокет с peer-аутентификацией")
		}
	case ModeTCP:
		if cfg.PasswordFile == "" && cfg.Role != "" && cfg.Role != cfg.SudoUser {
			return errors.New("postgres.password_file обязателен, когда роль отличается от суперпользователя")
		}
	}
	if cfg.Jobs < 1 || cfg.Jobs > 32 {
		return fmt.Errorf("postgres.jobs = %d вне диапазона 1..32", cfg.Jobs)
	}
	if cfg.Timeout < time.Minute || cfg.Timeout > 24*time.Hour {
		return fmt.Errorf("postgres.timeout = %s вне диапазона 1m..24h", cfg.Timeout)
	}
	return nil
}

// Role — сведения о роли, под которой сервис работает с сервером.
type Role struct {
	Name string
	// Superuser — роль является суперпользователем.
	Superuser bool
	// CreateDB — роль может создавать базы данных.
	CreateDB bool
}

// Database — база данных кластера.
type Database struct {
	Name        string
	Owner       string
	Encoding    string
	Collate     string
	Ctype       string
	Tablespace  string
	SizeBytes   int64
	Connections int
	AllowConn   bool
	IsTemplate  bool
}

// SQL — служебные запросы к каталогу PostgreSQL. Список БД и роль берутся из
// системных каталогов, поэтому сервис не зависит от состояния своей схемы.
const (
	// SQLServerVersion возвращает версию сервера.
	SQLServerVersion = "SHOW server_version"
	// SQLRoleInfo возвращает роль и её права на создание БД.
	SQLRoleInfo = `SELECT current_user, rolsuper, rolcreatedb FROM pg_roles WHERE rolname = current_user`
	// SQLListDatabases возвращает базы, их владельцев, кодировку, локаль, размер
	// и число активных подключений. numbackends виден всем ролям, в отличие от pg_stat_activity.
	SQLListDatabases = `SELECT d.datname, pg_get_userbyid(d.datdba), pg_encoding_to_char(d.encoding),
       d.datcollate, d.datctype, COALESCE(t.spcname, 'pg_default'),
       pg_database_size(d.datname), d.datallowconn, d.datistemplate,
       COALESCE(s.numbackends, 0)
FROM pg_database d
LEFT JOIN pg_tablespace t ON t.oid = d.dbtablespace
LEFT JOIN pg_stat_database s ON s.datname = d.datname
ORDER BY d.datname`
)

// Client — обёртка над утилитами PostgreSQL.
type Client struct {
	cfg    Config
	runner CommandRunner
	log    io.Writer
}

// NewClient создаёт клиент, подставляя значения по умолчанию. Лог нужен для
// журнала задач: в него дублируется stderr утилит; nil отключает вывод.
func NewClient(cfg Config, runner CommandRunner, log io.Writer) *Client {
	cfg = cfg.WithDefaults()
	if runner == nil {
		runner = ExecRunner{Env: passwordEnv(cfg.PasswordFile)}
	}
	return &Client{cfg: cfg, runner: runner, log: log}
}

// Config возвращает настройки клиента с подставленными значениями по умолчанию.
func (c *Client) Config() Config { return c.cfg }

// Prefix возвращает способ запуска утилит: нужен проверкам окружения и
// формированию argv в сервисе бэкапов.
func (c *Client) Prefix() Prefix { return c.cfg.Prefix() }

// BinaryPath возвращает путь к утилите PostgreSQL и проверяет, что она доступна.
// Ошибка объясняет, какого пакета не хватает: без postgresql-client-15 сервис
// не заработает, и это должно быть видно из сообщения, а не из кода 127.
func (c *Client) BinaryPath(tool string) (string, error) {
	path, err := c.cfg.Prefix().Binary(tool)
	if err != nil {
		return "", err
	}
	if _, err := c.runner.LookPath(path); err != nil {
		return "", fmt.Errorf(
			"%s не найден: %w (в Debian 12 файлы даёт пакет postgresql-client-15)", path, err)
	}
	return path, nil
}

// Exec выполняет утилиту PostgreSQL, подключая потоки ввода-вывода. Метод
// используется для дампа (stdout — файл архива) и восстановления (stdin — архив).
func (c *Client) Exec(ctx context.Context, tool string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	path, err := c.BinaryPath(tool)
	if err != nil {
		return err
	}
	process, prefixArgs := c.cfg.Prefix().Wrap(path)
	if stdout == nil {
		stdout = io.Discard
	}
	switch {
	case stderr == nil:
		stderr = c.log
	case c.log != nil:
		// Вывод команды дублируется в журнал задачи: по нему видно причину сбоя.
		stderr = io.MultiWriter(stderr, c.log)
	}
	if stderr == nil {
		stderr = io.Discard
	}
	runCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	full := make([]string, 0, len(prefixArgs)+len(args))
	full = append(full, prefixArgs...)
	full = append(full, args...)
	return c.runner.Run(runCtx, process, full, stdin, stdout, stderr)
}

// output выполняет утилиту и возвращает её stdout.
func (c *Client) output(ctx context.Context, tool string, args []string) ([]byte, error) {
	path, err := c.BinaryPath(tool)
	if err != nil {
		return nil, err
	}
	process, prefixArgs := c.cfg.Prefix().Wrap(path)
	runCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	full := make([]string, 0, len(prefixArgs)+len(args))
	full = append(full, prefixArgs...)
	full = append(full, args...)
	return c.runner.Output(runCtx, process, full)
}

// passwordEnv возвращает переменные окружения для аутентификации по паролю.
// Пароль передаётся через PGPASSFILE: в командной строке его нет, поэтому он
// не виден ни в списке процессов, ни в журнале задач.
func passwordEnv(passwordFile string) []string {
	if passwordFile == "" {
		return nil
	}
	return []string{"PGPASSFILE=" + passwordFile}
}

// Query выполняет запрос к служебной БД и возвращает строки результата.
func (c *Client) Query(ctx context.Context, sql string) ([]Row, error) {
	args, err := PSQLArgv(PSQLArgs{
		Database:       c.cfg.Database,
		SQL:            sql,
		Connection:     c.cfg.Prefix().ConnectionArgs(c.cfg.connConfig()),
		FieldSeparator: FieldSeparator,
	})
	if err != nil {
		return nil, err
	}
	out, err := c.output(ctx, "psql", args)
	if err != nil {
		return nil, err
	}
	return ParseTuples(out)
}

// Ping проверяет доступность сервера: pg_isready не подключается к БД и не
// требует пароля, поэтому проверка проходит даже когда роль войти не может.
func (c *Client) Ping(ctx context.Context) error {
	args := []string{"--quiet", "--timeout", "10", "--dbname", c.cfg.Database}
	args = append(args, c.cfg.Prefix().ConnectionArgs(c.cfg.connConfig())...)
	if _, err := c.output(ctx, "pg_isready", args); err != nil {
		return fmt.Errorf("сервер PostgreSQL недоступен: %w", err)
	}
	return nil
}

// ServerVersion возвращает версию сервера.
func (c *Client) ServerVersion(ctx context.Context) (Version, error) {
	rows, err := c.Query(ctx, SQLServerVersion)
	if err != nil {
		return Version{}, fmt.Errorf("не удалось получить версию сервера: %w", err)
	}
	if len(rows) == 0 {
		return Version{}, errors.New("сервер не вернул версию")
	}
	return ParsePgVersion([]byte(rows[0].Field(0)))
}

// ClientVersions возвращает версии утилит PostgreSQL. Отсутствие любой из них —
// ошибка: без pg_dump и pg_restore сервис не работает, а расхождение версий
// клиента и сервера ломает восстановление.
func (c *Client) ClientVersions(ctx context.Context) (map[string]Version, error) {
	versions := make(map[string]Version, len(Tools))
	problems := make([]string, 0, len(Tools))
	for _, tool := range Tools {
		out, err := c.output(ctx, tool, []string{"--version"})
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		version, err := ParsePgVersion(out)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", tool, err))
			continue
		}
		versions[tool] = version
	}
	if len(problems) > 0 {
		return versions, errors.New(strings.Join(problems, "; "))
	}
	return versions, nil
}

// RoleInfo возвращает роль, под которой сервис работает с сервером, и её права.
func (c *Client) RoleInfo(ctx context.Context) (Role, error) {
	rows, err := c.Query(ctx, SQLRoleInfo)
	if err != nil {
		return Role{}, err
	}
	if len(rows) == 0 {
		return Role{}, errors.New("сервер не вернул сведения о роли")
	}
	row := rows[0]
	return Role{
		Name:      row.Field(0),
		Superuser: parseBool(row.Field(1)),
		CreateDB:  parseBool(row.Field(2)),
	}, nil
}

// ListDatabases возвращает базы данных кластера: владельца, кодировку, локаль,
// размер и число активных подключений.
func (c *Client) ListDatabases(ctx context.Context) ([]Database, error) {
	rows, err := c.Query(ctx, SQLListDatabases)
	if err != nil {
		return nil, err
	}
	databases := make([]Database, 0, len(rows))
	for _, row := range rows {
		if row.Field(0) == "" {
			continue
		}
		databases = append(databases, Database{
			Name:        row.Field(0),
			Owner:       row.Field(1),
			Encoding:    row.Field(2),
			Collate:     row.Field(3),
			Ctype:       row.Field(4),
			Tablespace:  row.Field(5),
			SizeBytes:   parseInt64(row.Field(6)),
			AllowConn:   parseBool(row.Field(7)),
			IsTemplate:  parseBool(row.Field(8)),
			Connections: int(parseInt64(row.Field(9))),
		})
	}
	return databases, nil
}

// ListArchive читает оглавление архива: сервер для этого не нужен, поэтому
// проверка архива проходит даже когда PostgreSQL недоступен.
func (c *Client) ListArchive(ctx context.Context, archive string) ([]TOCEntry, error) {
	args, err := RestoreArgv(RestoreArgs{
		ListOnly:   true,
		Archive:    archive,
		Connection: c.cfg.Prefix().ConnectionArgs(c.cfg.connConfig()),
	})
	if err != nil {
		return nil, err
	}
	out, err := c.output(ctx, "pg_restore", args)
	if err != nil {
		return nil, err
	}
	return ParseRestoreList(out)
}

// parseBool разбирает значение из psql: булевы поля приходят как t/f.
func parseBool(value string) bool {
	switch strings.TrimSpace(value) {
	case "t", "true", "on", "1", "yes":
		return true
	default:
		return false
	}
}

// parseInt64 переводит значение в число; мусор трактуется как 0, потому что
// размер БД и число подключений — справочные поля.
func parseInt64(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}
