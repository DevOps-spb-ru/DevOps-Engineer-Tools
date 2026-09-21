package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// checkSudo проверяет, что утилиты PostgreSQL выполняются под владельцем кластера без
// пароля: это главная зависимость сервиса (в Debian пароль не спрашивают — работа идёт
// через локальный сокет и правило sudoers с фиксированным списком утилит).
func checkSudo(ctx context.Context, report *Report, opts Options) {
	cfg := opts.Cfg.Postgres
	if mode := cfg.Mode; mode != "" && mode != pg.ModeSudo {
		report.add("sudo", LevelOK, fmt.Sprintf("режим %s: sudo не используется", mode), "")
		return
	}
	prefix := cfg.Prefix()
	binary, err := prefix.Binary("pg_dump")
	if err != nil {
		report.add("sudo", LevelError, err.Error(), "")
		return
	}
	process, prefixArgs := prefix.Wrap(binary)
	var stdout bytes.Buffer
	if err := opts.Runner.Run(ctx, process, append(prefixArgs, "--version"), nil, &stdout, io.Discard); err != nil {
		report.add("sudo", LevelError,
			fmt.Sprintf("sudo -n -u %s %s не выполняется: %v", cfg.SudoUser, binary, err),
			sudoHint(cfg))
		return
	}
	version, err := pg.ParsePgVersion(stdout.Bytes())
	if err != nil {
		report.add("sudo", LevelWarn,
			fmt.Sprintf("pg_dump под %s запускается, но версию разобрать не удалось: %v", cfg.SudoUser, err), "")
		return
	}
	report.add("sudo", LevelOK,
		fmt.Sprintf("sudo -n -u %s работает без пароля: pg_dump %s", cfg.SudoUser, version), "")
}

// sudoHint возвращает подсказку про правила sudoers. Список разрешённых утилит
// фиксирован: правило вида «postgres ALL» избыточно и расширяет права сервиса.
// Пути берутся у Prefix — это пути на сервере Linux, а не локальные пути сборки.
func sudoHint(cfg config.PostgresConfig) string {
	prefix := cfg.Prefix()
	binaries := make([]string, 0, len(pg.Tools))
	for _, tool := range pg.Tools {
		binary, err := prefix.Binary(tool)
		if err != nil {
			continue
		}
		binaries = append(binaries, binary)
	}
	return fmt.Sprintf("проверьте /etc/sudoers.d/sqlbrc: %s ALL=(%s) NOPASSWD: %s",
		DefaultServiceUser, cfg.SudoUser, strings.Join(binaries, ", "))
}

// checkPasswordFile проверяет файл пароля режима tcp (PGPASSFILE): утилиты читают
// его при каждом запуске, а права шире 0600 означали бы, что пароль роли видит
// любой пользователь сервера. В режиме sudo проверять нечего: пароль не нужен.
func checkPasswordFile(report *Report, opts Options) {
	cfg := opts.Cfg.Postgres
	if cfg.Mode != pg.ModeTCP {
		return
	}
	path := strings.TrimSpace(cfg.PasswordFile)
	if path == "" {
		report.add("pgpass", LevelError,
			"postgres.password_file не задан: в режиме tcp пароль роли передаётся файлом",
			"создайте файл формата «host:port:база:роль:пароль» (install -m 0600) и укажите путь в postgres.password_file")
		return
	}
	info, err := opts.FS.Stat(path)
	if err != nil {
		report.add("pgpass", LevelError, fmt.Sprintf("%s не читается: %v", path, err),
			fmt.Sprintf("проверьте путь и права: install -m 0600 -o %s -g %s <файл> %s", DefaultServiceUser, DefaultServiceUser, path))
		return
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		report.add("pgpass", LevelError, fmt.Sprintf("%s: режим %04o", path, perm),
			fmt.Sprintf("доступ к паролю роли должен быть только у сервиса: chmod 0600 %s", path))
		return
	}
	report.add("pgpass", LevelOK, fmt.Sprintf("%s: режим %04o", path, info.Mode().Perm()), "")
}

// serverInfo — что удалось узнать о сервере: нужно для сравнения версий клиентов.
type serverInfo struct {
	version pg.Version
	ok      bool
}

// checkServer проверяет доступность сервера и его версию.
func checkServer(ctx context.Context, report *Report, opts Options) serverInfo {
	if err := opts.PG.Ping(ctx); err != nil {
		report.add("server", LevelError, err.Error(),
			"проверьте, что PostgreSQL запущен, а локальный сокет доступен (pg_hba: local all all peer)")
		return serverInfo{}
	}
	version, err := opts.PG.ServerVersion(ctx)
	if err != nil {
		report.add("server", LevelError, err.Error(),
			"проверьте подключение: sudo -n -u postgres psql -c 'show server_version'")
		return serverInfo{}
	}
	report.add("server", LevelOK,
		fmt.Sprintf("сервер PostgreSQL %s доступен через локальный сокет", version), "")
	return serverInfo{version: version, ok: true}
}

// checkClients перечисляет версии утилит PostgreSQL и сравнивает их с сервером.
// Расхождение версий — источник ошибок: дамп, снятый клиентом новее сервера,
// старый pg_restore не читает вовсе.
func checkClients(ctx context.Context, report *Report, opts Options, server serverInfo) {
	versions, err := opts.PG.ClientVersions(ctx)
	if err != nil {
		report.add("clients", LevelError, "утилиты PostgreSQL недоступны: "+err.Error(),
			fmt.Sprintf("установите клиент: apt install postgresql-client-15 (ожидаются файлы в %s)",
				opts.Cfg.Postgres.BinDir))
		return
	}
	listed := formatVersions(versions)
	if !server.ok {
		report.add("clients", LevelOK, listed+"; версию сервера узнать не удалось, сравнение пропущено", "")
		return
	}
	critical, warnings := compareVersions(versions, server.version)
	switch {
	case len(critical) > 0:
		report.add("clients", LevelError, fmt.Sprintf("%s; %s", listed, strings.Join(critical, ", ")),
			"обновите клиент: pg_dump от клиента старее сервера отказывается работать (версии должны совпадать)")
	case len(warnings) > 0:
		report.add("clients", LevelWarn, fmt.Sprintf("%s; %s", listed, strings.Join(warnings, ", ")),
			"клиент новее сервера: свой дамп восстановить получится, но pg_restore этой версии не везде есть")
	default:
		report.add("clients", LevelOK, fmt.Sprintf("%s; версия совпадает с сервером %s", listed, server.version), "")
	}
}

// formatVersions печатает версии утилит в стабильном порядке.
func formatVersions(versions map[string]pg.Version) string {
	listed := make([]string, 0, len(versions))
	for name, version := range versions {
		listed = append(listed, fmt.Sprintf("%s %s", name, version))
	}
	sort.Strings(listed)
	return strings.Join(listed, ", ")
}

// compareVersions возвращает расхождения версий клиента и сервера: критичные
// (клиент старее сервера) и предупреждения (клиент новее сервера).
func compareVersions(versions map[string]pg.Version, server pg.Version) (critical, warnings []string) {
	for _, tool := range pg.Tools {
		version, ok := versions[tool]
		if !ok {
			continue
		}
		switch {
		case version.Major < server.Major:
			critical = append(critical, fmt.Sprintf("%s %s старее сервера %s", tool, version, server))
		case version.Major > server.Major:
			warnings = append(warnings, fmt.Sprintf("%s %s новее сервера %s", tool, version, server))
		}
	}
	sort.Strings(critical)
	sort.Strings(warnings)
	return critical, warnings
}

// checkRole проверяет права роли: без права создавать БД восстановление в нового
// стенда невозможно, а без суперпользователя объекты достанутся служебной роли.
func checkRole(ctx context.Context, report *Report, opts Options) {
	role, err := opts.PG.RoleInfo(ctx)
	if err != nil {
		report.add("role", LevelError, "не удалось получить сведения о роли: "+err.Error(),
			"проверьте подключение: sudo -n -u postgres psql -c 'select current_user'")
		return
	}
	message := fmt.Sprintf("роль %s: superuser=%s, createdb=%s",
		role.Name, yesNo(role.Superuser), yesNo(role.CreateDB))
	switch {
	case role.Name == "":
		report.add("role", LevelError, "сервер не вернул имя роли", "")
	case !role.Superuser && !role.CreateDB:
		report.add("role", LevelError, message+": роль не может создавать базы данных",
			"выдайте право: ALTER ROLE "+role.Name+" CREATEDB; либо работайте под postgres (sudo -n -u postgres)")
	case !role.Superuser:
		report.add("role", LevelWarn, message+": владельцы объектов из дампа применятся только для своих объектов",
			"для переноса владельцев нужен суперпользователь: работайте под postgres (sudo -n -u postgres)")
	default:
		report.add("role", LevelOK, message, "")
	}
}

// yesNo печатает булево значение по-русски.
func yesNo(value bool) string {
	if value {
		return "да"
	}
	return "нет"
}

// checkBackupDir проверяет каталог бэкапов: права, запись и свободное место.
func checkBackupDir(report *Report, opts Options) {
	checkDir(report, opts, "storage-dir", opts.Cfg.Storage.Dir,
		opts.Cfg.Storage.MinFreeSpace.Bytes(), 0o750)
}

// checkStateDir проверяет служебный каталог: в нём журнал задач и аудит,
// поэтому он должен быть закрыт (0700).
func checkStateDir(report *Report, opts Options) {
	checkDir(report, opts, "state-dir", opts.Cfg.Storage.StateDir, 0, 0o700)
}

// checkDir проверяет один каталог и собирает все замечания к нему в один результат:
// администратору важен итог по каталогу, а не пять строк на каждый признак.
func checkDir(report *Report, opts Options, name, path string, minFree int64, wantPerm os.FileMode) {
	info, err := opts.FS.Stat(path)
	if err != nil {
		report.add(name, LevelError, fmt.Sprintf("каталог %s недоступен: %v", path, err), dirHint(path))
		return
	}
	if !info.IsDir() {
		report.add(name, LevelError, fmt.Sprintf("%s существует, но это не каталог", path), dirHint(path))
		return
	}

	level := LevelOK
	hints := make([]string, 0, 2)
	message := fmt.Sprintf("%s: режим %04o", path, info.Mode().Perm())
	if owner := opts.FS.Owner(info); owner != "" {
		message += ", владелец " + owner
	}
	if perm := info.Mode().Perm(); perm&0o002 != 0 {
		level = LevelWarn
		hints = append(hints, fmt.Sprintf("закройте доступ на запись всем: chmod %04o %s", wantPerm, path))
	} else if perm != wantPerm {
		hints = append(hints, fmt.Sprintf("ожидается режим %04o: chmod %04o %s", wantPerm, wantPerm, path))
	}

	if err := opts.FS.WriteProbe(path); err != nil {
		level = LevelError
		message += fmt.Sprintf("; запись от имени сервиса недоступна: %v", err)
		hints = append(hints, "проверьте права пользователя сервиса и ReadWritePaths в unit-файле systemd")
	}

	free, total, err := opts.FS.FreeSpace(path)
	switch {
	case err == nil && minFree > 0 && free < minFree:
		level = LevelError
		message += fmt.Sprintf("; свободно %s из %s, порог %s",
			bytesize.HumanSize(free), bytesize.HumanSize(total), bytesize.HumanSize(minFree))
		hints = append(hints, "освободите место или снизьте storage.min_free_space")
	case err == nil:
		message += fmt.Sprintf("; свободно %s из %s", bytesize.HumanSize(free), bytesize.HumanSize(total))
	case errors.Is(err, ErrUnsupported):
		message += "; свободное место на этой платформе не проверяется"
	default:
		if level == LevelOK {
			level = LevelWarn
		}
		message += fmt.Sprintf("; свободное место: %v", err)
	}
	report.add(name, level, message, strings.Join(hints, "; "))
}

// dirHint возвращает подсказку по созданию каталога с правами пользователя сервиса.
func dirHint(path string) string {
	return fmt.Sprintf("создайте каталог: install -d -m 0750 -o %s -g %s %s",
		DefaultServiceUser, DefaultServiceUser, path)
}

// checkLocales проверяет, что локали существующих БД есть в системе: восстановление
// в новую БД выполняется с CREATE DATABASE ... LC_COLLATE, и отсутствующая локаль
// валит восстановление уже после того, как место под дамп потрачено.
func checkLocales(ctx context.Context, report *Report, opts Options) {
	databases, err := opts.PG.ListDatabases(ctx)
	if err != nil {
		report.add("locales", LevelWarn, "список баз недоступен, проверка локалей пропущена: "+err.Error(), "")
		return
	}
	path, err := opts.Runner.LookPath("locale")
	if err != nil {
		report.add("locales", LevelWarn, "утилита locale не найдена: проверка локалей пропущена",
			"установите пакет locales: apt install locales")
		return
	}
	out, err := opts.Runner.Output(ctx, path, []string{"--all"})
	if err != nil {
		report.add("locales", LevelWarn, "не удалось получить список локалей: "+err.Error(), "")
		return
	}
	available := make(map[string]bool, 32)
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			available[name] = true
		}
	}
	missing, checked := missingLocales(databases, available)
	if len(missing) > 0 {
		report.add("locales", LevelWarn, fmt.Sprintf("в системе нет локалей: %s", strings.Join(missing, ", ")),
			"восстановление в новую БД упадёт на CREATE DATABASE ... LC_COLLATE: сгенерируйте локаль (dpkg-reconfigure locales) или используйте C.UTF-8")
		return
	}
	report.add("locales", LevelOK,
		fmt.Sprintf("проверено локалей %d по %d базам: все есть в системе", checked, len(databases)), "")
}

// missingLocales возвращает локали баз, которых нет в системе, и их общее число.
// Локали C и POSIX есть всегда, поэтому не проверяются.
func missingLocales(databases []pg.Database, available map[string]bool) (missing []string, checked int) {
	seen := make(map[string]bool, 8)
	for _, database := range databases {
		for _, locale := range []string{database.Collate, database.Ctype} {
			if locale == "" || locale == "C" || locale == "POSIX" || seen[locale] {
				continue
			}
			seen[locale] = true
			checked++
			if !available[locale] {
				missing = append(missing, locale)
			}
		}
	}
	sort.Strings(missing)
	return missing, checked
}

// checkArchiveAccess проверяет, что пользователь postgres читает файлы бэкапов.
// Отдельная проверка нужна из-за AppArmor в Debian: профиль postgres запрещает
// доступ за пределы /var/lib/postgresql, и отказ виден только на чтении файла,
// хотя права каталога выглядят корректно.
func checkArchiveAccess(ctx context.Context, report *Report, opts Options) {
	archive, ok := newestArchive(opts.FS, opts.Cfg.Storage.Dir)
	if !ok {
		report.add("archive-access", LevelWarn,
			"в каталоге бэкапов нет файлов *.dump: проверка чтения архива пользователем postgres пропущена", "")
		return
	}
	prefix := opts.Cfg.Postgres.Prefix()
	binary, err := prefix.Binary("pg_restore")
	if err != nil {
		report.add("archive-access", LevelError, err.Error(), "")
		return
	}
	process, prefixArgs := prefix.Wrap(binary)
	args := append(append([]string{}, prefixArgs...), "--list", archive)
	var stdout bytes.Buffer
	if err := opts.Runner.Run(ctx, process, args, nil, &stdout, io.Discard); err != nil {
		report.add("archive-access", LevelError,
			fmt.Sprintf("пользователь %s не может прочитать %s: %v", opts.Cfg.Postgres.SudoUser, archive, err),
			"проверьте AppArmor: dmesg | grep -i denied; aa-status; добавьте доступ к "+opts.Cfg.Storage.Dir+
				" в /etc/apparmor.d/local/usr.lib.postgresql.*")
		return
	}
	entries, err := pg.ParseRestoreList(stdout.Bytes())
	if err != nil {
		report.add("archive-access", LevelWarn,
			fmt.Sprintf("pg_restore --list прочитал %s, но оглавление разобрать не удалось: %v", archive, err), "")
		return
	}
	report.add("archive-access", LevelOK,
		fmt.Sprintf("pg_restore под %s читает %s: объектов в архиве %d",
			opts.Cfg.Postgres.SudoUser, filepath.Base(archive), len(entries)), "")
}

// checkUnit проверяет наличие юнита systemd: без него сервис не поднимется после
// перезагрузки, а бэкапы по расписанию не будут запускаться.
func checkUnit(report *Report, opts Options) {
	for _, path := range opts.UnitPaths {
		if _, err := opts.FS.Stat(path); err == nil {
			report.add("systemd-unit", LevelOK, "юнит найден: "+path, "")
			return
		}
	}
	report.add("systemd-unit", LevelWarn,
		fmt.Sprintf("юнит systemd не найден (%s): сервис запущен вручную или из исходников",
			strings.Join(opts.UnitPaths, ", ")),
		"в 0.2.0 юнит в поставку не входит: добавьте его сами и включите — systemctl enable --now sqlbrc")
}

// checkWeb проверяет безопасность веб-интерфейса. Интерфейс слушает LAN без
// reverse-proxy, поэтому TLS или ограничение подсети — обязательные условия.
func checkWeb(report *Report, opts Options) {
	cfg := opts.Cfg
	if len(cfg.Auth.Users) == 0 && strings.TrimSpace(cfg.Auth.TokenFile) == "" {
		report.add("web-security", LevelError,
			"не задан ни один пользователь и нет файла токенов: интерфейс был бы открыт всем",
			"создайте пользователя: sqlbrc hash-password --login admin")
		return
	}
	host, _, err := net.SplitHostPort(cfg.Server.Listen)
	exposed := err != nil || !config.IsLoopbackHost(host)
	details := fmt.Sprintf("listen %s, пользователей %d, TLS %s",
		cfg.Server.Listen, len(cfg.Auth.Users), tlsState(cfg.Server.TLS.Enabled()))
	switch {
	case cfg.Server.TLS.Enabled():
		report.add("web-security", LevelOK, details, "")
	case !exposed:
		report.add("web-security", LevelOK, details+": адрес доступен только с сервера", "")
	case cfg.Server.AllowInsecure:
		report.add("web-security", LevelWarn, details+": трафик идёт без шифрования",
			"ограничьте доступ к порту на межсетевом экране (nftables) или настройте server.tls")
	default:
		report.add("web-security", LevelError, details+": интерфейс слушает не loopback без TLS",
			"настройте server.tls или осознанно включите server.allow_insecure")
	}
}

// tlsState печатает состояние TLS словами: строка попадает в отчёт doctor.
func tlsState(enabled bool) string {
	if enabled {
		return "включён"
	}
	return "выключен"
}

// checkRetention предупреждает о базах без свежих бэкапов: политика хранения
// бессмысленна, если дампы никто не снимает.
func checkRetention(ctx context.Context, report *Report, opts Options) {
	databases, err := opts.PG.ListDatabases(ctx)
	if err != nil {
		report.add("retention", LevelWarn,
			"список баз недоступен, проверка свежести бэкапов пропущена: "+err.Error(), "")
		return
	}
	managed := make([]pg.Database, 0, len(databases))
	for _, database := range databases {
		if opts.Cfg.Databases.Allowed(database.Name) {
			managed = append(managed, database)
		}
	}
	if len(managed) == 0 {
		report.add("retention", LevelWarn,
			fmt.Sprintf("по шаблону %s не найдено ни одной базы среди %d баз кластера",
				opts.Cfg.Databases.Pattern, len(databases)),
			"проверьте databases.pattern: от него зависит список обслуживаемых баз")
		return
	}
	limit := time.Duration(opts.Cfg.Storage.KeepDays) * 24 * time.Hour
	now := opts.Now()
	stale := make([]string, 0, len(managed))
	for _, database := range managed {
		created, ok := newestArchiveTime(opts.FS, opts.Cfg.Storage.Dir, database.Name)
		switch {
		case !ok:
			stale = append(stale, database.Name+": бэкапов нет")
		case now.Sub(created) > limit:
			stale = append(stale, fmt.Sprintf("%s: последний бэкап %s", database.Name, humanAge(now.Sub(created))))
		}
	}
	if len(stale) > 0 {
		report.add("retention", LevelWarn,
			fmt.Sprintf("нет свежих бэкапов (%d из %d баз, порог %d дней): %s",
				len(stale), len(managed), opts.Cfg.Storage.KeepDays, strings.Join(stale, ", ")),
			"снимите бэкап вручную (sqlbrc backup --db <имя>) или поставьте команду в cron: таймер бэкапов появится в 0.3.0")
		return
	}
	report.add("retention", LevelOK,
		fmt.Sprintf("у всех %d обслуживаемых баз есть бэкап не старше %d дней",
			len(managed), opts.Cfg.Storage.KeepDays), "")
}

// newestArchiveIn возвращает самый свежий файл *.dump в одном каталоге.
func newestArchiveIn(fs FS, dir string) (string, time.Time, bool) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return "", time.Time{}, false
	}
	path := ""
	var mod time.Time
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".dump") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if path == "" || info.ModTime().After(mod) {
			path = filepath.Join(dir, entry.Name())
			mod = info.ModTime()
		}
	}
	return path, mod, path != ""
}

// newestArchive ищет самый свежий архив в каталоге бэкапов и его подкаталогах:
// раскладка — <каталог>/<бд>/<метка времени>.dump.
func newestArchive(fs FS, dir string) (string, bool) {
	path, mod, found := newestArchiveIn(fs, dir)
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return path, found
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		nested, nestedMod, nestedFound := newestArchiveIn(fs, filepath.Join(dir, entry.Name()))
		if !nestedFound {
			continue
		}
		if !found || nestedMod.After(mod) {
			path, mod, found = nested, nestedMod, true
		}
	}
	return path, found
}

// newestArchiveTime возвращает время самого свежего бэкапа базы.
func newestArchiveTime(fs FS, dir, database string) (time.Time, bool) {
	_, mod, found := newestArchiveIn(fs, filepath.Join(dir, database))
	return mod, found
}

// humanAge форматирует возраст бэкапа в удобочитаемом виде.
func humanAge(age time.Duration) string {
	switch {
	case age < time.Hour:
		return fmt.Sprintf("%d мин назад", int(age.Minutes()))
	case age < 48*time.Hour:
		return fmt.Sprintf("%d ч назад", int(age.Hours()))
	default:
		return fmt.Sprintf("%d дн назад", int(age.Hours()/24))
	}
}
