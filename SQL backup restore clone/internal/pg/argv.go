package pg

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
)

// Режимы запуска утилит PostgreSQL.
const (
	// ModeSudo — утилиты выполняются через sudo -n -u <SudoUser>, подключение идёт
	// через локальный сокет с peer-аутентификацией. Пароли не нужны, поэтому
	// в конфиге и в командной строке их нет.
	ModeSudo = "sudo"
	// ModeTCP — прямое подключение по сети с паролем роли (PGPASSFILE).
	// Нужно поставке в контейнере: локального сокета и peer-аутентификации там нет.
	ModeTCP = "tcp"
)

// Форматы дампа pg_dump, которые поддерживает сервис. 0.1.0 использует только
// custom: он сжимается и восстанавливается параллельно.
const (
	FormatCustom = "custom"
	FormatPlain  = "plain"
)

// DefaultCompression — уровень сжатия zlib для custom-формата. В PostgreSQL 15
// pg_dump умеет только zlib; при передаче уровня вызывающий код обязан задать его
// явно, поэтому значение продублировано здесь как ориентир для сервиса.
const DefaultCompression = 6

// Tools — утилиты PostgreSQL, запуск которых разрешён. Список фиксирован:
// имя утилиты подставляется в путь, и произвольный бинарь запустить нельзя.
var Tools = []string{"pg_dump", "pg_restore", "psql", "pg_isready"}

// Prefix описывает способ запуска утилит PostgreSQL.
type Prefix struct {
	// Mode — ModeSudo или ModeTCP.
	Mode string
	// SudoPath — путь к sudo в Debian (/usr/bin/sudo).
	SudoPath string
	// SudoUser — пользователь, под которым выполняются утилиты (postgres).
	SudoUser string
	// BinDir — каталог утилит PostgreSQL (/usr/bin: pg_dump -> pg_wrapper).
	BinDir string
}

// Validate проверяет, что способ запуска задан полностью.
func (p Prefix) Validate() error {
	if p.BinDir == "" {
		return errors.New("не задан каталог утилит PostgreSQL (postgres.bin_dir)")
	}
	switch p.Mode {
	case ModeSudo:
		if p.SudoPath == "" {
			return errors.New("не задан путь к sudo (postgres.sudo_path)")
		}
		if p.SudoUser == "" {
			return errors.New("не задан пользователь для sudo (postgres.sudo_user)")
		}
		return ValidateIdentifier(p.SudoUser, "postgres.sudo_user")
	case ModeTCP:
		return nil
	default:
		return fmt.Errorf("неизвестный режим запуска утилит %q (ожидается %q или %q)", p.Mode, ModeSudo, ModeTCP)
	}
}

// Binary возвращает полный путь к утилите PostgreSQL. В Debian /usr/bin/pg_dump —
// символическая ссылка на pg_wrapper, поэтому путь задаётся явно: под sudo
// переменная PATH пользователя postgres не используется. Разделитель пути — «/»:
// это путь на сервере Linux, и на Windows-разработке он должен выглядеть так же.
func (p Prefix) Binary(tool string) (string, error) {
	if err := ValidateTool(tool); err != nil {
		return "", err
	}
	return path.Join(p.BinDir, tool), nil
}

// Wrap возвращает имя процесса и аргументы перед аргументами утилиты:
// в режиме sudo это «sudo -n -u postgres <путь>». Флаг -n обязателен: без него
// sudo будет ждать пароль и задача зависнет вместо понятной ошибки.
func (p Prefix) Wrap(binary string) (string, []string) {
	if p.Mode == ModeSudo {
		return p.SudoPath, []string{"-n", "-u", p.SudoUser, binary}
	}
	return binary, nil
}

// ConnectionArgs возвращает аргументы подключения к серверу. В режиме sudo они
// не нужны: подключение идёт через локальный сокет, а -h/-p/-U подменяли бы
// peer-аутентификацию на TCP и требовали бы пароль.
func (p Prefix) ConnectionArgs(conn ConnConfig) []string {
	if p.Mode != ModeTCP {
		return nil
	}
	args := make([]string, 0, 6)
	if conn.Host != "" {
		args = append(args, "--host", conn.Host)
	}
	if conn.Port != "" {
		args = append(args, "--port", conn.Port)
	}
	if conn.Role != "" {
		args = append(args, "--username", conn.Role)
	}
	return args
}

// ConnConfig — параметры сетевого подключения (режим ModeTCP).
type ConnConfig struct {
	Host string
	Port string
	Role string
}

// ValidateTool проверяет, что запрошена известная утилита PostgreSQL.
func ValidateTool(name string) error {
	for _, tool := range Tools {
		if tool == name {
			return nil
		}
	}
	return fmt.Errorf("неизвестная утилита %q (разрешены: %s)", name, strings.Join(Tools, ", "))
}

// ValidateIdentifier проверяет идентификатор PostgreSQL (роль, БД): буквы латиницы,
// цифры, подчёркивание, длина до 63 символов.
func ValidateIdentifier(value, field string) error {
	if value == "" {
		return fmt.Errorf("%s: пустое значение", field)
	}
	if len(value) > MaxIdentifierLength {
		return fmt.Errorf("%s: значение %q длиннее %d символов", field, value, MaxIdentifierLength)
	}
	for index, r := range value {
		ok := r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(index > 0 && r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("%s: недопустимый символ %q в значении %q", field, r, value)
		}
	}
	return nil
}

// DumpArgs — параметры pg_dump.
type DumpArgs struct {
	// DB — имя базы данных.
	DB string
	// Pattern — шаблон имён БД из databases.pattern, то есть какие базы сервис
	// считает своими. Пустое значение означает DefaultAllowPattern; шаблон из
	// конфига передаётся явно, иначе сужение шаблона не влияло бы на запуск.
	Pattern string
	// Connection — аргументы подключения из Prefix.ConnectionArgs.
	Connection []string
	// Format — формат архива (по умолчанию FormatCustom).
	Format string
	// Compression — уровень сжатия 0..9.
	Compression int
	// NoOwner — не записывать команды смены владельца объектов.
	NoOwner bool
	// NoPrivileges — не выгружать GRANT/REVOKE.
	NoPrivileges bool
	// NoACL — не выгружать ACL.
	NoACL bool
	// Verbose — подробный вывод pg_dump в журнал задачи.
	Verbose bool
	// Extra — дополнительные аргументы из CLI (флаг --pg-dump-arg).
	Extra []string
}

// ValidateFormat проверяет формат архива.
func ValidateFormat(format string) error {
	switch format {
	case FormatCustom, FormatPlain:
		return nil
	default:
		return fmt.Errorf("неизвестный формат дампа %q (поддерживается %s)", format, FormatCustom)
	}
}

// ValidateCompression проверяет уровень сжатия.
func ValidateCompression(level int) error {
	if level < 0 || level > 9 {
		return fmt.Errorf("уровень сжатия %d вне диапазона 0..9", level)
	}
	return nil
}

// dumpForbiddenArgs — аргументы, которые sqlbrc формирует сам. Если передать их
// в --pg-dump-arg, файл или подключение окажутся вне контроля сервиса.
var dumpForbiddenArgs = []string{
	"--file", "-f", "--dbname", "-d", "--host", "-h", "--port", "-p", "--username", "-U",
}

// restoreForbiddenArgs — то же для pg_restore.
var restoreForbiddenArgs = []string{
	"--file", "-f", "--dbname", "-d", "--host", "-h", "--port", "-p", "--username", "-U",
}

// DumpArgv формирует аргументы pg_dump: подключение, формат, сжатие и имя БД
// передаются отдельными элементами массива. Имя БД проверяется по шаблону
// a.Pattern (пусто — DefaultAllowPattern), поэтому суженный в конфиге шаблон
// действует и здесь.
func DumpArgv(a DumpArgs) ([]string, error) {
	if err := ValidateDBName(a.DB, a.Pattern); err != nil {
		return nil, err
	}
	format := a.Format
	if format == "" {
		format = FormatCustom
	}
	if err := ValidateFormat(format); err != nil {
		return nil, err
	}
	if err := ValidateCompression(a.Compression); err != nil {
		return nil, err
	}
	if err := validateExtraArgs(a.Extra, dumpForbiddenArgs, "pg_dump"); err != nil {
		return nil, err
	}

	args := make([]string, 0, 12+len(a.Connection)+len(a.Extra))
	args = append(args, a.Connection...)
	args = append(args, "--format", format, "--compress", strconv.Itoa(a.Compression))
	if a.Verbose {
		args = append(args, "--verbose")
	}
	// Владельцы объектов переносятся в дамп: при восстановлении объекты снова
	// принадлежат владельцу из дампа, а не пользователю, под которым работает сервис.
	if a.NoOwner {
		args = append(args, "--no-owner")
	}
	if a.NoPrivileges {
		args = append(args, "--no-privileges")
	}
	if a.NoACL {
		args = append(args, "--no-acl")
	}
	args = append(args, a.Extra...)
	args = append(args, "--dbname", a.DB)
	return args, nil
}

// RestoreArgs — параметры pg_restore.
type RestoreArgs struct {
	// DB — имя базы данных для восстановления.
	DB string
	// Pattern — шаблон имён БД из databases.pattern (пусто — DefaultAllowPattern):
	// от него зависит, какую базу сервис считает своей и потому вправе очистить.
	Pattern string
	// Archive — путь к архиву. Для списка объектов (ListOnly) архив обязателен,
	// при восстановлении дамп читается из stdin, чтобы не дублировать файл.
	Archive string
	// Connection — аргументы подключения из Prefix.ConnectionArgs.
	Connection []string
	// Jobs — число параллельных процессов (0 или 1 — без параллелизма).
	Jobs int
	// ListOnly — вместо восстановления вывести список объектов архива.
	ListOnly bool
	// ExitOnError — останавливаться на первой ошибке вместо накопления предупреждений.
	ExitOnError bool
	// Clean — удалить объекты перед восстановлением (режим clean).
	Clean bool
	// IfExists — не считать ошибкой отсутствие объекта при Clean.
	IfExists bool
	// NoOwner — не восстанавливать владельцев объектов. По умолчанию false:
	// владельцы берутся из дампа, поэтому объекты остаются за ролями из архива
	// (в режиме sudo перенос чужих владельцев доступен только суперпользователю).
	NoOwner bool
	// Verbose — подробный вывод в журнал задачи.
	Verbose bool
	// Extra — дополнительные аргументы из CLI (флаг --pg-restore-arg).
	Extra []string
}

// RestoreArgv формирует аргументы pg_restore. Имя БД проверяется по шаблону
// a.Pattern (пусто — DefaultAllowPattern): сужение шаблона в конфиге действует
// и на восстановление.
func RestoreArgv(a RestoreArgs) ([]string, error) {
	if a.ListOnly {
		if strings.TrimSpace(a.Archive) == "" {
			return nil, errors.New("для списка объектов архива нужен путь к файлу")
		}
	} else if err := ValidateDBName(a.DB, a.Pattern); err != nil {
		return nil, err
	}
	if a.Jobs < 0 || a.Jobs > 32 {
		return nil, fmt.Errorf("число параллельных процессов %d вне диапазона 0..32", a.Jobs)
	}
	if err := validateExtraArgs(a.Extra, restoreForbiddenArgs, "pg_restore"); err != nil {
		return nil, err
	}

	args := make([]string, 0, 12+len(a.Connection)+len(a.Extra))
	if a.ListOnly {
		args = append(args, "--list")
	}
	args = append(args, a.Connection...)
	if a.Jobs > 1 {
		args = append(args, "--jobs", strconv.Itoa(a.Jobs))
	}
	if a.Clean {
		args = append(args, "--clean")
		if a.IfExists {
			args = append(args, "--if-exists")
		}
	}
	if a.ExitOnError {
		args = append(args, "--exit-on-error")
	}
	if a.NoOwner {
		args = append(args, "--no-owner")
	}
	if a.Verbose {
		args = append(args, "--verbose")
	}
	args = append(args, a.Extra...)
	if a.DB != "" {
		args = append(args, "--dbname", a.DB)
	}
	if a.Archive != "" {
		args = append(args, a.Archive)
	}
	return args, nil
}

// PSQLArgs — параметры psql для служебных запросов к каталогу.
type PSQLArgs struct {
	// Database — БД, в которой выполняется запрос.
	Database string
	// SQL — текст запроса.
	SQL string
	// Connection — аргументы подключения из Prefix.ConnectionArgs.
	Connection []string
	// FieldSeparator — разделитель полей (один символ).
	FieldSeparator string
	// Extra — дополнительные аргументы из CLI.
	Extra []string
}

// PSQLArgv формирует аргументы psql. Ключи обязательны: -X отключает ~/.psqlrc,
// а -v ON_ERROR_STOP=1 не даёт psql продолжить работу после ошибки, иначе
// неполный ответ сервера выглядел бы как успешный запрос.
func PSQLArgv(a PSQLArgs) ([]string, error) {
	if err := ValidateIdentifier(a.Database, "psql.database"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.SQL) == "" {
		return nil, errors.New("пустой запрос к PostgreSQL")
	}
	if err := ValidateFieldSeparator(a.FieldSeparator); err != nil {
		return nil, err
	}
	if err := validateExtraArgs(a.Extra, dumpForbiddenArgs, "psql"); err != nil {
		return nil, err
	}

	args := make([]string, 0, 14+len(a.Connection)+len(a.Extra))
	args = append(args,
		"--no-psqlrc", "--quiet", "--no-align", "--tuples-only",
		"--field-separator", a.FieldSeparator,
		"--set", "ON_ERROR_STOP=1",
	)
	args = append(args, a.Connection...)
	args = append(args, a.Extra...)
	args = append(args, "--dbname", a.Database, "--command", a.SQL)
	return args, nil
}

// ValidateFieldSeparator проверяет разделитель полей psql: он должен быть одним
// символом, иначе разбор строки результата становится неоднозначным.
func ValidateFieldSeparator(separator string) error {
	if len([]rune(separator)) != 1 {
		return fmt.Errorf("разделитель полей %q должен состоять из одного символа", separator)
	}
	return nil
}

// validateExtraArgs не даёт переопределить аргументы, которые формирует сервис:
// файл, подключение и имя БД должны оставаться под его контролем.
func validateExtraArgs(extra []string, forbidden []string, tool string) error {
	for _, arg := range extra {
		name := arg
		if index := strings.IndexByte(name, '='); index >= 0 {
			name = name[:index]
		}
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("пустой аргумент в списке дополнительных аргументов %s", tool)
		}
		for _, blocked := range forbidden {
			if name == blocked {
				return fmt.Errorf(
					"аргумент %q задаётся сервисом: %s подключается к БД и пишет дамп сам", arg, tool)
			}
		}
	}
	return nil
}
