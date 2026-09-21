package doctor

import (
	"os"
	"testing"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// TestCheckPasswordFile проверяет проверку файла пароля режима tcp: утилиты
// читают его при каждом запуске, поэтому и отсутствие файла, и права шире 0600 —
// ошибки (пароль роли не должен быть доступен посторонним).
func TestCheckPasswordFile(t *testing.T) {
	const path = "/etc/sqlbrc/pgpass"
	cases := []struct {
		name string
		mode string
		path string
		// files — файлы в подменённой файловой системе: путь и права.
		files map[string]os.FileMode
		// want — ожидаемый уровень проверки; noCheck означает, что проверка не добавляется.
		noCheck bool
		want    Level
	}{
		{name: "режим sudo: проверки нет", mode: pg.ModeSudo, path: path, noCheck: true},
		{name: "режим tcp: путь не задан", mode: pg.ModeTCP, want: LevelError},
		{name: "режим tcp: файла нет", mode: pg.ModeTCP, path: path, files: map[string]os.FileMode{}, want: LevelError},
		{name: "режим tcp: права 0644", mode: pg.ModeTCP, path: path, files: map[string]os.FileMode{path: 0o644}, want: LevelError},
		{name: "режим tcp: права 0600", mode: pg.ModeTCP, path: path, files: map[string]os.FileMode{path: 0o600}, want: LevelOK},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fsys := newFakeFS()
			for name, mode := range testCase.files {
				fsys.files[name] = fakeFile{name: name, mode: mode}
			}
			opts := Options{Cfg: config.Default(), FS: fsys}
			opts.Cfg.Postgres.Mode = testCase.mode
			opts.Cfg.Postgres.PasswordFile = testCase.path

			report := Report{}
			checkPasswordFile(&report, opts)
			if testCase.noCheck {
				if len(report.Checks) != 0 {
					t.Fatalf("проверка добавлена без режима tcp: %+v", report.Checks)
				}
				return
			}
			check := checkByName(t, report, "pgpass")
			if check.Level != testCase.want {
				t.Fatalf("уровень %s, ожидался %s (%s)", check.Level, testCase.want, check.Message)
			}
		})
	}
}
