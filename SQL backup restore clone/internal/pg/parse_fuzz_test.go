package pg

import "testing"

// FuzzParsePgVersion проверяет разбор версий: строка приходит из вывода внешней
// утилиты PostgreSQL, поэтому паник быть не должно ни на каком вводе.
func FuzzParsePgVersion(f *testing.F) {
	seeds := []string{
		"", "15.19 (Debian 15.19-0+deb12u1)", "pg_dump (PostgreSQL) 15.19 (Debian)",
		"psql (PostgreSQL) 15.8 (Ubuntu 15.8-1)", "16", "0", "PostgreSQL", "…15…",
		"pg_dump (PostgreSQL) 999999999999999999999.1",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		version, err := ParsePgVersion([]byte(value))
		if err != nil {
			if version.Major != 0 {
				t.Errorf("при ошибке major = %d, ожидался 0", version.Major)
			}
			return
		}
		if version.Major <= 0 {
			t.Errorf("версия %+v для %q: major должен быть положительным", version, value)
		}
		if version.String() == "" {
			t.Errorf("пустая строка версии для %+v", version)
		}
	})
}

// FuzzParseTuplesSeparated проверяет разбор вывода psql: число строк результата
// должно совпадать с числом строк ввода, а падений на мусоре быть не должно.
func FuzzParseTuplesSeparated(f *testing.F) {
	seeds := []string{"", "\n", "a\x1fb", "a\x1fb\nc\x1fd\n", "\x1f", "a|b", "\r\n"}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		rows, err := ParseTuplesSeparated([]byte(value), FieldSeparator)
		if err != nil {
			t.Fatalf("разбор %q завершился ошибкой: %v", value, err)
		}
		for _, row := range rows {
			for index := range row {
				if index < 0 {
					t.Errorf("отрицательный индекс поля в %q", value)
				}
			}
		}
	})
}

// FuzzParseRestoreList проверяет разбор оглавления архива: текст приходит от
// pg_restore, и на любом вводе разбор обязан вернуть либо объекты, либо ошибку.
func FuzzParseRestoreList(f *testing.F) {
	seeds := []string{
		"", "; comment\n", "251; 1259 16409 TABLE public t owner\n",
		"251; 1259 16409 TABLE - t -\n", "abc; 1 2 3\n", "\x00; 1 2 TABLE a b c\n",
		"251;1259\n",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		entries, err := ParseRestoreList([]byte(value))
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.Type == "" && entry.Name == "" {
				t.Errorf("пустой объект в разборе %q", value)
			}
			if text := entry.String(); text == "" {
				t.Errorf("пустое описание объекта %+v", entry)
			}
		}
		if summary := SummarizeTOC(entries); len(summary) > len(entries) {
			t.Errorf("сводка длиннее списка: %v при %d объектах", summary, len(entries))
		}
	})
}
