package pg

import (
	"strings"
	"testing"
)

// TestPrefixValidate проверяет, что неполный или неизвестный способ запуска
// отвергается: сервис не должен запускаться с пустым путём к sudo или каталогом утилит.
func TestPrefixValidate(t *testing.T) {
	tests := []struct {
		name    string
		prefix  Prefix
		wantErr bool
	}{
		{name: "sudo по умолчанию", prefix: Prefix{Mode: ModeSudo, SudoPath: DefaultSudoPath, SudoUser: DefaultSudoUser, BinDir: DefaultBinDir}},
		{name: "tcp", prefix: Prefix{Mode: ModeTCP, BinDir: DefaultBinDir}},
		{name: "неизвестный режим", prefix: Prefix{Mode: "local", BinDir: DefaultBinDir}, wantErr: true},
		{name: "пустой режим", prefix: Prefix{BinDir: DefaultBinDir}, wantErr: true},
		{name: "без каталога утилит", prefix: Prefix{Mode: ModeSudo, SudoPath: DefaultSudoPath, SudoUser: DefaultSudoUser}, wantErr: true},
		{name: "без sudo", prefix: Prefix{Mode: ModeSudo, SudoUser: DefaultSudoUser, BinDir: DefaultBinDir}, wantErr: true},
		{name: "без пользователя", prefix: Prefix{Mode: ModeSudo, SudoPath: DefaultSudoPath, BinDir: DefaultBinDir}, wantErr: true},
		{name: "некорректный пользователь", prefix: Prefix{Mode: ModeSudo, SudoPath: DefaultSudoPath, SudoUser: "post gres", BinDir: DefaultBinDir}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.prefix.Validate()
			if test.wantErr {
				if err == nil {
					t.Fatalf("Prefix.Validate() вернула nil, ожидалась ошибка")
				}
				return
			}
			if err != nil {
				t.Fatalf("Prefix.Validate(): неожиданная ошибка: %v", err)
			}
		})
	}
}

// TestPrefixBinary проверяет путь к утилите и запрет произвольных бинарей:
// имя утилиты приходит из кода, но список всё равно фиксирован.
func TestPrefixBinary(t *testing.T) {
	prefix := Prefix{Mode: ModeSudo, SudoPath: DefaultSudoPath, SudoUser: DefaultSudoUser, BinDir: DefaultBinDir}
	for _, tool := range Tools {
		path, err := prefix.Binary(tool)
		if err != nil {
			t.Fatalf("Binary(%q): неожиданная ошибка: %v", tool, err)
		}
		if want := "/usr/bin/" + tool; path != want {
			t.Errorf("Binary(%q) = %q, ожидалось %q", tool, path, want)
		}
	}
	for _, tool := range []string{"sh", "bash", "kill", ""} {
		if _, err := prefix.Binary(tool); err == nil {
			t.Errorf("Binary(%q) вернула путь: произвольные утилиты запускать нельзя", tool)
		}
	}
}

func TestPrefixWrap(t *testing.T) {
	sudo := Prefix{Mode: ModeSudo, SudoPath: DefaultSudoPath, SudoUser: DefaultSudoUser, BinDir: DefaultBinDir}
	process, args := sudo.Wrap("/usr/bin/pg_dump")
	if process != DefaultSudoPath {
		t.Errorf("процесс = %q, ожидался %q", process, DefaultSudoPath)
	}
	// -n обязателен: без него sudo ждёт пароль и задача зависает.
	if got := strings.Join(args, " "); got != "-n -u postgres /usr/bin/pg_dump" {
		t.Errorf("аргументы sudo = %q", got)
	}

	tcp := Prefix{Mode: ModeTCP, BinDir: DefaultBinDir}
	process, args = tcp.Wrap("/usr/bin/pg_dump")
	if process != "/usr/bin/pg_dump" || len(args) != 0 {
		t.Errorf("в режиме tcp процесс = %q, аргументы = %v", process, args)
	}
}

func TestPrefixConnectionArgs(t *testing.T) {
	conn := ConnConfig{Host: "127.0.0.1", Port: "5432", Role: "app_owner"}

	sudo := Prefix{Mode: ModeSudo, SudoPath: DefaultSudoPath, SudoUser: DefaultSudoUser, BinDir: DefaultBinDir}
	if args := sudo.ConnectionArgs(conn); len(args) != 0 {
		t.Errorf("в режиме sudo аргументы подключения должны быть пустыми, получено %v", args)
	}

	tcp := Prefix{Mode: ModeTCP, BinDir: DefaultBinDir}
	want := "--host 127.0.0.1 --port 5432 --username app_owner"
	if got := strings.Join(tcp.ConnectionArgs(conn), " "); got != want {
		t.Errorf("аргументы подключения = %q, ожидалось %q", got, want)
	}
	if got := tcp.ConnectionArgs(ConnConfig{}); len(got) != 0 {
		t.Errorf("пустые параметры подключения дали аргументы %v", got)
	}
}

func TestDumpArgv(t *testing.T) {
	tests := []struct {
		name    string
		args    DumpArgs
		want    string
		wantErr bool
	}{
		{
			name: "значения по умолчанию",
			args: DumpArgs{DB: "fse-1234", Compression: DefaultCompression},
			want: "--format custom --compress 6 --dbname fse-1234",
		},
		{
			name: "полный набор",
			args: DumpArgs{
				DB:          "fse-1234",
				Format:      FormatCustom,
				Compression: 6,
				NoOwner:     true,
				Verbose:     true,
				Extra:       []string{"--no-comments"},
			},
			want: "--format custom --compress 6 --verbose --no-owner --no-comments --dbname fse-1234",
		},
		{
			name: "без владельцев и с ACL",
			args: DumpArgs{DB: "fse-1", Compression: 6, NoPrivileges: true, NoACL: true},
			want: "--format custom --compress 6 --no-privileges --no-acl --dbname fse-1",
		},
		{name: "имя базы вне шаблона", args: DumpArgs{DB: "postgres"}, wantErr: true},
		{
			name: "шаблон из конфига",
			args: DumpArgs{
				DB: "stand-team-7", Pattern: `^stand-team-[0-9]+$`, Compression: DefaultCompression,
			},
			want: "--format custom --compress 6 --dbname stand-team-7",
		},
		// Шаблон из конфига ужесточает проверку: база, которую взял бы шаблон
		// по умолчанию, при суженном шаблоне командой не станет.
		{name: "имя вне шаблона конфига", args: DumpArgs{DB: "prod-1", Pattern: `^stand-[0-9]+$`}, wantErr: true},
		{name: "незаякоренный шаблон конфига", args: DumpArgs{DB: "fse-1", Pattern: `[0-9]+$`}, wantErr: true},
		{name: "пустое имя базы", args: DumpArgs{}, wantErr: true},
		{name: "формат не поддерживается", args: DumpArgs{DB: "fse-1", Format: "tar"}, wantErr: true},
		{name: "сжатие вне диапазона", args: DumpArgs{DB: "fse-1", Compression: 12}, wantErr: true},
		{name: "отрицательное сжатие", args: DumpArgs{DB: "fse-1", Compression: -1}, wantErr: true},
		// Аргументы, которые формирует сервис, передавать нельзя: файл и подключение
		// должны оставаться под его контролем.
		{name: "свой файл", args: DumpArgs{DB: "fse-1", Extra: []string{"--file=/tmp/other.dump"}}, wantErr: true},
		{name: "своя база", args: DumpArgs{DB: "fse-1", Extra: []string{"-d", "postgres"}}, wantErr: true},
		{name: "свой хост", args: DumpArgs{DB: "fse-1", Extra: []string{"--host=evil"}}, wantErr: true},
		{name: "пустой дополнительный аргумент", args: DumpArgs{DB: "fse-1", Extra: []string{"  "}}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := DumpArgv(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatalf("DumpArgv вернула %v, ожидалась ошибка", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DumpArgv: неожиданная ошибка: %v", err)
			}
			if joined := strings.Join(got, " "); joined != test.want {
				t.Errorf("аргументы = %q, ожидалось %q", joined, test.want)
			}
		})
	}
}

func TestValidateFormatAndCompression(t *testing.T) {
	for _, format := range []string{FormatCustom, FormatPlain} {
		if err := ValidateFormat(format); err != nil {
			t.Errorf("ValidateFormat(%q): %v", format, err)
		}
	}
	for _, format := range []string{"", "tar", "directory", "CUSTOM"} {
		if err := ValidateFormat(format); err == nil {
			t.Errorf("ValidateFormat(%q) вернула nil", format)
		}
	}
	for _, level := range []int{0, 1, 6, 9} {
		if err := ValidateCompression(level); err != nil {
			t.Errorf("ValidateCompression(%d): %v", level, err)
		}
	}
	for _, level := range []int{-1, 10, 100} {
		if err := ValidateCompression(level); err == nil {
			t.Errorf("ValidateCompression(%d) вернула nil", level)
		}
	}
}

func TestRestoreArgv(t *testing.T) {
	tests := []struct {
		name    string
		args    RestoreArgs
		want    string
		wantErr bool
	}{
		{
			name: "восстановление по умолчанию",
			args: RestoreArgs{DB: "fse-1234"},
			want: "--dbname fse-1234",
		},
		{
			name: "очистка и параллелизм",
			args: RestoreArgs{
				DB: "fse-1234", Jobs: 4, Clean: true, IfExists: true, ExitOnError: true, Verbose: true,
			},
			want: "--jobs 4 --clean --if-exists --exit-on-error --verbose --dbname fse-1234",
		},
		{
			name: "чтение оглавления архива",
			args: RestoreArgs{ListOnly: true, Archive: "/var/backups/sqlbrc/fse-1/2026.dump"},
			want: "--list /var/backups/sqlbrc/fse-1/2026.dump",
		},
		{name: "оглавление без файла", args: RestoreArgs{ListOnly: true}, wantErr: true},
		{name: "имя базы вне шаблона", args: RestoreArgs{DB: "postgres"}, wantErr: true},
		{
			name: "шаблон из конфига",
			args: RestoreArgs{DB: "stand-team-7", Pattern: `^stand-team-[0-9]+$`},
			want: "--dbname stand-team-7",
		},
		// Суженный шаблон защищает от восстановления не на ту базу: команда
		// завершается ошибкой до запуска pg_restore.
		{name: "имя вне шаблона конфига", args: RestoreArgs{DB: "prod-1", Pattern: `^stand-[0-9]+$`}, wantErr: true},
		{name: "слишком много процессов", args: RestoreArgs{DB: "fse-1", Jobs: 64}, wantErr: true},
		{name: "отрицательные процессы", args: RestoreArgs{DB: "fse-1", Jobs: -2}, wantErr: true},
		{name: "своя база", args: RestoreArgs{DB: "fse-1", Extra: []string{"--dbname", "postgres"}}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := RestoreArgv(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatalf("RestoreArgv вернула %v, ожидалась ошибка", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("RestoreArgv: неожиданная ошибка: %v", err)
			}
			if joined := strings.Join(got, " "); joined != test.want {
				t.Errorf("аргументы = %q, ожидалось %q", joined, test.want)
			}
		})
	}
}

func TestPSQLArgv(t *testing.T) {
	tests := []struct {
		name    string
		args    PSQLArgs
		want    string
		wantErr bool
	}{
		{
			name: "запрос к каталогу",
			args: PSQLArgs{Database: "postgres", SQL: "SHOW server_version", FieldSeparator: FieldSeparator},
			want: "--no-psqlrc --quiet --no-align --tuples-only --field-separator \x1f --set ON_ERROR_STOP=1" +
				" --dbname postgres --command SHOW server_version",
		},
		{name: "пустая база", args: PSQLArgs{SQL: "SELECT 1", FieldSeparator: FieldSeparator}, wantErr: true},
		{name: "некорректная база", args: PSQLArgs{Database: "post gres", SQL: "SELECT 1", FieldSeparator: FieldSeparator}, wantErr: true},
		{name: "пустой запрос", args: PSQLArgs{Database: "postgres", FieldSeparator: FieldSeparator}, wantErr: true},
		{name: "пробельный запрос", args: PSQLArgs{Database: "postgres", SQL: "   ", FieldSeparator: FieldSeparator}, wantErr: true},
		{name: "разделитель из двух символов", args: PSQLArgs{Database: "postgres", SQL: "SELECT 1", FieldSeparator: "||"}, wantErr: true},
		{name: "пустой разделитель", args: PSQLArgs{Database: "postgres", SQL: "SELECT 1"}, wantErr: true},
		{
			name:    "своя база",
			args:    PSQLArgs{Database: "postgres", SQL: "SELECT 1", FieldSeparator: FieldSeparator, Extra: []string{"-d", "other"}},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := PSQLArgv(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatalf("PSQLArgv вернула %v, ожидалась ошибка", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("PSQLArgv: неожиданная ошибка: %v", err)
			}
			if joined := strings.Join(got, " "); joined != test.want {
				t.Errorf("аргументы = %q, ожидалось %q", joined, test.want)
			}
		})
	}
}

// TestArgvHasNoShell проверяет главное правило запуска: команды собираются как
// argv-массив без промежуточного шелла, а имя процесса — только из фиксированного
// списка утилит PostgreSQL или sudo. Иначе имя базы из веб-формы стало бы командой.
func TestArgvHasNoShell(t *testing.T) {
	prefix := Prefix{Mode: ModeSudo, SudoPath: DefaultSudoPath, SudoUser: DefaultSudoUser, BinDir: DefaultBinDir}
	shells := map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "cmd": true, "powershell": true}

	dump, err := DumpArgv(DumpArgs{DB: "fse-1234", Compression: DefaultCompression})
	if err != nil {
		t.Fatalf("DumpArgv: %v", err)
	}
	restore, err := RestoreArgv(RestoreArgs{DB: "fse-1234", Jobs: 2})
	if err != nil {
		t.Fatalf("RestoreArgv: %v", err)
	}
	query, err := PSQLArgv(PSQLArgs{Database: "postgres", SQL: "SELECT 1", FieldSeparator: FieldSeparator})
	if err != nil {
		t.Fatalf("PSQLArgv: %v", err)
	}

	for _, command := range [][]string{dump, restore, query} {
		process, prefixArgs := prefix.Wrap("/usr/bin/pg_dump")
		if shells[toolNameOf(process)] {
			t.Errorf("процесс %q — шелл", process)
		}
		all := append(append([]string{process}, prefixArgs...), command...)
		for index, arg := range all {
			if index == 0 && shells[toolNameOf(arg)] {
				t.Errorf("нулевой аргумент %q — шелл", arg)
			}
			if arg == "-c" || arg == "-i" {
				t.Errorf("в argv попал ключ шелла %q: команды собираются без оболочки", arg)
			}
		}
	}
}
