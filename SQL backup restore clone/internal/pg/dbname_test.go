package pg

import (
	"strings"
	"testing"
)

func TestValidateDBName(t *testing.T) {
	tests := []struct {
		name     string
		database string
		pattern  string
		wantErr  bool
	}{
		{name: "стенд по умолчанию", database: "fse-1234"},
		{name: "другой префикс стенда", database: "fssd-7"},
		{name: "дефис и цифра в префиксе", database: "dops-fix-42"},
		{name: "пользовательский шаблон", database: "stand-1", pattern: `^(stand)-[a-z0-9-]+$`},
		{name: "чужой префикс", database: "prod-1", pattern: `^fse-[0-9]+$`, wantErr: true},
		{name: "имя без номера стенда", database: "fse-prod", wantErr: true},
		{name: "номер не в конце имени", database: "fse-1234-x", wantErr: true},
		{name: "префикс без номера", database: "fse-", wantErr: true},
		{name: "шаблон базы с цифрой", database: "template0", wantErr: true},
		{name: "пустое имя", database: "", wantErr: true},
		{name: "пробел", database: " ", wantErr: true},
		{name: "служебная база не подходит под шаблон", database: "postgres", wantErr: true},
		{name: "верхний регистр", database: "FSE-1234", wantErr: true},
		{name: "точка с запятой", database: "fse-1234;drop database postgres", wantErr: true},
		{name: "обход каталогов", database: "../../etc/passwd", wantErr: true},
		{name: "точка в начале", database: ".fse", wantErr: true},
		{name: "перевод строки", database: "fse-1234\n--", wantErr: true},
		{name: "юникод", database: "стенд", wantErr: true},
		{name: "длиннее идентификатора", database: "-" + strings.Repeat("a", 60), wantErr: true},
		{name: "некорректный шаблон", database: "fse-1", pattern: "[", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDBName(test.database, test.pattern)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ValidateDBName(%q) вернула nil, ожидалась ошибка", test.database)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateDBName(%q): неожиданная ошибка: %v", test.database, err)
			}
		})
	}
}

func TestValidateTag(t *testing.T) {
	tests := []struct {
		tag     string
		wantErr bool
	}{
		{tag: "before-release"},
		{tag: "release_1.2"},
		{tag: "a"},
		{tag: "", wantErr: true},
		{tag: " ", wantErr: true},
		{tag: "Release-1", wantErr: true},
		{tag: "метка", wantErr: true},
		{tag: "tag with space", wantErr: true},
		{tag: strings.Repeat("a", 33), wantErr: true},
		{tag: "-начинается-с-дефиса", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.tag, func(t *testing.T) {
			err := ValidateTag(test.tag)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ValidateTag(%q) вернула nil, ожидалась ошибка", test.tag)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateTag(%q): неожиданная ошибка: %v", test.tag, err)
			}
		})
	}
}

func TestIsReservedDatabase(t *testing.T) {
	for _, name := range ReservedDatabases {
		if !IsReservedDatabase(name) {
			t.Errorf("%q должна считаться служебной базой", name)
		}
	}
	for _, name := range []string{"fse-1234", "postgresql", "template"} {
		if IsReservedDatabase(name) {
			t.Errorf("%q не должна считаться служебной базой", name)
		}
	}
}

func TestSplitBackupID(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		wantDB   string
		wantName string
		wantErr  bool
	}{
		{
			name:     "обычный идентификатор",
			id:       "fse-1234/20260921T020000Z",
			wantDB:   "fse-1234",
			wantName: "20260921T020000Z",
		},
		{
			name:     "служебный бэкап перед восстановлением",
			id:       "fse-1234/20260921T020000Z-pre-restore",
			wantDB:   "fse-1234",
			wantName: "20260921T020000Z-pre-restore",
		},
		{name: "без разделителя", id: "fse-1234", wantErr: true},
		{name: "обход каталогов", id: "fse-1234/../../etc/passwd", wantErr: true},
		{name: "лишний разделитель", id: "fse-1234/2026/09", wantErr: true},
		{name: "чужая база", id: "postgres/20260921T020000Z", wantErr: true},
		{name: "пустое значение", id: "", wantErr: true},
		{name: "пустое имя файла", id: "fse-1234/", wantErr: true},
		{name: "точка в имени файла", id: "fse-1234/..", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, fileName, err := SplitBackupID(test.id)
			if test.wantErr {
				if err == nil {
					t.Fatalf("SplitBackupID(%q) вернула %q/%q, ожидалась ошибка", test.id, database, fileName)
				}
				return
			}
			if err != nil {
				t.Fatalf("SplitBackupID(%q): неожиданная ошибка: %v", test.id, err)
			}
			if database != test.wantDB || fileName != test.wantName {
				t.Errorf("SplitBackupID(%q) = %q/%q, ожидалось %q/%q",
					test.id, database, fileName, test.wantDB, test.wantName)
			}
		})
	}
}

func TestValidateIdentifier(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "обычная роль", value: "postgres"},
		{name: "подчёркивание в начале", value: "_internal"},
		{name: "пустое значение", value: "", wantErr: true},
		{name: "дефис", value: "fse-1234", wantErr: true},
		{name: "цифра в начале", value: "1stand", wantErr: true},
		{name: "пробел", value: "post gres", wantErr: true},
		{name: "кавычка", value: `"postgres"`, wantErr: true},
		{name: "длиннее идентификатора", value: strings.Repeat("a", MaxIdentifierLength+1), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateIdentifier(test.value, "field")
			if test.wantErr {
				if err == nil {
					t.Fatalf("ValidateIdentifier(%q) вернула nil, ожидалась ошибка", test.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateIdentifier(%q): неожиданная ошибка: %v", test.value, err)
			}
		})
	}
}

func TestValidateTool(t *testing.T) {
	for _, tool := range Tools {
		if err := ValidateTool(tool); err != nil {
			t.Errorf("ValidateTool(%q): неожиданная ошибка: %v", tool, err)
		}
	}
	for _, tool := range []string{"sh", "bash", "pg_dump.exe", "/usr/bin/pg_dump", "", "rm"} {
		if err := ValidateTool(tool); err == nil {
			t.Errorf("ValidateTool(%q) вернула nil: произвольные бинари запускать нельзя", tool)
		}
	}
}
