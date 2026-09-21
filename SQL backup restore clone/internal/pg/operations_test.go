package pg

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestClientDump проверяет снятие дампа: архив идёт потоком в переданный writer,
// а argv содержит формат, сжатие и имя базы, но не путь к файлу.
func TestClientDump(t *testing.T) {
	runner := newFakeRunner()
	runner.responses["pg_dump"] = "PGDMP-архив"
	client := newTestClient(runner)

	var out bytes.Buffer
	err := client.Dump(context.Background(), DumpOptions{
		Database:    "fse-1234",
		Compression: DefaultCompression,
	}, &out)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if out.String() != "PGDMP-архив" {
		t.Errorf("в writer попал %q", out.String())
	}
	call := runner.lastCall()
	for _, want := range []string{"pg_dump", "--format custom", "--compress 6", "--dbname fse-1234"} {
		if !strings.Contains(call, want) {
			t.Errorf("в вызове нет %q: %s", want, call)
		}
	}
	if strings.Contains(call, "--file") {
		t.Errorf("pg_dump не должен писать файл сам, путь задаёт сервис: %s", call)
	}
}

// TestClientDumpAllowsUncompressed проверяет, что нулевой уровень сжатия не
// подменяется значением по умолчанию: для маленьких стендов дамп без сжатия
// восстанавливается заметно быстрее.
func TestClientDumpAllowsUncompressed(t *testing.T) {
	runner := newFakeRunner()
	client := newTestClient(runner)
	var out bytes.Buffer
	if err := client.Dump(context.Background(), DumpOptions{Database: "fse-1234"}, &out); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if call := runner.lastCall(); !strings.Contains(call, "--compress 0") {
		t.Errorf("нулевой уровень сжатия потерян: %s", call)
	}
}

// TestClientDumpRejectsForeignDatabase проверяет, что дамп чужой базы не
// запускается: имя приходит из CLI и проверяется по шаблону обслуживаемых баз.
func TestClientDumpRejectsForeignDatabase(t *testing.T) {
	runner := newFakeRunner()
	client := newTestClient(runner)
	var out bytes.Buffer
	for _, database := range []string{"prod", "postgres", "../etc", ""} {
		err := client.Dump(context.Background(), DumpOptions{Database: database}, &out)
		if err == nil {
			t.Errorf("дамп базы %q не отвергнут", database)
		}
	}
	if len(runner.calls) != 0 {
		t.Errorf("pg_dump был запущен: %v", runner.calls)
	}
}

// TestClientRestoreFromStdin проверяет восстановление: архив читается из stdin
// (файл не копируется), а имя базы, очистка объектов и остановка на ошибке
// передаются pg_restore.
func TestClientRestoreFromStdin(t *testing.T) {
	runner := newFakeRunner()
	client := newTestClient(runner)
	archive := bytes.NewBufferString("PGDMP-архив")

	err := client.Restore(context.Background(), RestoreOptions{
		Database:    "fse-1234",
		Jobs:        1,
		Clean:       true,
		IfExists:    true,
		ExitOnError: true,
	}, archive)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	call := runner.lastCall()
	for _, want := range []string{"pg_restore", "--clean", "--if-exists", "--exit-on-error", "--dbname fse-1234"} {
		if !strings.Contains(call, want) {
			t.Errorf("в вызове нет %q: %s", want, call)
		}
	}
	if strings.Contains(call, "--jobs") {
		t.Errorf("при одном процессе --jobs не нужен: %s", call)
	}
	if archive.Len() != 0 {
		t.Errorf("архив не прочитан полностью: осталось %d байт", archive.Len())
	}
}

// TestClientRestoreParallelUsesArchivePath проверяет параллельное восстановление:
// pg_restore не читает архив из stdin при --jobs, поэтому путь передаётся
// позиционным аргументом.
func TestClientRestoreParallelUsesArchivePath(t *testing.T) {
	runner := newFakeRunner()
	client := newTestClient(runner)
	archive := "/var/backups/sqlbrc/fse-1234/20260921T020000Z.dump"

	err := client.Restore(context.Background(), RestoreOptions{
		Database: "fse-1234",
		Archive:  archive,
		Jobs:     4,
		Clean:    true,
		IfExists: true,
	}, nil)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	call := runner.lastCall()
	for _, want := range []string{"--jobs 4", "--dbname fse-1234", archive} {
		if !strings.Contains(call, want) {
			t.Errorf("в вызове нет %q: %s", want, call)
		}
	}
}

// TestClientRestoreParallelRequiresArchive проверяет отказ: без пути к архиву
// параллельное восстановление невозможно, и это должно выясняться до запуска
// pg_restore, а не по его ошибке.
func TestClientRestoreParallelRequiresArchive(t *testing.T) {
	runner := newFakeRunner()
	client := newTestClient(runner)
	err := client.Restore(context.Background(), RestoreOptions{
		Database: "fse-1234",
		Jobs:     4,
	}, bytes.NewBufferString("PGDMP-архив"))
	if err == nil {
		t.Fatal("восстановление без пути к архиву не отвергнуто")
	}
	if !strings.Contains(err.Error(), "путь к архиву") {
		t.Errorf("сообщение об ошибке %q", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("pg_restore был запущен: %v", runner.calls)
	}
}

// TestClientArchiveSummary проверяет сводку по архиву: она попадает в метаданные
// бэкапа, поэтому её можно получить и без доступа к серверу.
func TestClientArchiveSummary(t *testing.T) {
	runner := newFakeRunner()
	runner.responses["pg_restore"] = "; Archive created at 2026-09-21 02:00:00 UTC\n" +
		"218; 1259 16409 TABLE public alembic_version\n" +
		"219; 1259 16410 TABLE public users\n" +
		"220; 1259 16411 INDEX public users_pkey\n"
	client := newTestClient(runner)

	summary, err := client.ArchiveSummary(context.Background(), "/var/backups/sqlbrc/fse-1234/20260921T020000Z.dump")
	if err != nil {
		t.Fatalf("ArchiveSummary: %v", err)
	}
	if summary.Entries != 3 {
		t.Errorf("объектов в архиве %d, ожидалось 3", summary.Entries)
	}
	want := []string{"INDEX 1", "TABLE 2"}
	if len(summary.ByType) != len(want) {
		t.Fatalf("сводка по типам %v, ожидалось %v", summary.ByType, want)
	}
	for index, line := range want {
		if summary.ByType[index] != line {
			t.Errorf("сводка по типам %v, ожидалось %v", summary.ByType, want)
		}
	}
	if call := runner.lastCall(); !strings.Contains(call, "--list") {
		t.Errorf("оглавление читается без --list: %s", call)
	}
}

// TestClientPasswordEnv проверяет передачу пароля: он попадает в окружение
// процессом PGPASSFILE, поэтому в argv его нет и в списке процессов он не виден.
func TestClientPasswordEnv(t *testing.T) {
	if env := passwordEnv(""); env != nil {
		t.Errorf("без файла пароля окружение задано: %v", env)
	}
	env := passwordEnv("/etc/sqlbrc/pgpass")
	if len(env) != 1 || env[0] != "PGPASSFILE=/etc/sqlbrc/pgpass" {
		t.Errorf("окружение пароля %v, ожидался путь к файлу", env)
	}
}

// TestClientTCPModeKeepsPasswordOutOfArgv проверяет режим tcp: подключение
// задаётся аргументами, а пароль остаётся в файле — в командной строке его нет.
func TestClientTCPModeKeepsPasswordOutOfArgv(t *testing.T) {
	runner := newFakeRunner()
	client := NewClient(Config{
		Mode:         ModeTCP,
		BinDir:       "/usr/bin",
		Host:         "127.0.0.1",
		Port:         "5432",
		Role:         "fse_admin",
		PasswordFile: "/etc/sqlbrc/pgpass",
	}, runner, nil)

	var out bytes.Buffer
	if err := client.Dump(context.Background(), DumpOptions{Database: "fse-1234"}, &out); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	call := runner.lastCall()
	for _, want := range []string{"--host 127.0.0.1", "--port 5432", "--username fse_admin"} {
		if !strings.Contains(call, want) {
			t.Errorf("в вызове нет %q: %s", want, call)
		}
	}
	if strings.Contains(call, "pgpass") {
		t.Errorf("путь к файлу пароля попал в командную строку: %s", call)
	}
}

// TestClientDatabaseOperations проверяет чтение сведений о базе: наличие,
// параметры, размер, число подключений и их завершение — всё через служебные
// запросы psql.
func TestClientDatabaseOperations(t *testing.T) {
	runner := newFakeRunner()
	client := newTestClient(runner)
	ctx := context.Background()

	runner.pushQueue("psql", "1\n", "")
	exists, err := client.DatabaseExists(ctx, "fse-1234", "")
	if err != nil {
		t.Fatalf("DatabaseExists: %v", err)
	}
	if !exists {
		t.Error("существующая база не найдена")
	}
	exists, err = client.DatabaseExists(ctx, "fssd-7", "")
	if err != nil {
		t.Fatalf("DatabaseExists: %v", err)
	}
	if exists {
		t.Error("отсутствующая база найдена")
	}

	runner.pushQueue("psql", strings.Join([]string{"UTF8", "ru_RU.UTF-8", "ru_RU.UTF-8", "postgres"}, FieldSeparator)+"\n")
	locale, err := client.DatabaseLocale(ctx, "fse-1234", "")
	if err != nil {
		t.Fatalf("DatabaseLocale: %v", err)
	}
	if locale.Encoding != "UTF8" || locale.Collate != "ru_RU.UTF-8" || locale.Owner != "postgres" {
		t.Errorf("параметры базы разобраны неверно: %+v", locale)
	}

	runner.pushQueue("psql", "1048576\n")
	size, err := client.DatabaseSize(ctx, "fse-1234", "")
	if err != nil {
		t.Fatalf("DatabaseSize: %v", err)
	}
	if size != 1048576 {
		t.Errorf("размер базы %d, ожидался 1048576", size)
	}

	runner.pushQueue("psql", "2\n", "2\n")
	sessions, err := client.Sessions(ctx, "fse-1234", "")
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if sessions != 2 {
		t.Errorf("подключений %d, ожидалось 2", sessions)
	}
	killed, err := client.TerminateBackends(ctx, "fse-1234", "")
	if err != nil {
		t.Fatalf("TerminateBackends: %v", err)
	}
	if killed != 2 {
		t.Errorf("завершено подключений %d, ожидалось 2", killed)
	}
	if call := runner.lastCall(); !strings.Contains(call, "pg_terminate_backend") {
		t.Errorf("подключения завершаются не тем запросом: %s", call)
	}
	if _, err := client.DatabaseExists(ctx, "prod", ""); err == nil {
		t.Error("проверка чужой базы не отвергнута")
	}
}

// TestClientDatabaseMutations проверяет создание, смену владельца и удаление базы:
// запрос собирает сервис, а чужая база отвергается до его выполнения.
func TestClientDatabaseMutations(t *testing.T) {
	runner := newFakeRunner()
	client := newTestClient(runner)
	ctx := context.Background()

	if err := client.CreateDatabase(ctx, CreateDatabaseOptions{Database: "prod"}); err == nil {
		t.Error("создание чужой базы не отвергнуто")
	}
	if len(runner.calls) != 0 {
		t.Errorf("запрос выполнен несмотря на отказ: %v", runner.calls)
	}
	if err := client.CreateDatabase(ctx, CreateDatabaseOptions{
		Database: "fssd-7", Owner: "postgres", Template: "template0",
	}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	if call := runner.lastCall(); !strings.Contains(call, `CREATE DATABASE "fssd-7"`) {
		t.Errorf("вызов создания базы: %s", call)
	}
	if err := client.AlterDatabaseOwner(ctx, "fssd-7", "postgres", ""); err != nil {
		t.Fatalf("AlterDatabaseOwner: %v", err)
	}
	if err := client.DropDatabase(ctx, "fssd-7", ""); err != nil {
		t.Fatalf("DropDatabase: %v", err)
	}
	if call := runner.lastCall(); !strings.Contains(call, `DROP DATABASE IF EXISTS "fssd-7"`) {
		t.Errorf("вызов удаления базы: %s", call)
	}
	runner.failures["psql"] = "permission denied"
	err := client.DropDatabase(ctx, "fssd-7", "")
	if err == nil || !strings.Contains(err.Error(), "не удалось удалить базу") {
		t.Errorf("ошибка удаления базы: %v", err)
	}
}

// TestClientRequireOwnedDatabase проверяет защиту от восстановления в чужую базу:
// отсутствие базы и чужой владелец — разные отказы, и оба останавливают операцию.
func TestClientRequireOwnedDatabase(t *testing.T) {
	runner := newFakeRunner()
	client := newTestClient(runner)
	ctx := context.Background()

	runner.pushQueue("psql", "")
	err := client.RequireOwnedDatabase(ctx, "fssd-7", "postgres", "")
	if !errors.Is(err, ErrDatabaseMissing) {
		t.Errorf("отсутствующая база дала ошибку %v, ожидалась ErrDatabaseMissing", err)
	}

	runner.pushQueue("psql",
		"1\n",
		strings.Join([]string{"UTF8", "ru_RU.UTF-8", "ru_RU.UTF-8", "other_role"}, FieldSeparator)+"\n")
	err = client.RequireOwnedDatabase(ctx, "fse-1234", "postgres", "")
	if err == nil || !strings.Contains(err.Error(), "принадлежит роли other_role") {
		t.Errorf("база с чужим владельцем прошла проверку: %v", err)
	}

	runner.pushQueue("psql", "1\n")
	if err := client.RequireOwnedDatabase(ctx, "fse-1234", "", ""); err != nil {
		t.Errorf("без роли-владельца достаточно проверки существования: %v", err)
	}
}

// TestClientWaitForReady проверяет ожидание сервера: при готовом сервере вызов
// завершается сразу, при молчащем — по пределу времени, с причиной отказа.
func TestClientWaitForReady(t *testing.T) {
	runner := newFakeRunner()
	client := newTestClient(runner)
	if err := client.WaitForReady(context.Background(), ReadyOptions{
		Timeout:  time.Second,
		Interval: time.Millisecond,
	}); err != nil {
		t.Fatalf("WaitForReady: %v", err)
	}

	runner.failures["pg_isready"] = "no response"
	err := client.WaitForReady(context.Background(), ReadyOptions{
		Timeout:  30 * time.Millisecond,
		Interval: time.Millisecond,
	})
	if err == nil {
		t.Fatal("ожидание молчащего сервера завершилось успешно")
	}
	if !strings.Contains(err.Error(), "не ответил за") || !strings.Contains(err.Error(), "no response") {
		t.Errorf("сообщение об ошибке %q: ожидалась причина отказа", err)
	}
	if len(runner.calls) < 2 {
		t.Errorf("готовность проверена %d раз: ожидались повторные попытки", len(runner.calls))
	}
}
