package config

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// loginPattern — допустимые имена пользователей веб-интерфейса.
var loginPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// maxMinFreeSpace — верхняя граница порога свободного места: больше 1 ТиБ
// означало бы, что бэкапы не создаются никогда.
const maxMinFreeSpace = bytesize.Size(1 << 40)

// problems собирает все замечания к конфигу: администратор должен увидеть
// полный список ошибок, а не по одной за запуск.
type problems []string

// add добавляет замечание.
func (p *problems) add(format string, args ...any) {
	*p = append(*p, fmt.Sprintf(format, args...))
}

// err возвращает ошибку со всеми замечаниями.
func (p problems) err() error {
	if len(p) == 0 {
		return nil
	}
	return fmt.Errorf("конфиг некорректен:\n  - %s", strings.Join(p, "\n  - "))
}

// Validate проверяет конфигурацию целиком.
func (c Configuration) Validate() error {
	var found problems
	if c.SchemaVersion != SchemaVersion {
		found.add("schema_version = %d, ожидается %d", c.SchemaVersion, SchemaVersion)
	}
	c.validateServer(&found)
	c.validateAuth(&found)
	c.validatePostgres(&found)
	c.validateStorage(&found)
	c.validateDatabases(&found)
	c.validateJobs(&found)
	c.validateLogging(&found)
	return found.err()
}

// ClientConfig собирает настройки запуска утилит PostgreSQL для пакета pg.
func (p PostgresConfig) ClientConfig() pg.Config {
	return pg.Config{
		Mode:         p.Mode,
		SudoPath:     p.SudoPath,
		SudoUser:     p.SudoUser,
		BinDir:       p.BinDir,
		Host:         p.Host,
		Port:         p.Port,
		Role:         p.Role,
		Database:     p.Database,
		PasswordFile: p.PasswordFile,
		Jobs:         p.Jobs,
		Timeout:      p.Timeout.Duration(),
	}
}

// validateServer проверяет адрес веб-интерфейса и настройки TLS.
func (c Configuration) validateServer(found *problems) {
	listen := strings.TrimSpace(c.Server.Listen)
	if listen == "" {
		found.add("server.listen: адрес не задан (пример: 0.0.0.0:8088)")
	} else {
		host, port, err := net.SplitHostPort(listen)
		switch {
		case err != nil:
			found.add("server.listen: %q не разобран как host:port: %v", listen, err)
		default:
			if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
				found.add("server.listen: некорректный порт %q", port)
			}
			// Веб-интерфейс слушает LAN без reverse-proxy, поэтому открытый HTTP
			// за пределами loopback — осознанное решение администратора.
			if !IsLoopbackHost(host) && !c.Server.TLS.Enabled() && !c.Server.AllowInsecure {
				found.add(
					"server.listen: интерфейс слушает %q без TLS: настройте server.tls или осознанно включите server.allow_insecure (трафик ограничивается nftables по подсети)",
					listen)
			}
		}
	}
	if c.Server.TLS.CertFile != "" && c.Server.TLS.KeyFile == "" {
		found.add("server.tls.key_file: задан сертификат без ключа")
	}
	if c.Server.TLS.CertFile == "" && c.Server.TLS.KeyFile != "" {
		found.add("server.tls.cert_file: задан ключ без сертификата")
	}
	if c.Server.MaxBodyBytes < 4096 || c.Server.MaxBodyBytes > 64<<20 {
		found.add("server.max_body_bytes = %d вне диапазона 4KiB..64MiB", c.Server.MaxBodyBytes)
	}
}

// IsLoopbackHost отвечает, доступен ли адрес только с самого сервера.
// Пустой адрес (":8088") означает «все интерфейсы» и локальным не считается.
func IsLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if host == "" {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// validateAuth проверяет встроенных пользователей: без них интерфейс оказался бы
// открыт всем, кто видит порт.
func (c Configuration) validateAuth(found *problems) {
	if len(c.Auth.Users) == 0 && strings.TrimSpace(c.Auth.TokenFile) == "" {
		found.add("auth: не задан ни один пользователь (auth.users) и нет auth.token_file")
	}
	logins := make(map[string]bool, len(c.Auth.Users))
	for index, user := range c.Auth.Users {
		field := fmt.Sprintf("auth.users[%d]", index)
		if !loginPattern.MatchString(user.Login) {
			found.add("%s.login: %q недопустим (строчные латинские буквы, цифры, \"_\", \"-\", \".\", 3..32 символа)", field, user.Login)
		}
		if logins[user.Login] {
			found.add("%s.login: %q повторяется", field, user.Login)
		}
		logins[user.Login] = true
		if err := validateBcrypt(user.PasswordBcrypt); err != nil {
			found.add("%s.password_bcrypt: %v", field, err)
		}
	}
	switch {
	case c.Auth.SessionTTL <= 0:
		found.add("auth.session_ttl: должно быть больше нуля")
	case c.Auth.SessionTTL > Duration(7*24*time.Hour):
		found.add("auth.session_ttl: %s больше 168h: сессия переживёт смену пароля", c.Auth.SessionTTL)
	}
	if c.Auth.LoginBackoff < 0 {
		found.add("auth.login_backoff: не может быть отрицательным")
	}
	if c.Auth.MaxLoginAttempts < 1 || c.Auth.MaxLoginAttempts > 100 {
		found.add("auth.max_login_attempts = %d вне диапазона 1..100", c.Auth.MaxLoginAttempts)
	}
}

// validateBcrypt проверяет формат хэша пароля. Опечатка в хэше иначе выяснилась
// бы на входе, когда уже поздно что-то исправлять.
func validateBcrypt(hash string) error {
	if hash == "" {
		return errors.New("хэш не задан: создайте его командой sqlbrc hash-password")
	}
	if !strings.HasPrefix(hash, "$2") || len(hash) != 60 {
		return errors.New("значение не похоже на bcrypt-хэш (ожидается $2a$… длиной 60 символов)")
	}
	return nil
}

// validatePostgres проверяет режим запуска утилит PostgreSQL. В 0.1.0 поддержан
// только sudo: подключение идёт через локальный сокет с peer-аутентификацией,
// пароли в конфиге не хранятся.
func (c Configuration) validatePostgres(found *problems) {
	mode := c.Postgres.Mode
	if mode == "" {
		mode = pg.ModeSudo
	}
	if mode != pg.ModeSudo {
		found.add(
			"postgres.mode = %q: в 0.1.0 поддерживается только %q (sudo -n -u %s через локальный сокет); режим tcp для контейнера появится в 0.2.0",
			mode, pg.ModeSudo, c.Postgres.SudoUser)
		return
	}
	if role := strings.TrimSpace(c.Postgres.Role); role != "" && role != c.Postgres.SudoUser {
		found.add(
			"postgres.role = %q: в режиме sudo подключение идёт от %q, поэтому роль из конфига не применяется; владелец объектов берётся из дампа (databases.owner)",
			role, c.Postgres.SudoUser)
	}
	if err := c.Postgres.ClientConfig().Validate(); err != nil {
		found.add("postgres: %v", err)
	}
}

// validateStorage проверяет каталоги, политику хранения и порог свободного места.
func (c Configuration) validateStorage(found *problems) {
	if !isAbsoluteUnixPath(c.Storage.Dir) {
		found.add("storage.dir: %q должен быть абсолютным путём (пример: /var/backups)", c.Storage.Dir)
	}
	if !isAbsoluteUnixPath(c.Storage.StateDir) {
		found.add("storage.state_dir: %q должен быть абсолютным путём (пример: /var/lib/sqlbrc)", c.Storage.StateDir)
	}
	// Журнал задач, аудит и служебные файлы не должны лежать среди бэкапов:
	// веб-интерфейс показывает содержимое storage.dir.
	if c.Storage.StateDir == c.Storage.Dir && c.Storage.Dir != "" {
		found.add("storage.state_dir: не должен совпадать со storage.dir")
	}
	if c.Storage.KeepLast < 1 || c.Storage.KeepLast > 1000 {
		found.add("storage.keep_last = %d вне диапазона 1..1000 (в README — 7 бэкапов на БД)", c.Storage.KeepLast)
	}
	if c.Storage.KeepDays < 1 || c.Storage.KeepDays > 3650 {
		found.add("storage.keep_days = %d вне диапазона 1..3650", c.Storage.KeepDays)
	}
	if c.Storage.MinFreeSpace <= 0 {
		found.add("storage.min_free_space: порог свободного места должен быть больше нуля (пример: 5GB)")
	}
	if c.Storage.MinFreeSpace > maxMinFreeSpace {
		found.add("storage.min_free_space = %s больше %s: новые бэкапы не создавались бы никогда",
			c.Storage.MinFreeSpace, maxMinFreeSpace)
	}
}

// validateDatabases проверяет шаблон имён и защищённый список.
func (c Configuration) validateDatabases(found *problems) {
	if _, err := c.Databases.Compile(); err != nil {
		found.add("%v", err)
	}
	if len(c.Databases.Protected) == 0 {
		found.add("databases.protected: список не должен быть пустым: в него входят служебные БД (%s)",
			strings.Join(pg.ReservedDatabases, ", "))
	}
	if owner := c.Databases.Owner; owner != "" {
		if err := pg.ValidateIdentifier(owner, "databases.owner"); err != nil {
			found.add("%v", err)
		}
	}
}

// validateJobs проверяет ограничения фоновых задач.
func (c Configuration) validateJobs(found *problems) {
	if c.Jobs.MaxParallel < 1 || c.Jobs.MaxParallel > 16 {
		found.add("jobs.max_parallel = %d вне диапазона 1..16", c.Jobs.MaxParallel)
	}
	if c.Jobs.MaxPerDB < 1 || c.Jobs.MaxPerDB > 16 {
		found.add("jobs.max_per_db = %d вне диапазона 1..16", c.Jobs.MaxPerDB)
	}
	if c.Jobs.MaxPerDB > c.Jobs.MaxParallel {
		found.add("jobs.max_per_db = %d больше jobs.max_parallel = %d: ограничение никогда не сработает",
			c.Jobs.MaxPerDB, c.Jobs.MaxParallel)
	}
	if c.Jobs.Retain < 10 || c.Jobs.Retain > 100000 {
		found.add("jobs.retain = %d вне диапазона 10..100000", c.Jobs.Retain)
	}
}

// validateLogging проверяет уровень и формат журнала.
func (c Configuration) validateLogging(found *problems) {
	if !IsValidLevel(c.Logging.Level) {
		found.add("logging.level = %q: допустимы %s, %s, %s, %s",
			c.Logging.Level, LevelDebug, LevelInfo, LevelWarn, LevelError)
	}
	if !IsValidFormat(c.Logging.Format) {
		found.add("logging.format = %q: допустимы %s и %s", c.Logging.Format, FormatText, FormatJSON)
	}
}

// IsValidLevel отвечает, поддерживается ли уровень журнала. Проверку использует
// и конфиг, и флаг --log-level: правило в одном месте.
func IsValidLevel(level string) bool {
	switch level {
	case LevelDebug, LevelInfo, LevelWarn, LevelError:
		return true
	default:
		return false
	}
}

// IsValidFormat отвечает, поддерживается ли формат журнала.
func IsValidFormat(format string) bool {
	switch format {
	case FormatText, FormatJSON:
		return true
	default:
		return false
	}
}

// isAbsoluteUnixPath проверяет абсолютный путь в стиле Linux. Проверка сделана
// через префикс «/», а не через filepath.IsAbs: сервис работает на Debian,
// а тесты и разработка идут и на Windows, где filepath.IsAbs("/var") = false.
func isAbsoluteUnixPath(path string) bool {
	return strings.HasPrefix(strings.TrimSpace(path), "/")
}
