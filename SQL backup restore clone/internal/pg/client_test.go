package pg

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// newTestClient создаёт клиент с подменённым запуском команд: тесты проверяют
// argv и разбор вывода, не поднимая PostgreSQL. Значения по умолчанию
// (sudo, /usr/bin, база postgres) подставляет Config.WithDefaults.
func newTestClient(runner CommandRunner) *Client {
	return NewClient(Config{}, runner, nil)
}

// TestClientBinaryPath проверяет сообщение об отсутствующей утилите: без него
// причина «сервис не работает» превращается в поиск кода 127.
func TestClientBinaryPath(t *testing.T) {
	runner := newFakeRunner()
	client := newTestClient(runner)
	path, err := client.BinaryPath("pg_dump")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if path != "/usr/bin/pg_dump" {
		t.Errorf("путь = %q, ожидался /usr/bin/pg_dump", path)
	}
	if _, err := client.BinaryPath("sh"); err == nil {
		t.Error("BinaryPath(«sh») вернула путь: произвольные утилиты запускать нельзя")
	}

	runner.missing["pg_dump"] = true
	_, err = client.BinaryPath("pg_dump")
	if err == nil {
		t.Fatal("ожидалась ошибка об отсутствующей утилите")
	}
	if !strings.Contains(err.Error(), "postgresql-client-15") {
		t.Errorf("в ошибке нет подсказки про пакет: %v", err)
	}
}

// TestClientPing проверяет argv pg_isready и разбор отказа сервера.
func TestClientPing(t *testing.T) {
	runner := newFakeRunner()
	client := newTestClient(runner)
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	call := runner.lastCall()
	for _, want := range []string{DefaultSudoPath, "-n", "-u", "postgres", "/usr/bin/pg_isready", "--dbname", "postgres"} {
		if !strings.Contains(call, want) {
			t.Errorf("в вызове нет %q: %s", want, call)
		}
	}

	runner.failures["pg_isready"] = "no response"
	err := client.Ping(context.Background())
	if err == nil {
		t.Fatal("ожидалась ошибка недоступного сервера")
	}
	if !strings.Contains(err.Error(), "недоступен") {
		t.Errorf("ошибка не объясняет ситуацию: %v", err)
	}
}

// TestClientServerVersion проверяет разбор версии сервера из вывода psql.
func TestClientServerVersion(t *testing.T) {
	runner := newFakeRunner()
	runner.responses["psql"] = string(readTestdata(t, "server_version.txt"))
	client := newTestClient(runner)

	version, err := client.ServerVersion(context.Background())
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if version.Major != 15 || version.Minor != 19 {
		t.Errorf("версия = %s, ожидалось 15.19", version)
	}
	// Запрос идёт с разделителем полей и без psqlrc: иначе разбор зависит от ~/.psqlrc.
	call := runner.lastCall()
	for _, want := range []string{"--no-psqlrc", "--tuples-only", "--field-separator", "ON_ERROR_STOP=1", "SHOW server_version"} {
		if !strings.Contains(call, want) {
			t.Errorf("в вызове psql нет %q: %s", want, call)
		}
	}
}

// TestClientVersions проверяет сбор версий всех утилит и агрегацию ошибок.
func TestClientVersions(t *testing.T) {
	runner := newFakeRunner()
	for _, tool := range Tools {
		runner.responses[tool] = tool + " (PostgreSQL) 15.19 (Debian 15.19-0+deb12u1)\n"
	}
	client := newTestClient(runner)

	versions, err := client.ClientVersions(context.Background())
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(versions) != len(Tools) {
		t.Fatalf("получено версий %d, ожидалось %d", len(versions), len(Tools))
	}
	if version := versions["pg_dump"]; version.Major != 15 {
		t.Errorf("версия pg_dump = %s", version)
	}

	runner.missing["pg_restore"] = true
	versions, err = client.ClientVersions(context.Background())
	if err == nil {
		t.Fatal("ожидалась ошибка об отсутствующем pg_restore")
	}
	if len(versions) != len(Tools)-1 {
		t.Errorf("версий без pg_restore %d, ожидалось %d", len(versions), len(Tools)-1)
	}
}

// TestClientRoleInfo проверяет разбор прав роли: от них зависит, сможет ли сервис
// создать базу под нового стенда и перенести владельцев объектов.
func TestClientRoleInfo(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		want      Role
		wantError bool
	}{
		{
			name:     "суперпользователь",
			response: "postgres" + FieldSeparator + "t" + FieldSeparator + "t\n",
			want:     Role{Name: "postgres", Superuser: true, CreateDB: true},
		},
		{
			name:     "роль без права создавать базы",
			response: "app_owner" + FieldSeparator + "f" + FieldSeparator + "f\n",
			want:     Role{Name: "app_owner"},
		},
		{name: "пустой ответ", response: "", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := newFakeRunner()
			runner.responses["psql"] = test.response
			role, err := newTestClient(runner).RoleInfo(context.Background())
			if test.wantError {
				if err == nil {
					t.Fatalf("RoleInfo вернула %+v, ожидалась ошибка", role)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if role != test.want {
				t.Errorf("роль = %+v, ожидалось %+v", role, test.want)
			}
		})
	}
}

// TestClientListDatabases проверяет разбор списка баз: владелец, локаль, размер
// и число подключений видны в интерфейсе и в отчёте doctor.
func TestClientListDatabases(t *testing.T) {
	runner := newFakeRunner()
	// Файл testdata использует «|» как разделитель: подменяем его на настоящий.
	rows := strings.ReplaceAll(string(readTestdata(t, "psql_databases.txt")), "|", FieldSeparator)
	runner.responses["psql"] = rows

	databases, err := newTestClient(runner).ListDatabases(context.Background())
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(databases) != 4 {
		t.Fatalf("баз %d, ожидалось 4", len(databases))
	}
	first := databases[0]
	if first.Name != "fse-1234" || first.Owner != "app_owner" {
		t.Errorf("первая база разобрана неверно: %+v", first)
	}
	if first.Collate != "en_US.UTF-8" || first.Ctype != "en_US.UTF-8" {
		t.Errorf("локаль разобрана неверно: %+v", first)
	}
	if first.SizeBytes != 104857600 {
		t.Errorf("размер = %d, ожидалось 104857600", first.SizeBytes)
	}
	if !first.AllowConn || first.IsTemplate {
		t.Errorf("флаги доступа разобраны неверно: %+v", first)
	}
	if first.Connections != 3 {
		t.Errorf("подключений = %d, ожидалось 3", first.Connections)
	}
	last := databases[3]
	if !last.IsTemplate || last.AllowConn {
		t.Errorf("template0 должна быть шаблоном без права подключения: %+v", last)
	}
	if last.Name != "template0" || last.Owner != "postgres" {
		t.Errorf("последняя база разобрана неверно: %+v", last)
	}
}

// TestClientListArchive проверяет чтение оглавления архива: сервер для этого
// не нужен, поэтому проверка проходит даже когда PostgreSQL недоступен.
func TestClientListArchive(t *testing.T) {
	runner := newFakeRunner()
	runner.responses["pg_restore"] = string(readTestdata(t, "pg_restore_list.txt"))

	entries, err := newTestClient(runner).ListArchive(context.Background(), "/var/backups/sqlbrc/fse-1/2026.dump")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(entries) != 7 {
		t.Fatalf("объектов %d, ожидалось 7", len(entries))
	}
	call := runner.lastCall()
	if !strings.Contains(call, "--list /var/backups/sqlbrc/fse-1/2026.dump") {
		t.Errorf("pg_restore вызван не с ключом --list: %s", call)
	}
}

// TestClientQueryError проверяет, что ошибка запроса не проглатывается: пустой
// ответ на сбой нельзя трактовать как «баз нет».
func TestClientQueryError(t *testing.T) {
	runner := newFakeRunner()
	runner.failures["psql"] = "psql: error: connection to server on socket failed"
	_, err := newTestClient(runner).Query(context.Background(), SQLServerVersion)
	if err == nil {
		t.Fatal("ожидалась ошибка запроса")
	}
	if !strings.Contains(err.Error(), "connection to server on socket failed") {
		t.Errorf("ошибка не содержит stderr psql: %v", err)
	}
}

// TestClientExecLogsStderr проверяет, что stderr команды дублируется в журнал
// задачи: по нему разбирают причину сбоя уже после завершения операции.
func TestClientExecLogsStderr(t *testing.T) {
	runner := newFakeRunner()
	runner.failures["pg_dump"] = "pg_dump: error: connection to database failed"
	var log, stderr bytes.Buffer
	client := NewClient(Config{}, runner, &log)

	err := client.Exec(context.Background(), "pg_dump", []string{"--version"}, nil, io.Discard, &stderr)
	if err == nil {
		t.Fatal("ожидалась ошибка запуска pg_dump")
	}
	for name, buffer := range map[string]*bytes.Buffer{"stderr": &stderr, "журнал": &log} {
		if !strings.Contains(buffer.String(), "connection to database failed") {
			t.Errorf("%s не содержит stderr команды: %q", name, buffer.String())
		}
	}

	// Без своего stderr вывод уходит только в журнал: так вызывается pg_dump.
	log.Reset()
	_ = client.Exec(context.Background(), "pg_dump", []string{"--version"}, nil, io.Discard, nil)
	if !strings.Contains(log.String(), "connection to database failed") {
		t.Errorf("без stderr вывод не попал в журнал: %q", log.String())
	}
}
