// Package config читает конфигурацию сервиса sqlbrc: YAML-файл плюс переменные
// окружения SQLBRC_*. Настройки проверяются до запуска: опечатка в шаблоне имён
// БД или в пути к бэкапам должна выясняться при старте и в `sqlbrc doctor`,
// а не в момент восстановления бэкапа.
package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// DefaultConfigPath — путь к конфигу при установке по инструкции (см. deploy/).
const DefaultConfigPath = "/etc/sqlbrc/config.yaml"

// EnvConfigPath — переменная окружения с путём к конфигу.
const EnvConfigPath = "SQLBRC_CONFIG"

// SchemaVersion — версия схемы конфига. Несовместимое изменение схемы её
// увеличивает, а старый файл отвергается с понятным сообщением вместо
// молчаливой работы с неполными настройками.
const SchemaVersion = 1

// Уровни и форматы журнала.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"

	FormatText = "text"
	FormatJSON = "json"
)

// Configuration — корень конфигурации сервиса.
type Configuration struct {
	SchemaVersion int             `yaml:"schema_version"`
	Server        ServerConfig    `yaml:"server"`
	Auth          AuthConfig      `yaml:"auth"`
	Postgres      PostgresConfig  `yaml:"postgres"`
	Storage       StorageConfig   `yaml:"storage"`
	Databases     DatabasesConfig `yaml:"databases"`
	Jobs          JobsConfig      `yaml:"jobs"`
	Logging       LoggingConfig   `yaml:"logging"`
}

// ServerConfig — веб-интерфейс и API.
type ServerConfig struct {
	// Listen — адрес и порт веб-интерфейса, например "0.0.0.0:8088".
	Listen string `yaml:"listen"`
	// TLS — сертификат и ключ. По умолчанию их нет: интерфейс слушает LAN
	// без reverse-proxy, поэтому при доступе извне нужен TLS или nftables.
	TLS TLSConfig `yaml:"tls"`
	// AllowInsecure — осознанное разрешение слушать не loopback без TLS.
	AllowInsecure bool `yaml:"allow_insecure"`
	// ReadOnly — режим «только просмотр»: операции изменения запрещены.
	ReadOnly bool `yaml:"read_only"`
	// MaxBodyBytes — предел размера запроса: формы сервиса больших тел не передают.
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
	// PublicURL — адрес сервиса для ссылок в уведомлениях.
	PublicURL string `yaml:"public_url"`
	// TrustedProxies — адреса прокси, от которых принимается X-Forwarded-For.
	TrustedProxies []string `yaml:"trusted_proxies"`
}

// TLSConfig — пути к сертификату и ключу.
type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// Enabled отвечает, настроен ли TLS.
func (t TLSConfig) Enabled() bool { return t.CertFile != "" && t.KeyFile != "" }

// AuthConfig — встроенные пользователи веб-интерфейса.
type AuthConfig struct {
	// Users — пользователи с bcrypt-хэшами паролей. Открытые пароли в конфиге
	// не хранятся: файл читают администраторы, и он попадает в бэкап системы.
	Users []User `yaml:"users"`
	// TokenFile — файл с токенами для API (по одному токену в строке).
	TokenFile string `yaml:"token_file"`
	// SessionTTL — время жизни сессии.
	SessionTTL Duration `yaml:"session_ttl"`
	// LoginBackoff — пауза после неудачного входа.
	LoginBackoff Duration `yaml:"login_backoff"`
	// MaxLoginAttempts — число неудачных попыток до временной блокировки входа.
	MaxLoginAttempts int `yaml:"max_login_attempts"`
}

// User — встроенный пользователь веб-интерфейса.
type User struct {
	// Login — имя пользователя.
	Login string `yaml:"login"`
	// PasswordBcrypt — хэш пароля, созданный `sqlbrc hash-password`.
	PasswordBcrypt string `yaml:"password_bcrypt"`
}

// PostgresConfig — доступ к PostgreSQL.
type PostgresConfig struct {
	// Mode — способ запуска утилит: sudo (0.1.0) или tcp (0.2.0).
	Mode string `yaml:"mode"`
	// SudoPath, SudoUser — путь к sudo и пользователь-владелец кластера.
	SudoPath string `yaml:"sudo_path"`
	SudoUser string `yaml:"sudo_user"`
	// BinDir — каталог утилит PostgreSQL.
	BinDir string `yaml:"bin_dir"`
	// Host, Port, Role — параметры сетевого подключения (режим tcp).
	Host string `yaml:"host"`
	Port string `yaml:"port"`
	Role string `yaml:"role"`
	// Database — служебная БД для запросов к каталогу.
	Database string `yaml:"database"`
	// PasswordFile — файл с паролем роли для режима tcp (права 0600).
	PasswordFile string `yaml:"password_file"`
	// Jobs — параллелизм pg_restore.
	Jobs int `yaml:"jobs"`
	// Timeout — предел времени на одну операцию.
	Timeout Duration `yaml:"timeout"`
}

// Prefix возвращает способ запуска утилит PostgreSQL.
func (p PostgresConfig) Prefix() pg.Prefix {
	return pg.Prefix{Mode: p.Mode, SudoPath: p.SudoPath, SudoUser: p.SudoUser, BinDir: p.BinDir}
}

// StorageConfig — где лежат бэкапы и служебные данные сервиса.
type StorageConfig struct {
	// Dir — каталог бэкапов сервиса, например /var/backups/sqlbrc.
	// Это отдельный подкаталог, а не /var/backups целиком: политика хранения
	// удаляет старые дампы, поэтому рядом не должно быть чужих файлов.
	Dir string `yaml:"dir"`
	// StateDir — служебный каталог сервиса: журнал задач, аудит, служебные файлы.
	StateDir string `yaml:"state_dir"`
	// KeepLast — сколько последних бэкапов каждой БД хранить.
	KeepLast int `yaml:"keep_last"`
	// KeepDays — сколько дней хранить бэкап, даже если он не попал в KeepLast.
	KeepDays int `yaml:"keep_days"`
	// MinFreeSpace — минимальный свободный остаток на разделе с бэкапами.
	// Меньше него новые бэкапы не создаются: иначе место кончится на середине
	// дампа, и файл останется битым.
	MinFreeSpace bytesize.Size `yaml:"min_free_space"`
}

// DatabasesConfig — какие БД обслуживаются.
type DatabasesConfig struct {
	// Pattern — шаблон имён обслуживаемых БД: стенды вида <префикс>-<номер>
	// (fse-1234, fssd-7, dops-fix-42).
	Pattern string `yaml:"pattern"`
	// Protected — БД, которые сервис не трогает даже при совпадении с шаблоном.
	Protected []string `yaml:"protected"`
	// Owner — роль-владелец создаваемых БД.
	Owner string `yaml:"owner"`
	// AutoBackupBeforeRestore — снимать служебный бэкап перед восстановлением.
	AutoBackupBeforeRestore bool `yaml:"auto_backup_before_restore"`
	// TerminateOnRestore — завершать активные подключения перед восстановлением.
	TerminateOnRestore bool `yaml:"terminate_on_restore"`
}

// Compile компилирует шаблон имён БД.
func (d DatabasesConfig) Compile() (*regexp.Regexp, error) {
	pattern := strings.TrimSpace(d.Pattern)
	if pattern == "" {
		pattern = pg.DefaultAllowPattern
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("databases.pattern: шаблон %q некорректен: %w", d.Pattern, err)
	}
	return compiled, nil
}

// IsProtected отвечает, входит ли БД в защищённый список (служебные БД включены
// всегда, даже если администратор забыл их в конфиге).
func (d DatabasesConfig) IsProtected(name string) bool {
	if pg.IsReservedDatabase(name) {
		return true
	}
	for _, protected := range d.Protected {
		if protected == name {
			return true
		}
	}
	return false
}

// Allowed отвечает, управляет ли сервис этой базой: имя подходит под шаблон и
// база не защищена. Проверка используется и веб-интерфейсом, и сервисом бэкапов,
// и проверками окружения — правила в одном месте.
func (d DatabasesConfig) Allowed(name string) bool {
	if name == "" || d.IsProtected(name) {
		return false
	}
	compiled, err := d.Compile()
	if err != nil {
		return false
	}
	return compiled.MatchString(name)
}

// JobsConfig — ограничения фоновых задач.
type JobsConfig struct {
	// MaxParallel — сколько задач выполняется одновременно.
	MaxParallel int `yaml:"max_parallel"`
	// MaxPerDB — сколько задач может идти по одной БД.
	MaxPerDB int `yaml:"max_per_db"`
	// Retain — сколько записей журнала задач хранить.
	Retain int `yaml:"retain"`
}

// LoggingConfig — журнал сервиса.
type LoggingConfig struct {
	// Level — debug, info, warn или error.
	Level string `yaml:"level"`
	// Format — text (для человека) или json (для journald и сборщиков).
	Format string `yaml:"format"`
}

// Default возвращает конфигурацию по умолчанию для сервера из README:
// Debian 12, PostgreSQL 15, бэкапы в /var/backups/sqlbrc, интерфейс на 0.0.0.0:8088.
func Default() Configuration {
	return Configuration{
		SchemaVersion: SchemaVersion,
		Server: ServerConfig{
			Listen:       "0.0.0.0:8088",
			MaxBodyBytes: 1 << 20,
		},
		Auth: AuthConfig{
			SessionTTL:       Duration(12 * time.Hour),
			LoginBackoff:     Duration(time.Second),
			MaxLoginAttempts: 5,
		},
		Postgres: PostgresConfig{
			Mode:     pg.ModeSudo,
			SudoPath: pg.DefaultSudoPath,
			SudoUser: pg.DefaultSudoUser,
			BinDir:   pg.DefaultBinDir,
			Database: pg.DefaultDatabase,
			Jobs:     pg.DefaultJobs,
			Timeout:  Duration(pg.DefaultTimeout),
		},
		Storage: StorageConfig{
			Dir:          "/var/backups/sqlbrc",
			StateDir:     "/var/lib/sqlbrc",
			KeepLast:     7,
			KeepDays:     14,
			MinFreeSpace: bytesize.Size(5 * 1000 * 1000 * 1000),
		},
		Databases: DatabasesConfig{
			Pattern:                 pg.DefaultAllowPattern,
			Protected:               append([]string{}, pg.ReservedDatabases...),
			AutoBackupBeforeRestore: true,
			TerminateOnRestore:      true,
		},
		Jobs: JobsConfig{
			MaxParallel: 2,
			MaxPerDB:    1,
			Retain:      200,
		},
		Logging: LoggingConfig{
			Level:  LevelInfo,
			Format: FormatText,
		},
	}
}

// Redacted возвращает копию конфигурации без секретов: она попадает в журнал
// задач и в раздел диагностики веб-интерфейса.
func (c Configuration) Redacted() Configuration {
	redacted := c
	redacted.Auth.Users = make([]User, 0, len(c.Auth.Users))
	for _, user := range c.Auth.Users {
		redacted.Auth.Users = append(redacted.Auth.Users, User{Login: user.Login, PasswordBcrypt: "***"})
	}
	if redacted.Auth.TokenFile != "" {
		redacted.Auth.TokenFile = "***"
	}
	if redacted.Postgres.PasswordFile != "" {
		redacted.Postgres.PasswordFile = "***"
	}
	return redacted
}
