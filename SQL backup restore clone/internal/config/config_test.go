package config

import (
	"strings"
	"testing"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// testBcryptHash — тестовый хэш пароля: 60 символов и префикс $2y$, как у bcrypt.
// Настоящий пароль в тестах не нужен: проверяется только формат хэша.
const testBcryptHash = "$2y$10$abcdefghijklmnopqrstuu0123456789abcdefghijklmnopqrstu"

// validConfig возвращает конфигурацию, проходящую проверку: значения по умолчанию,
// пользователь веб-интерфейса (без него сервис запускаться не должен) и
// allow_insecure, как в deploy/config.example.yaml — интерфейс слушает LAN
// из README (0.0.0.0:8088 без reverse-proxy).
func validConfig() Configuration {
	cfg := Default()
	cfg.Server.AllowInsecure = true
	cfg.Auth.Users = []User{{Login: "admin", PasswordBcrypt: testBcryptHash}}
	return cfg
}

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, ожидался %d", cfg.SchemaVersion, SchemaVersion)
	}
	// Политика хранения из README: последние 7 бэкапов каждой базы.
	if cfg.Storage.KeepLast != 7 {
		t.Errorf("storage.keep_last = %d, ожидалось 7 (требование README)", cfg.Storage.KeepLast)
	}
	if cfg.Server.Listen != "0.0.0.0:8088" {
		t.Errorf("server.listen = %q, ожидалось 0.0.0.0:8088", cfg.Server.Listen)
	}
	if cfg.Postgres.Mode != pg.ModeSudo || cfg.Postgres.SudoUser != pg.DefaultSudoUser {
		t.Errorf("режим PostgreSQL по умолчанию: %+v", cfg.Postgres)
	}
	if cfg.Storage.Dir != "/var/backups/sqlbrc" {
		t.Errorf("каталог бэкапов = %q", cfg.Storage.Dir)
	}
	if cfg.Databases.Pattern != pg.DefaultAllowPattern {
		t.Errorf("шаблон имён БД = %q, ожидался %q", cfg.Databases.Pattern, pg.DefaultAllowPattern)
	}
	if !cfg.Databases.AutoBackupBeforeRestore || !cfg.Databases.TerminateOnRestore {
		t.Errorf("защита перед восстановлением выключена: %+v", cfg.Databases)
	}
	// Минимальный конфиг из README обязан проходить проверку после добавления пользователя.
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("конфигурация по умолчанию с пользователем не проходит проверку: %v", err)
	}
	// Но сам Default() без решения администратора о TLS проверку не проходит:
	// открытый интерфейс — осознанный выбор, а не значение по умолчанию.
	if err := Default().Validate(); err == nil || !strings.Contains(err.Error(), "allow_insecure") {
		t.Errorf("Default() должна требовать решения о TLS, получено: %v", err)
	}
}

func TestValidateCollectsAllProblems(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.KeepLast = 0
	cfg.Storage.KeepDays = 0
	cfg.Logging.Format = "xml"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() вернула nil, ожидалась ошибка")
	}
	for _, want := range []string{"keep_last", "keep_days", "logging.format"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в ошибке нет %q: %v", want, err)
		}
	}
	if count := strings.Count(err.Error(), "\n  - "); count != 3 {
		t.Errorf("замечаний %d, ожидалось 3: %v", count, err)
	}
}

// validateCase — изменение конфигурации и ожидаемый результат проверки.
type validateCase struct {
	name     string
	change   func(*Configuration)
	wantHint string
	wantErr  bool
}

// runValidateCases применяет изменения к корректной конфигурации и проверяет результат.
func runValidateCases(t *testing.T, tests []validateCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			if test.change != nil {
				test.change(&cfg)
			}
			err := cfg.Validate()
			if !test.wantErr {
				if err != nil {
					t.Fatalf("Validate() вернула ошибку: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate() вернула nil, ожидалась ошибка")
			}
			if !strings.Contains(err.Error(), test.wantHint) {
				t.Errorf("в ошибке нет %q: %v", test.wantHint, err)
			}
		})
	}
}

// TestValidateServerAndAuth проверяет настройки веб-интерфейса и аутентификации:
// интерфейс слушает LAN без reverse-proxy, поэтому и TLS, и пользователи обязательны.
func TestValidateServerAndAuth(t *testing.T) {
	runValidateCases(t, []validateCase{
		{name: "значения по умолчанию корректны"},
		{
			name:     "неизвестная версия схемы",
			change:   func(c *Configuration) { c.SchemaVersion = 99 },
			wantHint: "schema_version",
			wantErr:  true,
		},
		{
			name:     "пустой адрес интерфейса",
			change:   func(c *Configuration) { c.Server.Listen = " " },
			wantHint: "server.listen",
			wantErr:  true,
		},
		{
			name:     "адрес без порта",
			change:   func(c *Configuration) { c.Server.Listen = "0.0.0.0" },
			wantHint: "server.listen",
			wantErr:  true,
		},
		{
			name:     "порт вне диапазона",
			change:   func(c *Configuration) { c.Server.Listen = "0.0.0.0:99999" },
			wantHint: "порт",
			wantErr:  true,
		},
		{
			name:   "loopback без TLS допустим",
			change: func(c *Configuration) { c.Server.Listen = "127.0.0.1:8088" },
		},
		{
			name: "TLS настроен",
			change: func(c *Configuration) {
				c.Server.TLS = TLSConfig{CertFile: "/etc/sqlbrc/tls.crt", KeyFile: "/etc/sqlbrc/tls.key"}
			},
		},
		{
			name:     "открытый интерфейс без TLS запрещён",
			change:   func(c *Configuration) { c.Server.AllowInsecure = false },
			wantHint: "allow_insecure",
			wantErr:  true,
		},
		{
			name:     "сертификат без ключа",
			change:   func(c *Configuration) { c.Server.TLS = TLSConfig{CertFile: "/etc/sqlbrc/tls.crt"} },
			wantHint: "key_file",
			wantErr:  true,
		},
		{
			name:     "ключ без сертификата",
			change:   func(c *Configuration) { c.Server.TLS = TLSConfig{KeyFile: "/etc/sqlbrc/tls.key"} },
			wantHint: "cert_file",
			wantErr:  true,
		},
		{
			name:     "предел тела запроса слишком мал",
			change:   func(c *Configuration) { c.Server.MaxBodyBytes = 10 },
			wantHint: "max_body_bytes",
			wantErr:  true,
		},
		{
			name:     "нет пользователей и токенов",
			change:   func(c *Configuration) { c.Auth.Users = nil },
			wantHint: "auth",
			wantErr:  true,
		},
		{
			name:   "только токен без пользователей",
			change: func(c *Configuration) { c.Auth.Users = nil; c.Auth.TokenFile = "/etc/sqlbrc/tokens" },
		},
		{
			name:     "некорректный хэш пароля",
			change:   func(c *Configuration) { c.Auth.Users[0].PasswordBcrypt = "пароль" },
			wantHint: "password_bcrypt",
			wantErr:  true,
		},
		{
			name:     "пустой хэш пароля",
			change:   func(c *Configuration) { c.Auth.Users[0].PasswordBcrypt = "" },
			wantHint: "password_bcrypt",
			wantErr:  true,
		},
		{
			name: "повторяющиеся логины",
			change: func(c *Configuration) {
				c.Auth.Users = append(c.Auth.Users, User{Login: "admin", PasswordBcrypt: testBcryptHash})
			},
			wantHint: "повторяется",
			wantErr:  true,
		},
		{
			name:     "логин с заглавными буквами",
			change:   func(c *Configuration) { c.Auth.Users[0].Login = "Admin" },
			wantHint: "login",
			wantErr:  true,
		},
		{
			name:     "слишком короткий логин",
			change:   func(c *Configuration) { c.Auth.Users[0].Login = "ab" },
			wantHint: "login",
			wantErr:  true,
		},
		{
			name:     "нулевое время жизни сессии",
			change:   func(c *Configuration) { c.Auth.SessionTTL = 0 },
			wantHint: "session_ttl",
			wantErr:  true,
		},
		{
			name:     "слишком долгая сессия",
			change:   func(c *Configuration) { c.Auth.SessionTTL = Duration(200 * time.Hour) },
			wantHint: "session_ttl",
			wantErr:  true,
		},
		{
			name:     "отрицательная пауза входа",
			change:   func(c *Configuration) { c.Auth.LoginBackoff = Duration(-time.Second) },
			wantHint: "login_backoff",
			wantErr:  true,
		},
		{
			name:     "ноль попыток входа",
			change:   func(c *Configuration) { c.Auth.MaxLoginAttempts = 0 },
			wantHint: "max_login_attempts",
			wantErr:  true,
		},
	})
}

// TestValidatePostgresAndStorage проверяет доступ к PostgreSQL, каталоги, политику
// хранения и ограничения задач.
func TestValidatePostgresAndStorage(t *testing.T) {
	runValidateCases(t, []validateCase{
		{
			name:     "режим tcp ещё не поддержан",
			change:   func(c *Configuration) { c.Postgres.Mode = pg.ModeTCP },
			wantHint: "0.2.0",
			wantErr:  true,
		},
		{
			name:     "роль не применяется в режиме sudo",
			change:   func(c *Configuration) { c.Postgres.Role = "app_owner" },
			wantHint: "postgres.role",
			wantErr:  true,
		},
		{
			name:     "пароль не используется в режиме sudo",
			change:   func(c *Configuration) { c.Postgres.PasswordFile = "/etc/sqlbrc/pgpass" },
			wantHint: "postgres",
			wantErr:  true,
		},
		{
			name:     "некорректный пользователь sudo",
			change:   func(c *Configuration) { c.Postgres.SudoUser = "post gres" },
			wantHint: "sudo_user",
			wantErr:  true,
		},
		{
			name:     "слишком много процессов восстановления",
			change:   func(c *Configuration) { c.Postgres.Jobs = 64 },
			wantHint: "jobs",
			wantErr:  true,
		},
		{
			name:     "слишком короткий таймаут операции",
			change:   func(c *Configuration) { c.Postgres.Timeout = Duration(time.Second) },
			wantHint: "timeout",
			wantErr:  true,
		},
		{
			name:     "относительный каталог бэкапов",
			change:   func(c *Configuration) { c.Storage.Dir = "backups" },
			wantHint: "storage.dir",
			wantErr:  true,
		},
		{
			name:     "относительный служебный каталог",
			change:   func(c *Configuration) { c.Storage.StateDir = "state" },
			wantHint: "state_dir",
			wantErr:  true,
		},
		{
			name:     "служебный каталог совпадает с каталогом бэкапов",
			change:   func(c *Configuration) { c.Storage.StateDir = c.Storage.Dir },
			wantHint: "state_dir",
			wantErr:  true,
		},
		{
			name:     "нулевое число хранимых бэкапов",
			change:   func(c *Configuration) { c.Storage.KeepLast = 0 },
			wantHint: "keep_last",
			wantErr:  true,
		},
		{
			name:     "нулевой срок хранения",
			change:   func(c *Configuration) { c.Storage.KeepDays = 0 },
			wantHint: "keep_days",
			wantErr:  true,
		},
		{
			name:     "нулевой порог свободного места",
			change:   func(c *Configuration) { c.Storage.MinFreeSpace = 0 },
			wantHint: "min_free_space",
			wantErr:  true,
		},
		{
			name:     "недостижимый порог свободного места",
			change:   func(c *Configuration) { c.Storage.MinFreeSpace = maxMinFreeSpace + 1 },
			wantHint: "min_free_space",
			wantErr:  true,
		},
		{
			name:     "некорректный шаблон имён БД",
			change:   func(c *Configuration) { c.Databases.Pattern = "[" },
			wantHint: "databases.pattern",
			wantErr:  true,
		},
		{
			name:     "пустой защищённый список",
			change:   func(c *Configuration) { c.Databases.Protected = nil },
			wantHint: "databases.protected",
			wantErr:  true,
		},
		{
			name:     "некорректный владелец баз",
			change:   func(c *Configuration) { c.Databases.Owner = "app-owner" },
			wantHint: "databases.owner",
			wantErr:  true,
		},
		{
			name:   "корректный владелец баз",
			change: func(c *Configuration) { c.Databases.Owner = "postgres" },
		},
		{
			name:     "нет параллельных задач",
			change:   func(c *Configuration) { c.Jobs.MaxParallel = 0 },
			wantHint: "jobs.max_parallel",
			wantErr:  true,
		},
		{
			name:     "задач по одной базе больше, чем всего",
			change:   func(c *Configuration) { c.Jobs.MaxPerDB = 8 },
			wantHint: "jobs.max_per_db",
			wantErr:  true,
		},
		{
			name:     "слишком маленький журнал задач",
			change:   func(c *Configuration) { c.Jobs.Retain = 2 },
			wantHint: "jobs.retain",
			wantErr:  true,
		},
		{
			name:     "неизвестный уровень журнала",
			change:   func(c *Configuration) { c.Logging.Level = "verbose" },
			wantHint: "logging.level",
			wantErr:  true,
		},
		{
			name:     "неизвестный формат журнала",
			change:   func(c *Configuration) { c.Logging.Format = "xml" },
			wantHint: "logging.format",
			wantErr:  true,
		},
	})
}

// TestRedacted проверяет, что секреты не попадают в журнал и в диагностику:
// конфиг с секретами печатается в отчётах сервиса.
func TestRedacted(t *testing.T) {
	cfg := validConfig()
	cfg.Auth.TokenFile = "/etc/sqlbrc/tokens"
	cfg.Postgres.PasswordFile = "/etc/sqlbrc/pgpass"

	redacted := cfg.Redacted()
	if redacted.Auth.Users[0].Login != "admin" {
		t.Errorf("логин потерян при скрытии секретов: %+v", redacted.Auth.Users)
	}
	for _, secret := range []string{testBcryptHash, "/etc/sqlbrc/tokens", "/etc/sqlbrc/pgpass"} {
		if strings.Contains(redacted.Auth.Users[0].PasswordBcrypt+redacted.Auth.TokenFile+redacted.Postgres.PasswordFile, secret) {
			t.Errorf("секрет %q не скрыт", secret)
		}
	}
	// Исходная конфигурация не меняется: скрытие возвращает копию.
	if cfg.Auth.Users[0].PasswordBcrypt != testBcryptHash {
		t.Errorf("исходный хэш изменён: %+v", cfg.Auth.Users[0])
	}
}

// TestDatabasesAllowed проверяет правило «какими базами управляет сервис»:
// оно решает, какие базы попадают в бэкапы, восстановление и удаление.
func TestDatabasesAllowed(t *testing.T) {
	tests := []struct {
		name string
		cfg  DatabasesConfig
		db   string
		want bool
	}{
		{name: "стенд по умолчанию", cfg: Default().Databases, db: "fse-1234", want: true},
		{name: "другой префикс стенда", cfg: Default().Databases, db: "fssd-7", want: true},
		{name: "дефис в префиксе стенда", cfg: Default().Databases, db: "dops-fix-42", want: true},
		{name: "служебная база", cfg: Default().Databases, db: "postgres", want: false},
		{name: "шаблон базы", cfg: Default().Databases, db: "template0", want: false},
		{name: "чужая база без номера стенда", cfg: Default().Databases, db: "prod", want: false},
		{name: "номер не в конце имени", cfg: Default().Databases, db: "fse-1234-x", want: false},
		{name: "верхний регистр", cfg: Default().Databases, db: "FSE-1", want: false},
		{name: "пустое имя", cfg: Default().Databases, db: "", want: false},
		{
			name: "защищённый список конфига",
			cfg:  DatabasesConfig{Pattern: `^.*$`, Protected: []string{"prod"}},
			db:   "prod",
			want: false,
		},
		{
			name: "служебная база защищена всегда",
			cfg:  DatabasesConfig{Pattern: `^postgres$`},
			db:   "postgres",
			want: false,
		},
		{
			name: "шаблон из конфига",
			cfg:  DatabasesConfig{Pattern: `^stand-[0-9]+$`},
			db:   "stand-42",
			want: true,
		},
		{
			name: "некорректный шаблон ничего не разрешает",
			cfg:  DatabasesConfig{Pattern: `[`},
			db:   "stand-42",
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.cfg.Allowed(test.db); got != test.want {
				t.Errorf("Allowed(%q) = %v, ожидалось %v", test.db, got, test.want)
			}
		})
	}
}

// TestCompileDatabasesPattern проверяет компиляцию шаблона: пустой шаблон
// означает шаблон по умолчанию, а некорректный — ошибку с именем поля.
func TestCompileDatabasesPattern(t *testing.T) {
	compiled, err := DatabasesConfig{}.Compile()
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	for _, name := range []string{"fse-1", "fssd-7", "dops-fix-42"} {
		if !compiled.MatchString(name) {
			t.Errorf("шаблон по умолчанию не подходит стенду %s", name)
		}
	}
	for _, name := range []string{"prod", "fse-prod", "template0"} {
		if compiled.MatchString(name) {
			t.Errorf("шаблон по умолчанию разрешает чужую базу %s", name)
		}
	}
	if _, err := (DatabasesConfig{Pattern: "["}).Compile(); err == nil {
		t.Error("некорректный шаблон не дал ошибки")
	}
	if !pg.IsReservedDatabase("template1") {
		t.Error("template1 должна считаться служебной базой")
	}
}

// TestPostgresClientConfig проверяет передачу настроек в пакет запуска утилит:
// расхождение здесь означало бы запуск не под тем пользователем.
func TestPostgresClientConfig(t *testing.T) {
	cfg := validConfig()
	cfg.Postgres.Jobs = 8
	cfg.Postgres.Timeout = Duration(30 * time.Minute)

	client := cfg.Postgres.ClientConfig()
	if client.Mode != pg.ModeSudo || client.SudoUser != pg.DefaultSudoUser || client.BinDir != pg.DefaultBinDir {
		t.Errorf("параметры запуска переданы неверно: %+v", client)
	}
	if client.Database != pg.DefaultDatabase || client.Jobs != 8 || client.Timeout != 30*time.Minute {
		t.Errorf("служебная база, параллелизм и таймаут переданы неверно: %+v", client)
	}
}

// TestIsLoopbackHost проверяет определение «доступен только с сервера»:
// от него зависит требование TLS для веб-интерфейса.
func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "127.0.0.1", want: true},
		{host: "::1", want: true},
		{host: "[::1]", want: true},
		{host: "localhost", want: true},
		{host: "", want: false},
		{host: "0.0.0.0", want: false},
		{host: "192.168.7.96", want: false},
		{host: "::", want: false},
	}

	for _, test := range tests {
		t.Run(test.host, func(t *testing.T) {
			if got := IsLoopbackHost(test.host); got != test.want {
				t.Errorf("IsLoopbackHost(%q) = %v, ожидалось %v", test.host, got, test.want)
			}
		})
	}
}
