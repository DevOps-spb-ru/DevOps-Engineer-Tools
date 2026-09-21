package pg

import (
	"strings"
	"testing"
)

// TestQuoteIdentifier проверяет квотирование идентификаторов: имена обслуживаемых
// баз содержат дефис, а кавычка внутри имени должна удваиваться, иначе запрос
// разорвётся и часть значения станет командой.
func TestQuoteIdentifier(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"fse-1234", `"fse-1234"`},
		{"postgres", `"postgres"`},
		{"fse-1234-", `"fse-1234-"`},
		{`bad"name`, `"bad""name"`},
		{`"; DROP DATABASE postgres; --`, `"""; DROP DATABASE postgres; --"`},
	}
	for _, testCase := range cases {
		if got := QuoteIdentifier(testCase.name); got != testCase.want {
			t.Errorf("QuoteIdentifier(%q) = %s, ожидалось %s", testCase.name, got, testCase.want)
		}
	}
}

// TestQuoteLiteral проверяет подстановку значений в строковый литерал: локаль и
// имя базы приходят из конфига и каталога сервера, поэтому апостроф обязан
// удваиваться.
func TestQuoteLiteral(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"локаль", "ru_RU.UTF-8", "'ru_RU.UTF-8'"},
		{"база с дефисом", "fse-1234", "'fse-1234'"},
		{"апостроф", "it's", "'it''s'"},
		{"инъекция", "x'; DROP DATABASE postgres; --", "'x''; DROP DATABASE postgres; --'"},
	}
	for _, testCase := range cases {
		if got := QuoteLiteral(testCase.value); got != testCase.want {
			t.Errorf("%s: QuoteLiteral(%q) = %s, ожидалось %s",
				testCase.name, testCase.value, got, testCase.want)
		}
	}
}

// TestSQLDatabaseQueries проверяет служебные запросы: имя базы подставляется
// литералом, поэтому база с дефисом не превращается в незакрытую строку, а
// счётчики подключений исключают собственный бэкенд сервиса.
func TestSQLDatabaseQueries(t *testing.T) {
	exists := sqlDatabaseExists("fse-1234")
	if !strings.Contains(exists, "datname = 'fse-1234'") {
		t.Errorf("запрос проверки базы: %q", exists)
	}
	size := sqlDatabaseSize("fse-1234")
	if size != "SELECT pg_database_size('fse-1234')" {
		t.Errorf("запрос размера базы: %q", size)
	}
	for name, query := range map[string]string{
		"sessions":  sqlDatabaseSessions("fse-1234"),
		"terminate": sqlTerminateBackends("fse-1234"),
	} {
		if !strings.Contains(query, "datname = 'fse-1234'") {
			t.Errorf("запрос %s: %q", name, query)
		}
		if !strings.Contains(query, "pid <> pg_backend_pid()") {
			t.Errorf("запрос %s не исключает собственное подключение: %q", name, query)
		}
	}
	locale := sqlDatabaseLocale("fse-1234")
	for _, field := range []string{"pg_encoding_to_char(encoding)", "datcollate", "datctype"} {
		if !strings.Contains(locale, field) {
			t.Errorf("запрос параметров базы не содержит %q: %q", field, locale)
		}
	}
}

// TestSQLCreateDatabase проверяет сборку CREATE DATABASE: имя, владелец и локаль
// попадают в запрос в закавыченном виде, а чужая база отвергается до запроса.
func TestSQLCreateDatabase(t *testing.T) {
	query, err := sqlCreateDatabase(CreateDatabaseOptions{
		Database: "fssd-7",
		Owner:    "postgres",
		Template: "template0",
		Encoding: "UTF8",
		Collate:  "ru_RU.UTF-8",
		Ctype:    "ru_RU.UTF-8",
	})
	if err != nil {
		t.Fatalf("sqlCreateDatabase: %v", err)
	}
	want := `CREATE DATABASE "fssd-7" TEMPLATE "template0" OWNER "postgres" ` +
		`ENCODING 'UTF8' LC_COLLATE 'ru_RU.UTF-8' LC_CTYPE 'ru_RU.UTF-8'`
	if query != want {
		t.Errorf("запрос:\n  %s\nожидалось:\n  %s", query, want)
	}
	if _, err := sqlCreateDatabase(CreateDatabaseOptions{Database: "prod"}); err == nil {
		t.Error("чужая база прошла проверку при создании")
	}
	if _, err := sqlCreateDatabase(CreateDatabaseOptions{Database: "fse-1234", Owner: "bad role"}); err == nil {
		t.Error("недопустимый владелец прошёл проверку")
	}
	if _, err := sqlCreateDatabase(CreateDatabaseOptions{Database: "fse-1234", Collate: "ru\nRU"}); err == nil {
		t.Error("локаль с переводом строки прошла проверку")
	}
}

// TestSQLDropAndAlterDatabase проверяет удаление и смену владельца: оба запроса
// касаются существующей базы, поэтому её имя проверяется по шаблону.
func TestSQLDropAndAlterDatabase(t *testing.T) {
	drop, err := sqlDropDatabase("fse-1234", "")
	if err != nil {
		t.Fatalf("sqlDropDatabase: %v", err)
	}
	if drop != `DROP DATABASE IF EXISTS "fse-1234"` {
		t.Errorf("запрос удаления: %q", drop)
	}
	if _, err := sqlDropDatabase("postgres", ""); err == nil {
		t.Error("служебная база прошла проверку при удалении")
	}
	alter, err := sqlAlterDatabaseOwner("fse-1234", "postgres", "")
	if err != nil {
		t.Fatalf("sqlAlterDatabaseOwner: %v", err)
	}
	if alter != `ALTER DATABASE "fse-1234" OWNER TO "postgres"` {
		t.Errorf("запрос смены владельца: %q", alter)
	}
	if _, err := sqlAlterDatabaseOwner("fse-1234", "", ""); err == nil {
		t.Error("пустой владелец прошёл проверку")
	}
}
