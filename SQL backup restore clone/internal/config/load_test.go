package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
)

// writeConfig создаёт временный файл конфигурации и возвращает его путь.
func writeConfig(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("не удалось создать конфиг: %v", err)
	}
	return path
}

// minimalConfig — минимальный конфиг как в deploy/config.example.yaml: всё остальное
// берётся из значений по умолчанию, поэтому файл должен читаться и проверяться.
const minimalConfig = `
server:
  listen: "0.0.0.0:8088"
  allow_insecure: true
auth:
  users:
    - login: admin
      password_bcrypt: "$2y$10$abcdefghijklmnopqrstuu0123456789abcdefghijklmnopqrstu"
postgres:
  sudo_user: postgres
storage:
  dir: "/var/backups/sqlbrc"
  keep_last: 7
`

func TestLoad(t *testing.T) {
	cfg, path, err := Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if !strings.HasSuffix(path, "config.yaml") {
		t.Errorf("путь к конфигу = %q", path)
	}
	// Поля, которых нет в файле, приходят из значений по умолчанию.
	if cfg.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, ожидался %d", cfg.SchemaVersion, SchemaVersion)
	}
	if cfg.Storage.KeepLast != 7 || cfg.Storage.Dir != "/var/backups/sqlbrc" {
		t.Errorf("настройки хранения не применились: %+v", cfg.Storage)
	}
	if cfg.Postgres.Mode != "sudo" || cfg.Postgres.BinDir != "/usr/bin" {
		t.Errorf("значения по умолчанию для PostgreSQL потеряны: %+v", cfg.Postgres)
	}
	if len(cfg.Auth.Users) != 1 || cfg.Auth.Users[0].Login != "admin" {
		t.Errorf("пользователи не прочитаны: %+v", cfg.Auth.Users)
	}
	if cfg.Storage.MinFreeSpace != bytesize.Size(5*1000*1000*1000) {
		t.Errorf("порог свободного места по умолчанию = %s", cfg.Storage.MinFreeSpace)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name     string
		document string
		wantHint string
	}{
		{
			name:     "некорректный YAML",
			document: "server: [\n",
			wantHint: "разобрать",
		},
		{
			name:     "неизвестный размер",
			document: minimalConfig + "  min_free_space: \"много\"\n",
			wantHint: "размер",
		},
		{
			name:     "ошибка проверки конфига",
			document: strings.Replace(minimalConfig, "keep_last: 7", "keep_last: 0", 1),
			wantHint: "конфиг",
		},
		{
			name:     "ломающий правило интерфейс",
			document: strings.Replace(minimalConfig, `allow_insecure: true`, `allow_insecure: false`, 1),
			wantHint: "allow_insecure",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := Load(writeConfig(t, test.document))
			if err == nil {
				t.Fatal("Load вернула nil, ожидалась ошибка")
			}
			if !strings.Contains(err.Error(), test.wantHint) {
				t.Errorf("в ошибке нет %q: %v", test.wantHint, err)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "нет-такого.yaml")
	if _, _, err := Load(missing); err == nil {
		t.Fatal("ожидалась ошибка отсутствующего конфига")
	} else if !strings.Contains(err.Error(), "не найден") {
		t.Errorf("ошибка не объясняет причину: %v", err)
	}
}

// TestReadWithoutValidation проверяет, что Read отдаёт конфиг без проверки:
// на этом построен `sqlbrc doctor` — замечания к конфигу он показывает
// отдельной строкой отчёта, а не завершается ошибкой до прогона.
func TestReadWithoutValidation(t *testing.T) {
	path := writeConfig(t, strings.Replace(minimalConfig, "keep_last: 7", "keep_last: 0", 1))

	cfg, resolved, err := Read(path)
	if err != nil {
		t.Fatalf("Read вернула ошибку: %v", err)
	}
	if resolved != path {
		t.Errorf("путь = %q, ожидался %q", resolved, path)
	}
	if cfg.Storage.KeepLast != 0 {
		t.Errorf("значение из файла не прочитано: %+v", cfg.Storage)
	}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() не нашла проблему, которую должен показать doctor")
	}
	// Load на том же файле отказывается работать: сервис не должен стартовать
	// с некорректной конфигурацией.
	if _, _, err := Load(path); err == nil {
		t.Error("Load приняла конфиг, который не проходит проверку")
	}
}

// TestResolvePath проверяет порядок поиска конфига: флаг, затем $SQLBRC_CONFIG,
// затем путь по умолчанию.
func TestResolvePath(t *testing.T) {
	envPath := writeConfig(t, minimalConfig)
	flagPath := writeConfig(t, minimalConfig)

	t.Run("явный путь", func(t *testing.T) {
		t.Setenv(EnvConfigPath, envPath)
		got, err := ResolvePath(flagPath)
		if err != nil {
			t.Fatalf("неожиданная ошибка: %v", err)
		}
		if got != flagPath {
			t.Errorf("путь = %q, ожидался %q", got, flagPath)
		}
	})

	t.Run("переменная окружения", func(t *testing.T) {
		t.Setenv(EnvConfigPath, envPath)
		got, err := ResolvePath("")
		if err != nil {
			t.Fatalf("неожиданная ошибка: %v", err)
		}
		if got != envPath {
			t.Errorf("путь = %q, ожидался %q", got, envPath)
		}
	})

	t.Run("путь по умолчанию не найден", func(t *testing.T) {
		t.Setenv(EnvConfigPath, "")
		_, err := ResolvePath("")
		if err == nil {
			t.Fatal("ожидалась ошибка: конфига по умолчанию в тестовой среде нет")
		}
		if !strings.Contains(err.Error(), DefaultConfigPath) {
			t.Errorf("в ошибке нет пути по умолчанию: %v", err)
		}
		if !strings.Contains(err.Error(), EnvConfigPath) {
			t.Errorf("в ошибке нет подсказки про %s: %v", EnvConfigPath, err)
		}
	})

	t.Run("явный путь не найден", func(t *testing.T) {
		t.Setenv(EnvConfigPath, envPath)
		if _, err := ResolvePath(filepath.Join(t.TempDir(), "нет.yaml")); err == nil {
			t.Fatal("ожидалась ошибка отсутствующего файла")
		}
	})
}

// envLookup возвращает функцию поиска переменных с заданными значениями.
func envLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

// TestApplyEnv проверяет переопределения из окружения: из unit-файла systemd
// меняются адрес, каталоги и пороги без правки конфига.
func TestApplyEnv(t *testing.T) {
	cfg := validConfig()
	err := ApplyEnv(&cfg, envLookup(map[string]string{
		"SQLBRC_SERVER_LISTEN":          "127.0.0.1:9090",
		"SQLBRC_SERVER_READ_ONLY":       "true",
		"SQLBRC_SERVER_ALLOW_INSECURE":  "false",
		"SQLBRC_SERVER_TLS_CERT_FILE":   "/etc/sqlbrc/tls.crt",
		"SQLBRC_SERVER_TLS_KEY_FILE":    "/etc/sqlbrc/tls.key",
		"SQLBRC_AUTH_TOKEN_FILE":        "/etc/sqlbrc/tokens",
		"SQLBRC_POSTGRES_SUDO_USER":     "dbowner",
		"SQLBRC_POSTGRES_BIN_DIR":       "/usr/local/bin",
		"SQLBRC_POSTGRES_MODE":          "custom-mode",
		"SQLBRC_POSTGRES_PASSWORD_FILE": "",
		"SQLBRC_STORAGE_DIR":            "/srv/backups",
		"SQLBRC_STORAGE_STATE_DIR":      "/var/lib/sqlbrc",
		"SQLBRC_STORAGE_KEEP_LAST":      "3",
		"SQLBRC_STORAGE_KEEP_DAYS":      "30",
		"SQLBRC_STORAGE_MIN_FREE_SPACE": "2GB",
		"SQLBRC_DATABASES_PATTERN":      "^stand-.*$",
		"SQLBRC_DATABASES_OWNER":        "postgres",
		"SQLBRC_LOG_LEVEL":              "debug",
		"SQLBRC_LOG_FORMAT":             "json",
		"SQLBRC_UNKNOWN_VARIABLE":       "ignored",
	}))
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:9090" || !cfg.Server.ReadOnly || cfg.Server.AllowInsecure {
		t.Errorf("настройки интерфейса не применились: %+v", cfg.Server)
	}
	if !cfg.Server.TLS.Enabled() {
		t.Errorf("TLS не применился: %+v", cfg.Server.TLS)
	}
	if cfg.Postgres.SudoUser != "dbowner" || cfg.Postgres.BinDir != "/usr/local/bin" || cfg.Postgres.Mode != "custom-mode" {
		t.Errorf("настройки PostgreSQL не применились: %+v", cfg.Postgres)
	}
	if cfg.Storage.Dir != "/srv/backups" || cfg.Storage.KeepLast != 3 || cfg.Storage.KeepDays != 30 {
		t.Errorf("настройки хранения не применились: %+v", cfg.Storage)
	}
	if cfg.Storage.MinFreeSpace != bytesize.Size(2_000_000_000) {
		t.Errorf("порог свободного места = %s, ожидалось 2.0 GB", cfg.Storage.MinFreeSpace)
	}
	if cfg.Logging.Level != "debug" || cfg.Logging.Format != "json" {
		t.Errorf("настройки журнала не применились: %+v", cfg.Logging)
	}
	if cfg.Databases.Pattern != "^stand-.*$" || cfg.Databases.Owner != "postgres" {
		t.Errorf("настройки баз не применились: %+v", cfg.Databases)
	}
	// Пустое значение переменной не затирает конфиг.
	if cfg.Auth.TokenFile != "/etc/sqlbrc/tokens" && cfg.Auth.TokenFile == "" {
		t.Errorf("пустое значение обработано неверно: %+v", cfg.Auth)
	}
}

// TestApplyEnvErrors проверяет, что некорректное значение переменной окружения
// даёт понятную ошибку с её именем, а не молчаливое значение по умолчанию.
func TestApplyEnvErrors(t *testing.T) {
	tests := []struct {
		name      string
		variables map[string]string
		wantHint  string
	}{
		{
			name:      "булево значение",
			variables: map[string]string{"SQLBRC_SERVER_READ_ONLY": "может быть"},
			wantHint:  "SQLBRC_SERVER_READ_ONLY",
		},
		{
			name:      "целое значение",
			variables: map[string]string{"SQLBRC_STORAGE_KEEP_LAST": "семь"},
			wantHint:  "SQLBRC_STORAGE_KEEP_LAST",
		},
		{
			name:      "размер",
			variables: map[string]string{"SQLBRC_STORAGE_MIN_FREE_SPACE": "много"},
			wantHint:  "SQLBRC_STORAGE_MIN_FREE_SPACE",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			err := ApplyEnv(&cfg, envLookup(test.variables))
			if err == nil {
				t.Fatal("ApplyEnv вернула nil, ожидалась ошибка")
			}
			if !strings.Contains(err.Error(), test.wantHint) {
				t.Errorf("в ошибке нет имени переменной %q: %v", test.wantHint, err)
			}
		})
	}
}

// TestApplyEnvEmpty проверяет, что без переменных окружения конфигурация не меняется.
func TestApplyEnvEmpty(t *testing.T) {
	cfg := validConfig()
	before := cfg
	if err := ApplyEnv(&cfg, envLookup(nil)); err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if cfg.Server.Listen != before.Server.Listen || cfg.Storage.Dir != before.Storage.Dir {
		t.Errorf("конфигурация изменилась без переменных окружения: %+v", cfg)
	}
}

// TestEnvNames проверяет, что список переменных окружения полон и отсортирован:
// его печатает справка `sqlbrc doctor`, и по нему администратор узнаёт имена.
func TestEnvNames(t *testing.T) {
	names := EnvNames()
	if len(names) != len(envOverrides) {
		t.Fatalf("имён %d, ожидалось %d", len(names), len(envOverrides))
	}
	for index, name := range names {
		if !strings.HasPrefix(name, "SQLBRC_") {
			t.Errorf("имя %q не начинается с SQLBRC_", name)
		}
		if name == EnvConfigPath {
			t.Errorf("путь к конфигу %q не переопределяется через ApplyEnv", EnvConfigPath)
		}
		if index > 0 && names[index-1] >= name {
			t.Errorf("список не отсортирован: %q перед %q", names[index-1], name)
		}
	}
}

// TestDurationYAML проверяет разбор длительностей: единица измерения обязательна,
// иначе значение вида «1800» читалось бы как 1800 наносекунд.
func TestDurationYAML(t *testing.T) {
	tests := []struct {
		name     string
		document string
		want     time.Duration
		wantErr  bool
	}{
		{name: "часы", document: "value: 12h\n", want: 12 * time.Hour},
		{name: "минуты", document: "value: 30m\n", want: 30 * time.Minute},
		{name: "секунды", document: "value: 45s\n", want: 45 * time.Second},
		{name: "число без единицы", document: "value: 1800\n", wantErr: true},
		{name: "пустое значение", document: "value: \"\"\n", wantErr: true},
		{name: "текст", document: "value: много\n", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var holder struct {
				Value Duration `yaml:"value"`
			}
			err := yaml.Unmarshal([]byte(test.document), &holder)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ожидалась ошибка разбора %q", test.document)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка разбора %q: %v", test.document, err)
			}
			if holder.Value.Duration() != test.want {
				t.Errorf("длительность = %s, ожидалось %s", holder.Value, test.want)
			}
		})
	}
}

// TestDurationMarshal проверяет, что длительность печатается строкой: снапшот
// настроек в отчёте сервиса должен оставаться читаемым.
func TestDurationMarshal(t *testing.T) {
	holder := struct {
		Value Duration `yaml:"value"`
	}{Value: Duration(90 * time.Minute)}

	out, err := yaml.Marshal(holder)
	if err != nil {
		t.Fatalf("не удалось сериализовать длительность: %v", err)
	}
	if got, want := string(out), "value: 1h30m0s\n"; got != want {
		t.Errorf("сериализация = %q, ожидалось %q", got, want)
	}
}

// FuzzLoad проверяет, что разбор произвольного YAML-документа не паникует:
// конфиг — внешние данные, а на сервере его правят руками.
func FuzzLoad(f *testing.F) {
	seeds := []string{
		minimalConfig,
		"",
		"server: {listen: ':8088'}\n",
		"storage: {min_free_space: 5GB}\n",
		"auth: {session_ttl: 12h}\n",
		"schema_version: not-a-number\n",
		"postgres:\n  sudo_user: postgres\n  jobs: 4\n",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, document string) {
		var cfg Configuration
		if err := yaml.Unmarshal([]byte(document), &cfg); err != nil {
			return
		}
		// Проверки и печать не должны падать на разобранном документе.
		_ = cfg.Validate()
		_ = cfg.Redacted()
		if text := cfg.Storage.MinFreeSpace.String(); text == "" {
			t.Errorf("пустое представление порога свободного места для %q", document)
		}
	})
}
