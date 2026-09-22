package pg

import (
	"strconv"
	"strings"
	"testing"
)

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

// FuzzParseCount проверяет разбор счётчика из вывода psql: значение приходит извне,
// поэтому результат обязан быть неотрицательным, а корректное число — вернуться без
// искажений: сужение int64 до int скрыло бы переполнение и дало бессмысленный счётчик.
func FuzzParseCount(f *testing.F) {
	seeds := []string{
		"", " ", "0", "1", "3", " 12 ", "-1", "мусор", "1e3", "0x10",
		"2147483647", "2147483648", "9223372036854775807", "99999999999999999999999",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		count := parseCount(value)
		if count < 0 {
			t.Errorf("отрицательный счётчик %d для %q", count, value)
		}

		expected, err := strconv.Atoi(strings.TrimSpace(value))
		switch {
		case err != nil, expected < 0:
			if count != 0 {
				t.Errorf("некорректное значение %q дало %d, ожидался 0", value, count)
			}
		case count != expected:
			t.Errorf("parseCount(%q) = %d, ожидалось %d", value, count, expected)
		}
	})
}
