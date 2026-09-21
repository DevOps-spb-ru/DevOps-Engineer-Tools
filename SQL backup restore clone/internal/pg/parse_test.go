package pg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readTestdata читает файл из testdata: тесты разбирают реальные форматы вывода
// pg_dump, psql и pg_restore, а не строки, придуманные под реализацию.
func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	// #nosec G304 -- путь собирается из имени файла теста в каталоге testdata.
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("не удалось прочитать testdata/%s: %v", name, err)
	}
	return data
}

func TestParsePgVersion(t *testing.T) {
	fromFile := readTestdata(t, "pg_dump_version.txt")

	tests := []struct {
		name    string
		input   string
		want    Version
		wantErr bool
	}{
		{name: "pg_dump из Debian", input: string(fromFile), want: Version{Major: 15, Minor: 19}},
		{
			name:  "psql",
			input: "psql (PostgreSQL) 15.8 (Ubuntu 15.8-1.pgdg22.04+1)\n",
			want:  Version{Major: 15, Minor: 8},
		},
		{
			name:  "SHOW server_version",
			input: string(readTestdata(t, "server_version.txt")),
			want:  Version{Major: 15, Minor: 19},
		},
		{name: "без слова PostgreSQL", input: "16.1", want: Version{Major: 16, Minor: 1}},
		{name: "patch-версия", input: "postgres (PostgreSQL) 15.4.2", want: Version{Major: 15, Minor: 4, Patch: 2}},
		{name: "пустой ввод", input: "", wantErr: true},
		{name: "только текст", input: "версия не определена", wantErr: true},
		{name: "нулевая версия", input: "0.0", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParsePgVersion([]byte(test.input))
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParsePgVersion(%q): ожидалась ошибка, получено %+v", test.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePgVersion(%q): неожиданная ошибка: %v", test.input, err)
			}
			if got.Major != test.want.Major || got.Minor != test.want.Minor || got.Patch != test.want.Patch {
				t.Errorf("ParsePgVersion(%q) = %s, ожидалось %d.%d.%d",
					test.input, got, test.want.Major, test.want.Minor, test.want.Patch)
			}
			if got.Raw == "" {
				t.Errorf("ParsePgVersion(%q): исходная строка версии не сохранена", test.input)
			}
		})
	}
}

func TestVersionString(t *testing.T) {
	tests := []struct {
		name    string
		version Version
		want    string
	}{
		{name: "major и minor", version: Version{Major: 15, Minor: 19}, want: "15.19"},
		{name: "только major", version: Version{Major: 16}, want: "16"},
		{name: "с патчем", version: Version{Major: 15, Minor: 4, Patch: 2}, want: "15.4.2"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.version.String(); got != test.want {
				t.Errorf("Version.String() = %q, ожидалось %q", got, test.want)
			}
		})
	}
}

func TestVersionAtLeastMajor(t *testing.T) {
	version := Version{Major: 15, Minor: 19}
	if !version.AtLeastMajor(15) {
		t.Errorf("15.19 должна быть не старее 15")
	}
	if version.AtLeastMajor(16) {
		t.Errorf("15.19 не должна считаться не старее 16")
	}
	if !version.AtLeastMajor(14) {
		t.Errorf("15.19 должна быть не старее 14")
	}
}

func TestParseTuples(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		separator string
		want      []Row
		wantErr   bool
	}{
		{
			name:      "несколько строк",
			input:     "a\x1fb\x1fc\n1\x1f2\x1f3\n",
			separator: FieldSeparator,
			want:      []Row{{"a", "b", "c"}, {"1", "2", "3"}},
		},
		{name: "пустой ввод", input: "", separator: FieldSeparator, want: nil},
		{name: "только пробелы", input: "  \n", separator: FieldSeparator, want: nil},
		{
			name:      "пустые значения остаются полями",
			input:     "a\x1f\x1fb\n",
			separator: FieldSeparator,
			want:      []Row{{"a", "", "b"}},
		},
		{
			name:      "переводы строк CRLF",
			input:     "a\x1fb\r\nc\x1fd\r\n",
			separator: FieldSeparator,
			want:      []Row{{"a", "b"}, {"c", "d"}},
		},
		{name: "пустой разделитель", input: "a\n", separator: "", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseTuplesSeparated([]byte(test.input), test.separator)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ожидалась ошибка разбора %q", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTuplesSeparated(%q): неожиданная ошибка: %v", test.input, err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("строк %d, ожидалось %d (%v)", len(got), len(test.want), got)
			}
			for index := range test.want {
				if strings.Join(got[index], "|") != strings.Join(test.want[index], "|") {
					t.Errorf("строка %d = %v, ожидалось %v", index, got[index], test.want[index])
				}
			}
		})
	}
}

func TestParseTuplesFromPsqlOutput(t *testing.T) {
	rows, err := ParseTuplesSeparated(readTestdata(t, "psql_databases.txt"), "|")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("баз %d, ожидалось 4 (%v)", len(rows), rows)
	}
	first := rows[0]
	if got := first.Field(0); got != "fse-1234" {
		t.Errorf("имя базы = %q", got)
	}
	if got := first.Field(1); got != "app_owner" {
		t.Errorf("владелец = %q", got)
	}
	if got := first.Field(6); got != "104857600" {
		t.Errorf("размер = %q", got)
	}
	// Отсутствующее поле не должно падать: разбор не зависит от числа колонок.
	if got := first.Field(42); got != "" {
		t.Errorf("поле за пределами строки = %q, ожидалась пустая строка", got)
	}
	if got := first.Field(-1); got != "" {
		t.Errorf("отрицательный индекс = %q, ожидалась пустая строка", got)
	}
}

func TestParseRestoreList(t *testing.T) {
	entries, err := ParseRestoreList(readTestdata(t, "pg_restore_list.txt"))
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(entries) != 7 {
		t.Fatalf("объектов %d, ожидалось 7 (%v)", len(entries), entries)
	}
	first := entries[0]
	if first.Type != "TABLE" || first.Schema != "public" || first.Name != "alembic_version" || first.Owner != "" {
		t.Errorf("первый объект разобран неверно: %+v", first)
	}
	// У объекта без владельца pg_restore печатает «-»: в метаданных это пустое значение.
	last := entries[len(entries)-1]
	if last.Owner != "" {
		t.Errorf("владелец SEQUENCE SET = %q, ожидалась пустая строка", last.Owner)
	}
	if last.Type != "SEQUENCE SET" {
		t.Errorf("тип последнего объекта = %q", last.Type)
	}

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "только комментарии", input: "; Archive created at 2026-09-21\n;\n", wantErr: true},
		{name: "пустой ввод", input: ""},
		{
			name:  "мусорные строки пропускаются",
			input: "не число; 1 2 TABLE public t owner\n251; 1259 16409 TABLE public t owner\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseRestoreList([]byte(test.input))
			if test.wantErr {
				if err == nil {
					t.Fatalf("ожидалась ошибка разбора %q", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if test.name == "мусорные строки пропускаются" && len(got) != 1 {
				t.Errorf("объектов %d, ожидался 1", len(got))
			}
		})
	}
}

func TestSummarizeTOC(t *testing.T) {
	entries, err := ParseRestoreList(readTestdata(t, "pg_restore_list.txt"))
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	summary := SummarizeTOC(entries)
	want := []string{"DEFAULT 1", "INDEX 1", "SEQUENCE 1", "SEQUENCE OWNED BY 1", "SEQUENCE SET 1", "TABLE 2"}
	if strings.Join(summary, ", ") != strings.Join(want, ", ") {
		t.Errorf("сводка = %v, ожидалось %v", summary, want)
	}
	if summary := SummarizeTOC(nil); len(summary) != 0 {
		t.Errorf("сводка по пустому списку = %v", summary)
	}
}

func TestTOCEntryString(t *testing.T) {
	tests := []struct {
		name  string
		entry TOCEntry
		want  string
	}{
		{
			name:  "со схемой и владельцем",
			entry: TOCEntry{Type: "TABLE", Schema: "public", Name: "users", Owner: "app_owner"},
			want:  "TABLE public.users (владелец app_owner)",
		},
		{
			name:  "без схемы и владельца",
			entry: TOCEntry{Type: "SEQUENCE SET", Name: "users_id_seq"},
			want:  "SEQUENCE SET users_id_seq",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.entry.String(); got != test.want {
				t.Errorf("TOCEntry.String() = %q, ожидалось %q", got, test.want)
			}
		})
	}
}

// TestParseTOCLine проверяет разбор отдельных строк pg_restore --list, включая
// многословные описания объектов: границу между описанием и схемой приходится
// восстанавливать по списку tocDescriptors, и ошибка здесь ломает всю сводку.
func TestParseTOCLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    TOCEntry
		wantErr bool
	}{
		{
			name: "таблица",
			line: "218; 1259 16409 TABLE public alembic_version ",
			want: TOCEntry{Type: "TABLE", Schema: "public", Name: "alembic_version", Owner: ""},
		},
		{
			name: "последовательность",
			line: "3375; 1259 16427 SEQUENCE public users_id_seq ",
			want: TOCEntry{Type: "SEQUENCE", Schema: "public", Name: "users_id_seq", Owner: ""},
		},
		{
			name: "описание из двух слов",
			line: "3377; 0 0 SEQUENCE SET public users_id_seq -",
			want: TOCEntry{Type: "SEQUENCE SET", Schema: "public", Name: "users_id_seq"},
		},
		{
			name: "описание из трёх слов",
			line: "3367; 0 0 SEQUENCE OWNED BY public users_id_seq ",
			want: TOCEntry{Type: "SEQUENCE OWNED BY", Schema: "public", Name: "users_id_seq", Owner: ""},
		},
		{
			name: "база данных без схемы",
			line: "1; 3079 16384 DATABASE - fse-1234 ",
			want: TOCEntry{Type: "DATABASE", Name: "fse-1234", Owner: ""},
		},
		{
			// Владелец стоит последним полем, поэтому имя объекта из двух слов
			// заканчивается перед ним.
			name: "имя из двух слов",
			line: "300; 2604 16418 DEFAULT public users id app_owner ",
			want: TOCEntry{Type: "DEFAULT", Schema: "public", Name: "users id", Owner: "app_owner"},
		},
		{name: "без идентификатора", line: "не число; 1259 16409 TABLE public t owner", wantErr: true},
		{name: "слишком мало полей", line: "251; 1259", wantErr: true},
		{name: "только описание", line: "251; 1259 16409 TABLE", wantErr: true},
		{
			// Усечённая строка: имя берётся как есть, падать на ней не нужно.
			name: "без схемы и владельца",
			line: "251; 1259 16409 TABLE users",
			want: TOCEntry{Type: "TABLE", Name: "users"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseTOCLine(test.line)
			if test.wantErr {
				if ok {
					t.Fatalf("parseTOCLine(%q) вернула %+v, ожидалась ошибка", test.line, got)
				}
				return
			}
			if !ok {
				t.Fatalf("parseTOCLine(%q): строка не разобрана", test.line)
			}
			if got != test.want {
				t.Errorf("parseTOCLine(%q) = %+v, ожидалось %+v", test.line, got, test.want)
			}
		})
	}
}

// TestTOCDescriptorsOrder проверяет инвариант списка описаний: он должен быть
// отсортирован по убыванию числа слов, иначе длинное описание («SEQUENCE SET»)
// перестанет находиться и превратится в тип «SEQUENCE» с чужим именем.
func TestTOCDescriptorsOrder(t *testing.T) {
	previous := len(strings.Fields(tocDescriptors[0]))
	seen := make(map[string]bool, len(tocDescriptors))
	for _, descriptor := range tocDescriptors {
		words := len(strings.Fields(descriptor))
		if words > previous {
			t.Fatalf("описание %q (%d слов) стоит после описания из %d слов: список не отсортирован",
				descriptor, words, previous)
		}
		if words == 0 {
			t.Fatalf("пустое описание в списке")
		}
		if seen[descriptor] {
			t.Fatalf("описание %q повторяется", descriptor)
		}
		seen[descriptor] = true
		previous = words
	}
}

// TestParseTOCLineForEveryDescriptor проверяет, что каждое описание из списка
// распознаётся: строка собирается по формату pg_restore и разбирается обратно.
func TestParseTOCLineForEveryDescriptor(t *testing.T) {
	for _, descriptor := range tocDescriptors {
		t.Run(descriptor, func(t *testing.T) {
			line := "251; 1259 16409 " + descriptor + " public users "
			got, ok := parseTOCLine(line)
			if !ok {
				t.Fatalf("строка %q не разобрана", line)
			}
			if got.Type != descriptor {
				t.Errorf("тип = %q, ожидалось %q", got.Type, descriptor)
			}
			if got.Name != "users" || got.Schema != "public" || got.Owner != "" {
				t.Errorf("поля разобраны неверно: %+v", got)
			}
		})
	}
}
