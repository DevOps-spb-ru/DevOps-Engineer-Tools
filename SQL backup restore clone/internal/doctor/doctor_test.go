package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// healthyOptions собирает окружение, в котором все проверки должны пройти:
// каталоги с правами, свежий бэкап, юнит systemd, локали и pg_restore под postgres.
func healthyOptions(t *testing.T) Options {
	t.Helper()
	cfg := testConfig()

	fs := newFakeFS()
	fs.addDir(cfg.Storage.Dir, 0o750)
	fs.addDir(cfg.Storage.StateDir, 0o700)
	fs.addDir(filepath.Join(cfg.Storage.Dir, "fse-1234"), 0o750)
	fs.addFile(filepath.Join(cfg.Storage.Dir, "fse-1234", "20260921T020000Z.dump"), testNow.Add(-2*time.Hour))
	fs.addFile("/etc/systemd/system/sqlbrc.service", testNow.Add(-24*time.Hour))

	runner := newFakeRunner()
	runner.stdout["locale"] = "C\nC.UTF-8\nen_US.UTF-8\nru_RU.UTF-8\n"
	runner.stdout["pg_restore"] = "; Archive created at 2026-09-21\n" +
		"218; 1259 16409 TABLE public alembic_version\n"

	return Options{
		Cfg:        cfg,
		ConfigPath: "/etc/sqlbrc/config.yaml",
		PG:         newFakePG(),
		FS:         fs,
		Runner:     runner,
		Now:        func() time.Time { return testNow },
	}
}

// checkByName возвращает результат проверки по имени.
func checkByName(t *testing.T, report Report, name string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("в отчёте нет проверки %q: %+v", name, report.Checks)
	return Check{}
}

// names возвращает имена проверок в порядке выполнения.
func names(report Report) []string {
	listed := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		listed = append(listed, check.Name)
	}
	return listed
}

// TestRunHealthy проверяет, что на подготовленном сервере прогон не находит ошибок
// и проходит все проверки: список имён фиксирован, чтобы проверка не пропала молча.
func TestRunHealthy(t *testing.T) {
	report := Run(context.Background(), healthyOptions(t))
	if report.Errors() != 0 {
		t.Fatalf("на подготовленном сервере найдены ошибки: %+v", report.Checks)
	}
	if report.ExitCode() != 0 {
		t.Errorf("код возврата = %d, ожидался 0", report.ExitCode())
	}
	if report.GeneratedAt != testNow {
		t.Errorf("время отчёта = %s, ожидалось %s", report.GeneratedAt, testNow)
	}
	if report.ConfigPath != "/etc/sqlbrc/config.yaml" {
		t.Errorf("путь к конфигу в отчёте = %q", report.ConfigPath)
	}
	want := []string{
		"config", "sudo", "server", "clients", "role", "storage-dir", "state-dir",
		"locales", "archive-access", "systemd-unit", "web-security", "retention",
	}
	if got := names(report); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("проверки = %v, ожидались %v", got, want)
	}
	if summary := report.Summary(); !strings.Contains(summary, "ошибок 0") {
		t.Errorf("сводка = %q", summary)
	}
}

// TestRunInvalidConfig проверяет, что при некорректном конфиге дальше не идём:
// остальные проверки бессмысленны и только запутают администратора.
func TestRunInvalidConfig(t *testing.T) {
	opts := healthyOptions(t)
	opts.Cfg.Storage.KeepLast = 0

	report := Run(context.Background(), opts)
	if len(report.Checks) != 1 {
		t.Fatalf("проверок %d, ожидалась одна: %+v", len(report.Checks), report.Checks)
	}
	check := checkByName(t, report, "config")
	if check.Level != LevelError || !strings.Contains(check.Message, "keep_last") {
		t.Errorf("проверка конфига: %+v", check)
	}
	if check.Hint == "" {
		t.Error("у ошибки конфига нет подсказки")
	}
	if report.ExitCode() != 1 {
		t.Errorf("код возврата = %d, ожидался 1", report.ExitCode())
	}
}

// TestRunSudoFailure проверяет, что отказ sudo виден как ошибка с подсказкой про
// sudoers: это самая частая причина «сервис не работает» на новом сервере.
func TestRunSudoFailure(t *testing.T) {
	opts := healthyOptions(t)
	opts.Runner.(*fakeRunner).failures["pg_dump"] = true

	report := Run(context.Background(), opts)
	check := checkByName(t, report, "sudo")
	if check.Level != LevelError {
		t.Fatalf("уровень проверки sudo = %q, ожидался %q", check.Level, LevelError)
	}
	if !strings.Contains(check.Hint, "sudoers.d/sqlbrc") {
		t.Errorf("подсказка не про правила sudoers: %q", check.Hint)
	}
	if !strings.Contains(check.Hint, "/usr/bin/pg_dump") {
		t.Errorf("в подсказке нет пути к утилите: %q", check.Hint)
	}
	if report.ExitCode() != 1 {
		t.Errorf("код возврата = %d, ожидался 1", report.ExitCode())
	}
}

// TestRunClientToolMissing проверяет, что отсутствие утилит PostgreSQL — ошибка
// с подсказкой про пакет postgresql-client-15.
func TestRunClientToolMissing(t *testing.T) {
	opts := healthyOptions(t)
	opts.PG.(*fakePG).clientsErr = errors.New("pg_dump: /usr/bin/pg_dump не найден")

	report := Run(context.Background(), opts)
	check := checkByName(t, report, "clients")
	if check.Level != LevelError || !strings.Contains(check.Hint, "postgresql-client-15") {
		t.Errorf("проверка клиентов: %+v", check)
	}
	// Сервер при этом доступен, поэтому его проверка остаётся успешной.
	if server := checkByName(t, report, "server"); server.Level != LevelOK {
		t.Errorf("сервер должен проверяться независимо: %+v", server)
	}
}

// TestRunServerUnavailable проверяет, что недоступный сервер — ошибка с подсказкой
// про локальный сокет и pg_hba, а не молчаливое «всё хорошо».
func TestRunServerUnavailable(t *testing.T) {
	opts := healthyOptions(t)
	opts.PG.(*fakePG).pingErr = errors.New("сервер PostgreSQL недоступен: exit status 2")

	report := Run(context.Background(), opts)
	check := checkByName(t, report, "server")
	if check.Level != LevelError || !strings.Contains(check.Hint, "pg_hba") {
		t.Errorf("проверка сервера: %+v", check)
	}
	if role := checkByName(t, report, "role"); role.Level != LevelOK {
		t.Errorf("проверки роли и сервера должны быть независимы: %+v", role)
	}
}

// TestRunClientVersionMismatch проверяет сравнение версий клиента и сервера:
// клиент старее — ошибка, новее — предупреждение.
func TestRunClientVersionMismatch(t *testing.T) {
	tests := []struct {
		name      string
		major     int
		wantLevel Level
	}{
		{name: "клиент старее сервера", major: 14, wantLevel: LevelError},
		{name: "клиент новее сервера", major: 16, wantLevel: LevelWarn},
		{name: "версии совпадают", major: 15, wantLevel: LevelOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := healthyOptions(t)
			pgFake := opts.PG.(*fakePG)
			for tool := range pgFake.clients {
				pgFake.clients[tool] = pg.Version{Major: test.major}
			}
			check := checkByName(t, Run(context.Background(), opts), "clients")
			if check.Level != test.wantLevel {
				t.Errorf("уровень = %q, ожидался %q (%s)", check.Level, test.wantLevel, check.Message)
			}
		})
	}
}

// TestRunRoleProblems проверяет права роли: без CREATEDB восстановление в нового
// стенда невозможно, а без суперпользователя не переносятся владельцы объектов.
func TestRunRoleProblems(t *testing.T) {
	tests := []struct {
		name      string
		role      pg.Role
		roleErr   error
		wantLevel Level
		wantHint  string
	}{
		{
			name:      "суперпользователь",
			role:      pg.Role{Name: "postgres", Superuser: true, CreateDB: true},
			wantLevel: LevelOK,
		},
		{
			name:      "роль без права создавать базы",
			role:      pg.Role{Name: "postgres"},
			wantLevel: LevelError,
			wantHint:  "ALTER ROLE",
		},
		{
			name:      "роль с CREATEDB без суперпользователя",
			role:      pg.Role{Name: "postgres", CreateDB: true},
			wantLevel: LevelWarn,
			wantHint:  "суперпользователь",
		},
		{name: "пустое имя роли", role: pg.Role{}, wantLevel: LevelError},
		{
			name:      "ошибка запроса о роли",
			roleErr:   errors.New("psql не найден"),
			wantLevel: LevelError,
			wantHint:  "select current_user",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := healthyOptions(t)
			pgFake := opts.PG.(*fakePG)
			pgFake.role = test.role
			pgFake.roleErr = test.roleErr

			check := checkByName(t, Run(context.Background(), opts), "role")
			if check.Level != test.wantLevel {
				t.Errorf("уровень = %q, ожидался %q (%s)", check.Level, test.wantLevel, check.Message)
			}
			if test.wantHint != "" && !strings.Contains(check.Hint, test.wantHint) {
				t.Errorf("в подсказке нет %q: %q", test.wantHint, check.Hint)
			}
		})
	}
}

// TestRunStorageProblems проверяет каталоги бэкапов: отсутствие, права, запись
// и свободное место. Порог места — главная защита от битого дампа.
func TestRunStorageProblems(t *testing.T) {
	t.Run("каталог отсутствует", func(t *testing.T) {
		opts := healthyOptions(t)
		fsFake := opts.FS.(*fakeFS)
		delete(fsFake.files, opts.Cfg.Storage.Dir)
		delete(fsFake.files, filepath.Join(opts.Cfg.Storage.Dir, "fse-1234"))

		check := checkByName(t, Run(context.Background(), opts), "storage-dir")
		if check.Level != LevelError || !strings.Contains(check.Hint, "install -d") {
			t.Errorf("проверка каталога: %+v", check)
		}
	})

	t.Run("каталог открыт на запись всем", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.FS.(*fakeFS).addDir(opts.Cfg.Storage.Dir, 0o777)

		check := checkByName(t, Run(context.Background(), opts), "storage-dir")
		if check.Level != LevelWarn || !strings.Contains(check.Hint, "chmod 0750") {
			t.Errorf("проверка прав каталога: %+v", check)
		}
	})

	t.Run("нет места", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.FS.(*fakeFS).free = 1 * 1000 * 1000 * 1000

		check := checkByName(t, Run(context.Background(), opts), "storage-dir")
		if check.Level != LevelError || !strings.Contains(check.Message, "порог") {
			t.Errorf("проверка свободного места: %+v", check)
		}
	})

	t.Run("запись запрещена", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.FS.(*fakeFS).writeErr[opts.Cfg.Storage.Dir] = errors.New("permission denied")

		check := checkByName(t, Run(context.Background(), opts), "storage-dir")
		if check.Level != LevelError || !strings.Contains(check.Message, "permission denied") {
			t.Errorf("проверка записи: %+v", check)
		}
		if !strings.Contains(check.Hint, "ReadWritePaths") {
			t.Errorf("в подсказке нет про ReadWritePaths: %q", check.Hint)
		}
	})

	t.Run("свободное место не проверяется на платформе", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.FS.(*fakeFS).freeErr = ErrUnsupported

		check := checkByName(t, Run(context.Background(), opts), "storage-dir")
		if check.Level != LevelOK || !strings.Contains(check.Message, "не проверяется") {
			t.Errorf("проверка без statfs: %+v", check)
		}
	})

	t.Run("вместо каталога файл", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.FS.(*fakeFS).files[opts.Cfg.Storage.Dir] = fakeFile{name: "", mode: 0o640}

		check := checkByName(t, Run(context.Background(), opts), "storage-dir")
		if check.Level != LevelError || !strings.Contains(check.Message, "не каталог") {
			t.Errorf("проверка типа файла: %+v", check)
		}
	})
}

// TestRunLocales проверяет сравнение локалей баз со списком системы: отсутствующая
// локаль ломает восстановление в новую базу уже после снятия дампа.
func TestRunLocales(t *testing.T) {
	t.Run("локаль отсутствует", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.Runner.(*fakeRunner).stdout["locale"] = "C\nC.UTF-8\n"

		check := checkByName(t, Run(context.Background(), opts), "locales")
		if check.Level != LevelWarn || !strings.Contains(check.Message, "en_US.UTF-8") {
			t.Errorf("проверка локалей: %+v", check)
		}
	})

	t.Run("утилита locale не найдена", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.Runner.(*fakeRunner).missingBin["locale"] = true

		check := checkByName(t, Run(context.Background(), opts), "locales")
		if check.Level != LevelWarn || !strings.Contains(check.Message, "locale") {
			t.Errorf("проверка отсутствующей утилиты locale: %+v", check)
		}
	})

	t.Run("утилита locale вернула ошибку", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.Runner.(*fakeRunner).failures["locale"] = true

		check := checkByName(t, Run(context.Background(), opts), "locales")
		if check.Level != LevelWarn || !strings.Contains(check.Message, "список локалей") {
			t.Errorf("проверка ошибки locale: %+v", check)
		}
	})

	t.Run("локали C не проверяются", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.PG.(*fakePG).databases = []pg.Database{{Name: "fse-1", Collate: "C", Ctype: "C", AllowConn: true}}
		opts.Runner.(*fakeRunner).stdout["locale"] = "C\n"

		check := checkByName(t, Run(context.Background(), opts), "locales")
		if check.Level != LevelOK {
			t.Errorf("проверка локали C: %+v", check)
		}
	})

	t.Run("список баз недоступен", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.PG.(*fakePG).databasesErr = errors.New("psql не найден")

		check := checkByName(t, Run(context.Background(), opts), "locales")
		if check.Level != LevelWarn || !strings.Contains(check.Message, "пропущена") {
			t.Errorf("проверка без списка баз: %+v", check)
		}
	})
}

// TestRunArchiveAccess проверяет чтение архива пользователем postgres: так ловятся
// отказы AppArmor, которые не видны по правам каталога.
func TestRunArchiveAccess(t *testing.T) {
	t.Run("архив читается", func(t *testing.T) {
		opts := healthyOptions(t)
		check := checkByName(t, Run(context.Background(), opts), "archive-access")
		if check.Level != LevelOK || !strings.Contains(check.Message, "объектов в архиве 1") {
			t.Errorf("проверка доступа к архиву: %+v", check)
		}
		if !opts.Runner.(*fakeRunner).ran("--list") {
			t.Error("pg_restore не вызывался с ключом --list")
		}
	})

	t.Run("AppArmor запретил чтение", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.Runner.(*fakeRunner).failures["pg_restore"] = true

		check := checkByName(t, Run(context.Background(), opts), "archive-access")
		if check.Level != LevelError || !strings.Contains(check.Hint, "AppArmor") {
			t.Errorf("проверка отказа доступа: %+v", check)
		}
	})

	t.Run("бэкапов нет", func(t *testing.T) {
		opts := healthyOptions(t)
		fsFake := opts.FS.(*fakeFS)
		fsFake.files = map[string]fakeFile{
			opts.Cfg.Storage.Dir:      {name: "", mode: 0o750 | fs.ModeDir, isDir: true},
			opts.Cfg.Storage.StateDir: {name: "sqlbrc", mode: 0o700 | fs.ModeDir, isDir: true},
		}

		check := checkByName(t, Run(context.Background(), opts), "archive-access")
		if check.Level != LevelWarn || !strings.Contains(check.Message, "нет файлов") {
			t.Errorf("проверка без бэкапов: %+v", check)
		}
	})

	t.Run("оглавление не разобрано", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.Runner.(*fakeRunner).stdout["pg_restore"] = "; Archive created at 2026-09-21\n"

		check := checkByName(t, Run(context.Background(), opts), "archive-access")
		if check.Level != LevelWarn || !strings.Contains(check.Message, "оглавление") {
			t.Errorf("проверка неразобранного оглавления: %+v", check)
		}
	})
}

// TestRunUnit проверяет предупреждение о запуске без юнита systemd: иначе сервис
// не поднимется после перезагрузки и бэкапы по расписанию не заработают.
func TestRunUnit(t *testing.T) {
	opts := healthyOptions(t)
	delete(opts.FS.(*fakeFS).files, "/etc/systemd/system/sqlbrc.service")

	check := checkByName(t, Run(context.Background(), opts), "systemd-unit")
	if check.Level != LevelWarn || !strings.Contains(check.Message, "не найден") {
		t.Errorf("проверка юнита: %+v", check)
	}
	if !strings.Contains(check.Hint, "systemctl") {
		t.Errorf("в подсказке нет команды включения юнита: %q", check.Hint)
	}

	// Юнит в /lib/systemd/system тоже считается найденным.
	opts = healthyOptions(t)
	delete(opts.FS.(*fakeFS).files, "/etc/systemd/system/sqlbrc.service")
	opts.FS.(*fakeFS).addFile("/lib/systemd/system/sqlbrc.service", testNow)
	if check := checkByName(t, Run(context.Background(), opts), "systemd-unit"); check.Level != LevelOK {
		t.Errorf("юнит из /lib/systemd/system не найден: %+v", check)
	}
}

// TestRunWebSecurity проверяет безопасность веб-интерфейса: он слушает LAN без
// reverse-proxy, поэтому либо TLS, либо ограничение подсети обязательны.
func TestRunWebSecurity(t *testing.T) {
	tests := []struct {
		name      string
		change    func(*config.Configuration)
		wantLevel Level
		wantHint  string
	}{
		{
			name:      "по умолчанию без TLS с allow_insecure",
			wantLevel: LevelWarn,
			wantHint:  "nftables",
		},
		{
			name: "только loopback",
			change: func(c *config.Configuration) {
				c.Server.Listen = "127.0.0.1:8088"
				c.Server.AllowInsecure = false
			},
			wantLevel: LevelOK,
		},
		{
			name: "TLS настроен",
			change: func(c *config.Configuration) {
				c.Server.TLS = config.TLSConfig{CertFile: "/etc/sqlbrc/tls.crt", KeyFile: "/etc/sqlbrc/tls.key"}
			},
			wantLevel: LevelOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := healthyOptions(t)
			if test.change != nil {
				test.change(&opts.Cfg)
			}
			check := checkByName(t, Run(context.Background(), opts), "web-security")
			if check.Level != test.wantLevel {
				t.Errorf("уровень = %q, ожидался %q (%s)", check.Level, test.wantLevel, check.Message)
			}
			if test.wantHint != "" && !strings.Contains(check.Hint, test.wantHint) {
				t.Errorf("в подсказке нет %q: %q", test.wantHint, check.Hint)
			}
		})
	}
}

// TestCheckWeb проверяет проверку безопасности напрямую: комбинации без TLS и без
// пользователей отвергает уже Validate, поэтому до прогона они не доходят —
// здесь checkWeb проверяется как вторая линия защиты.
func TestCheckWeb(t *testing.T) {
	tests := []struct {
		name      string
		change    func(*config.Configuration)
		wantLevel Level
		wantHint  string
	}{
		{
			name:      "интерфейс открыт без TLS и без разрешения",
			change:    func(c *config.Configuration) { c.Server.AllowInsecure = false },
			wantLevel: LevelError,
			wantHint:  "allow_insecure",
		},
		{
			name:      "нет пользователей и токенов",
			change:    func(c *config.Configuration) { c.Auth.Users = nil; c.Auth.TokenFile = "" },
			wantLevel: LevelError,
			wantHint:  "hash-password",
		},
		{
			name:      "токены вместо пользователей",
			change:    func(c *config.Configuration) { c.Auth.Users = nil; c.Auth.TokenFile = "/etc/sqlbrc/tokens" },
			wantLevel: LevelWarn,
		},
		{
			name:      "адрес не разобран",
			change:    func(c *config.Configuration) { c.Server.Listen = "не-адрес" },
			wantLevel: LevelWarn,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := healthyOptions(t)
			test.change(&opts.Cfg)

			var report Report
			checkWeb(&report, opts)
			if len(report.Checks) != 1 {
				t.Fatalf("проверок %d, ожидалась одна", len(report.Checks))
			}
			check := report.Checks[0]
			if check.Level != test.wantLevel {
				t.Errorf("уровень = %q, ожидался %q (%s)", check.Level, test.wantLevel, check.Message)
			}
			if test.wantHint != "" && !strings.Contains(check.Hint, test.wantHint) {
				t.Errorf("в подсказке нет %q: %q", test.wantHint, check.Hint)
			}
		})
	}
}

// TestRunRetention проверяет свежесть бэкапов обслуживаемых баз: политика хранения
// бессмысленна, если дампы не снимаются.
func TestRunRetention(t *testing.T) {
	fresh := func(add func(fs *fakeFS)) Options {
		opts := healthyOptions(t)
		add(opts.FS.(*fakeFS))
		return opts
	}

	t.Run("свежий бэкап", func(t *testing.T) {
		check := checkByName(t, Run(context.Background(), fresh(func(*fakeFS) {})), "retention")
		if check.Level != LevelOK {
			t.Errorf("проверка свежести: %+v", check)
		}
	})

	t.Run("устаревший бэкап", func(t *testing.T) {
		opts := healthyOptions(t)
		dir := filepath.Join(opts.Cfg.Storage.Dir, "fse-1234")
		fsFake := opts.FS.(*fakeFS)
		delete(fsFake.files, filepath.Join(dir, "20260921T020000Z.dump"))
		fsFake.addFile(filepath.Join(dir, "20260901T020000Z.dump"), testNow.Add(-20*24*time.Hour))

		check := checkByName(t, Run(context.Background(), opts), "retention")
		if check.Level != LevelWarn {
			t.Fatalf("проверка устаревшего бэкапа: %+v", check)
		}
		if !strings.Contains(check.Message, "fse-1234") || !strings.Contains(check.Message, "дн назад") {
			t.Errorf("в сообщении нет имени базы и возраста бэкапа: %q", check.Message)
		}
	})

	t.Run("бэкапов нет", func(t *testing.T) {
		opts := healthyOptions(t)
		dir := filepath.Join(opts.Cfg.Storage.Dir, "fse-1234")
		delete(opts.FS.(*fakeFS).files, filepath.Join(dir, "20260921T020000Z.dump"))

		check := checkByName(t, Run(context.Background(), opts), "retention")
		if check.Level != LevelWarn || !strings.Contains(check.Message, "бэкапов нет") {
			t.Errorf("проверка отсутствия бэкапов: %+v", check)
		}
	})

	t.Run("базы по шаблону не найдены", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.PG.(*fakePG).databases = []pg.Database{{Name: "postgres", AllowConn: true}}

		check := checkByName(t, Run(context.Background(), opts), "retention")
		if check.Level != LevelWarn || !strings.Contains(check.Message, "по шаблону") {
			t.Errorf("проверка шаблона: %+v", check)
		}
	})

	t.Run("список баз недоступен", func(t *testing.T) {
		opts := healthyOptions(t)
		opts.PG.(*fakePG).databasesErr = errors.New("psql не найден")

		check := checkByName(t, Run(context.Background(), opts), "retention")
		if check.Level != LevelWarn || !strings.Contains(check.Message, "пропущена") {
			t.Errorf("проверка без списка баз: %+v", check)
		}
	})
}

// TestReportCounts проверяет подсчёт уровней и код возврата: код 1 возвращается
// только при ошибках, предупреждения на код не влияют.
func TestReportCounts(t *testing.T) {
	report := Report{Checks: []Check{
		{Name: "config", Level: LevelOK},
		{Name: "locales", Level: LevelWarn},
		{Name: "storage-dir", Level: LevelError},
		{Name: "role", Level: LevelWarn},
	}}
	if report.Errors() != 1 {
		t.Errorf("ошибок %d, ожидалась 1", report.Errors())
	}
	if report.Warnings() != 2 {
		t.Errorf("предупреждений %d, ожидалось 2", report.Warnings())
	}
	if report.ExitCode() != 1 {
		t.Errorf("код возврата = %d, ожидался 1", report.ExitCode())
	}

	warningsOnly := Report{Checks: []Check{{Name: "locales", Level: LevelWarn}}}
	if warningsOnly.ExitCode() != 0 {
		t.Errorf("при одних предупреждениях код возврата = %d, ожидался 0", warningsOnly.ExitCode())
	}
	if summary := warningsOnly.Summary(); !strings.Contains(summary, "предупреждений 1") {
		t.Errorf("сводка = %q", summary)
	}
}

// TestReportWrite проверяет форматы отчёта: таблица для человека и JSON для скриптов.
func TestReportWrite(t *testing.T) {
	report := Report{
		GeneratedAt: testNow,
		ConfigPath:  "/etc/sqlbrc/config.yaml",
		Checks: []Check{
			{Name: "config", Level: LevelOK, Message: "конфиг корректен"},
			{Name: "storage-dir", Level: LevelError, Message: "каталог недоступен", Hint: "создайте каталог"},
		},
	}

	t.Run("таблица", func(t *testing.T) {
		var buffer bytes.Buffer
		if err := report.Write(&buffer, FormatTable); err != nil {
			t.Fatalf("неожиданная ошибка: %v", err)
		}
		for _, want := range []string{"УРОВЕНЬ", "ПРОВЕРКА", "config", "storage-dir", "→ создайте каталог", "ошибок 1"} {
			if !strings.Contains(buffer.String(), want) {
				t.Errorf("в таблице нет %q:\n%s", want, buffer.String())
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		var buffer bytes.Buffer
		if err := report.Write(&buffer, FormatJSON); err != nil {
			t.Fatalf("неожиданная ошибка: %v", err)
		}
		var decoded Report
		if err := json.Unmarshal(buffer.Bytes(), &decoded); err != nil {
			t.Fatalf("отчёт не разобран как JSON: %v\n%s", err, buffer.String())
		}
		if len(decoded.Checks) != 2 || decoded.Checks[1].Hint != "создайте каталог" {
			t.Errorf("JSON-отчёт потерял данные: %+v", decoded)
		}
		if decoded.ConfigPath != report.ConfigPath {
			t.Errorf("путь к конфигу в JSON = %q", decoded.ConfigPath)
		}
	})

	t.Run("неизвестный формат", func(t *testing.T) {
		var buffer bytes.Buffer
		err := report.Write(&buffer, "yaml")
		if err == nil {
			t.Fatal("ожидалась ошибка неизвестного формата")
		}
		if !strings.Contains(err.Error(), FormatTable) || !strings.Contains(err.Error(), FormatJSON) {
			t.Errorf("ошибка не перечисляет форматы: %v", err)
		}
		if buffer.Len() != 0 {
			t.Errorf("при ошибке формата что-то напечатано: %q", buffer.String())
		}
	})
}

// TestValidateFormat проверяет проверку формата до выполнения команд.
func TestValidateFormat(t *testing.T) {
	for _, format := range []string{FormatTable, FormatJSON} {
		if err := ValidateFormat(format); err != nil {
			t.Errorf("ValidateFormat(%q): %v", format, err)
		}
	}
	for _, format := range []string{"", "yaml", "TABLE"} {
		if err := ValidateFormat(format); err == nil {
			t.Errorf("ValidateFormat(%q) вернула nil", format)
		}
	}
}

// TestNewestArchive проверяет поиск самого свежего архива: каталог бэкапов
// содержит подкаталоги по базам, а файлы метаданных — не архивы.
func TestNewestArchive(t *testing.T) {
	dir := "/var/backups/sqlbrc"
	fsFake := newFakeFS()
	fsFake.addDir(dir, 0o750)
	fsFake.addDir(filepath.Join(dir, "fse-1"), 0o750)
	fsFake.addDir(filepath.Join(dir, "fse-2"), 0o750)
	fsFake.addFile(filepath.Join(dir, "README.txt"), testNow)
	fsFake.addFile(filepath.Join(dir, "fse-1", "20260919T020000Z.dump"), testNow.Add(-48*time.Hour))
	fsFake.addFile(filepath.Join(dir, "fse-2", "20260921T020000Z.dump"), testNow.Add(-2*time.Hour))
	fsFake.addFile(filepath.Join(dir, "fse-2", "20260921T020000Z.dump.json"), testNow.Add(-2*time.Hour))

	archive, found := newestArchive(fsFake, dir)
	if !found {
		t.Fatal("самый свежий архив не найден")
	}
	if want := filepath.Join(dir, "fse-2", "20260921T020000Z.dump"); archive != want {
		t.Errorf("архив = %q, ожидался %q", archive, want)
	}

	when, found := newestArchiveTime(fsFake, dir, "fse-1")
	if !found || !when.Equal(testNow.Add(-48*time.Hour)) {
		t.Errorf("время бэкапа = %s (найден: %v)", when, found)
	}

	empty := newFakeFS()
	empty.addDir(dir, 0o750)
	if _, found := newestArchive(empty, dir); found {
		t.Error("в пустом каталоге архив найден")
	}
	if _, found := newestArchiveTime(empty, dir, "нет-такой-базы"); found {
		t.Error("для отсутствующей базы бэкап найден")
	}
}

// TestHumanAge проверяет форматирование возраста бэкапа в сообщениях проверок.
func TestHumanAge(t *testing.T) {
	tests := []struct {
		age  time.Duration
		want string
	}{
		{age: 5 * time.Minute, want: "5 мин назад"},
		{age: 3 * time.Hour, want: "3 ч назад"},
		{age: 50 * time.Hour, want: "2 дн назад"},
	}

	for _, test := range tests {
		if got := humanAge(test.age); got != test.want {
			t.Errorf("humanAge(%s) = %q, ожидалось %q", test.age, got, test.want)
		}
	}
}

// TestMissingLocales проверяет список отсутствующих локалей: дубликаты не
// повторяются, а C и POSIX считаются всегда доступными.
func TestMissingLocales(t *testing.T) {
	databases := []pg.Database{
		{Name: "a", Collate: "en_US.UTF-8", Ctype: "en_US.UTF-8"},
		{Name: "b", Collate: "en_US.UTF-8", Ctype: "C"},
		{Name: "c", Collate: "ru_RU.UTF-8", Ctype: "POSIX"},
		{Name: "d", Collate: "", Ctype: ""},
	}
	missing, checked := missingLocales(databases, map[string]bool{"ru_RU.UTF-8": true})
	if checked != 2 {
		t.Errorf("проверено локалей %d, ожидалось 2", checked)
	}
	if len(missing) != 1 || missing[0] != "en_US.UTF-8" {
		t.Errorf("отсутствующие локали = %v, ожидалось [en_US.UTF-8]", missing)
	}
}

// TestWithDefaults проверяет подстановку зависимостей и значений по умолчанию:
// вызывающий код может не задавать время и пути к юниту systemd.
func TestWithDefaults(t *testing.T) {
	opts := withDefaults(Options{Cfg: testConfig(), PG: newFakePG(), FS: newFakeFS(), Runner: newFakeRunner()})
	if opts.Now == nil {
		t.Error("текущее время не подставлено")
	}
	if len(opts.UnitPaths) != len(DefaultUnitPaths) {
		t.Errorf("пути к юниту systemd = %v, ожидались %v", opts.UnitPaths, DefaultUnitPaths)
	}

	// Заданные вызывающим кодом значения не перезаписываются.
	custom := []string{"/etc/systemd/system/sqlbrc.service"}
	now := func() time.Time { return testNow }
	explicit := withDefaults(Options{
		Now: now, UnitPaths: custom, FS: newFakeFS(), Runner: newFakeRunner(),
	})
	if explicit.Now == nil || len(explicit.UnitPaths) != len(custom) {
		t.Errorf("заданные значения перезаписаны: %+v", explicit)
	}
}
