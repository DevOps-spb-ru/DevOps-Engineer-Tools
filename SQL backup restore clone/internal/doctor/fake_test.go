package doctor

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// testBcrypt — хэш пароля для тестовых конфигураций: 60 символов с префиксом $2y$.
const testBcrypt = "$2y$10$abcdefghijklmnopqrstuu0123456789abcdefghijklmnopqrstu"

// testConfig возвращает конфигурацию, проходящую проверку: интерфейс слушает LAN
// из README (0.0.0.0:8088) с осознанным allow_insecure и одним пользователем.
func testConfig() config.Configuration {
	cfg := config.Default()
	cfg.Server.AllowInsecure = true
	cfg.Auth.Users = []config.User{{Login: "admin", PasswordBcrypt: testBcrypt}}
	return cfg
}

// testNow — момент времени, от которого считаются возрасты бэкапов в тестах.
var testNow = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

// fakeFile — минимальная реализация os.FileInfo для проверок каталогов.
type fakeFile struct {
	name  string
	mode  fs.FileMode
	mod   time.Time
	isDir bool
}

// Name возвращает имя файла.
func (f fakeFile) Name() string { return f.name }

// Size возвращает размер файла: проверкам он не нужен.
func (f fakeFile) Size() int64 { return 0 }

// Mode возвращает права файла.
func (f fakeFile) Mode() fs.FileMode { return f.mode }

// ModTime возвращает время изменения файла.
func (f fakeFile) ModTime() time.Time { return f.mod }

// IsDir отвечает, каталог ли это.
func (f fakeFile) IsDir() bool { return f.isDir }

// Sys возвращает системную информацию: проверки работают через FS.Owner.
func (f fakeFile) Sys() any { return nil }

// fakeDirEntry — минимальная реализация os.DirEntry.
type fakeDirEntry struct {
	info fakeFile
}

// Name возвращает имя файла.
func (e fakeDirEntry) Name() string { return e.info.name }

// IsDir отвечает, каталог ли это.
func (e fakeDirEntry) IsDir() bool { return e.info.isDir }

// Type возвращает тип файла.
func (e fakeDirEntry) Type() fs.FileMode { return e.info.mode.Type() }

// Info возвращает информацию о файле.
func (e fakeDirEntry) Info() (os.FileInfo, error) { return e.info, nil }

// fakeFS — подмена файловых операций: проверки прогоняются без реальных каталогов.
type fakeFS struct {
	files    map[string]fakeFile
	writeErr map[string]error
	free     int64
	total    int64
	freeErr  error
	owner    string
}

// newFakeFS создаёт пустую файловую систему с приличным запасом места.
func newFakeFS() *fakeFS {
	return &fakeFS{
		files:    make(map[string]fakeFile),
		writeErr: make(map[string]error),
		free:     100 * 1000 * 1000 * 1000,
		total:    500 * 1000 * 1000 * 1000,
		owner:    "sqlbrc",
	}
}

// addDir добавляет каталог с указанными правами.
func (f *fakeFS) addDir(path string, mode fs.FileMode) *fakeFS {
	f.files[path] = fakeFile{name: filepath.Base(path), mode: mode | fs.ModeDir, isDir: true}
	return f
}

// addFile добавляет файл в каталоге.
func (f *fakeFS) addFile(path string, mod time.Time) *fakeFS {
	f.files[path] = fakeFile{name: filepath.Base(path), mode: 0o640, mod: mod}
	return f
}

// Stat возвращает информацию о файле.
func (f *fakeFS) Stat(path string) (os.FileInfo, error) {
	file, ok := f.files[path]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
	}
	return file, nil
}

// ReadDir возвращает содержимое каталога.
func (f *fakeFS) ReadDir(path string) ([]os.DirEntry, error) {
	if _, ok := f.files[path]; !ok {
		return nil, &fs.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	entries := make([]os.DirEntry, 0, 4)
	for candidate, file := range f.files {
		if !samePath(parentDir(candidate), path) {
			continue
		}
		entries = append(entries, fakeDirEntry{info: file})
	}
	return entries, nil
}

// parentDir возвращает родительский каталог пути, не завися от вида разделителя:
// тесты идут и на Windows, где filepath.Clean меняет «/» на «\» и ломает сравнение.
func parentDir(path string) string {
	normalized := strings.ReplaceAll(path, "\\", "/")
	if index := strings.LastIndex(normalized, "/"); index >= 0 {
		return normalized[:index]
	}
	return ""
}

// samePath сравнивает пути с учётом обоих видов разделителей.
func samePath(left, right string) bool {
	return strings.ReplaceAll(left, "\\", "/") == strings.ReplaceAll(right, "\\", "/")
}

// WriteProbe отвечает, разрешена ли запись в каталог.
func (f *fakeFS) WriteProbe(dir string) error {
	if err, ok := f.writeErr[dir]; ok {
		return err
	}
	if _, ok := f.files[dir]; !ok {
		return &fs.PathError{Op: "create", Path: dir, Err: os.ErrNotExist}
	}
	return nil
}

// FreeSpace возвращает подготовленное свободное и общее место.
func (f *fakeFS) FreeSpace(string) (int64, int64, error) {
	return f.free, f.total, f.freeErr
}

// Owner возвращает подготовленное имя владельца.
func (f *fakeFS) Owner(os.FileInfo) string { return f.owner }

// fakePG — подмена клиента PostgreSQL: проверки не подключаются к серверу.
type fakePG struct {
	pingErr      error
	server       pg.Version
	serverErr    error
	clients      map[string]pg.Version
	clientsErr   error
	role         pg.Role
	roleErr      error
	databases    []pg.Database
	databasesErr error
}

// newFakePG создаёт сервер, с которым все проверки проходят успешно.
func newFakePG() *fakePG {
	return &fakePG{
		server: pg.Version{Major: 15, Minor: 19},
		clients: map[string]pg.Version{
			"pg_dump":    {Major: 15, Minor: 19},
			"pg_restore": {Major: 15, Minor: 19},
			"psql":       {Major: 15, Minor: 19},
			"pg_isready": {Major: 15, Minor: 19},
		},
		role: pg.Role{Name: "postgres", Superuser: true, CreateDB: true},
		databases: []pg.Database{
			{Name: "fse-1234", Owner: "app_owner", Collate: "en_US.UTF-8", Ctype: "en_US.UTF-8", AllowConn: true},
			{Name: "postgres", Owner: "postgres", Collate: "C.UTF-8", Ctype: "C.UTF-8", AllowConn: true},
			{Name: "template0", Owner: "postgres", Collate: "C.UTF-8", Ctype: "C.UTF-8", IsTemplate: true},
		},
	}
}

// Ping проверяет доступность сервера.
func (f *fakePG) Ping(context.Context) error { return f.pingErr }

// ServerVersion возвращает версию сервера.
func (f *fakePG) ServerVersion(context.Context) (pg.Version, error) {
	return f.server, f.serverErr
}

// ClientVersions возвращает версии утилит PostgreSQL.
func (f *fakePG) ClientVersions(context.Context) (map[string]pg.Version, error) {
	return f.clients, f.clientsErr
}

// RoleInfo возвращает сведения о роли.
func (f *fakePG) RoleInfo(context.Context) (pg.Role, error) { return f.role, f.roleErr }

// ListDatabases возвращает список баз.
func (f *fakePG) ListDatabases(context.Context) ([]pg.Database, error) {
	return f.databases, f.databasesErr
}

// fakeRunner — подмена запуска внешних команд: sudo, locale и pg_restore
// в тестах проверок не запускаются.
type fakeRunner struct {
	calls      []string
	failures   map[string]bool
	stdout     map[string]string
	missingBin map[string]bool
}

// newFakeRunner создаёт подмену с успешными ответами по умолчанию.
func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		failures:   make(map[string]bool),
		stdout:     make(map[string]string),
		missingBin: make(map[string]bool),
	}
}

// LookPath отвечает, найдена ли утилита.
func (r *fakeRunner) LookPath(name string) (string, error) {
	if r.missingBin[name] {
		return "", fmt.Errorf("exec: %q: executable file not found in %%PATH%%", name)
	}
	return name, nil
}

// Run записывает вызов и отдаёт подготовленный stdout.
func (r *fakeRunner) Run(_ context.Context, name string, args []string, _ io.Reader, stdout, _ io.Writer) error {
	tool := r.record(name, args)
	if r.failures[tool] {
		return fmt.Errorf("exit status 1")
	}
	if stdout != nil {
		_, _ = io.WriteString(stdout, r.stdout[tool])
	}
	return nil
}

// Output записывает вызов и возвращает подготовленный stdout.
func (r *fakeRunner) Output(_ context.Context, name string, args []string) ([]byte, error) {
	tool := r.record(name, args)
	if r.failures[tool] {
		return nil, fmt.Errorf("exit status 1")
	}
	return []byte(r.stdout[tool]), nil
}

// record сохраняет вызов и возвращает имя утилиты.
func (r *fakeRunner) record(name string, args []string) string {
	r.calls = append(r.calls, pg.PrintableCommand(name, args))
	tool := toolName(name)
	if tool != "sudo" {
		return tool
	}
	// В режиме sudo утилита идёт аргументом после -n и -u <пользователь>.
	for _, arg := range args {
		if candidate := toolName(arg); pg.ValidateTool(candidate) == nil {
			return candidate
		}
	}
	return tool
}

// toolName возвращает последний элемент пути (с разделителями обоих видов).
func toolName(name string) string {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if index := strings.LastIndex(normalized, "/"); index >= 0 {
		return normalized[index+1:]
	}
	return normalized
}

// ran отвечает, вызывалась ли команда с такой подстрокой.
func (r *fakeRunner) ran(substring string) bool {
	for _, call := range r.calls {
		if strings.Contains(call, substring) {
			return true
		}
	}
	return false
}
