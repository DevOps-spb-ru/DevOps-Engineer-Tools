package pg

import "testing"

// FuzzValidateDBName проверяет проверку имён баз: имя приходит из веб-формы,
// поэтому на любом вводе функция обязана вернуть либо nil, либо ошибку,
// а разрешённое имя должно быть безопасно для argv и для пути к файлу.
func FuzzValidateDBName(f *testing.F) {
	seeds := []string{
		"fse-1234", "postgres", "template0", "", " ", "-", ".", "..",
		"fse-1234; rm -rf /", "../../etc/passwd", "FSE-1234",
		"fse-1234\n--", " fse ", "fse-1234\x00",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, name string) {
		if err := ValidateDBName(name, DefaultAllowPattern); err != nil {
			return
		}
		if IsReservedDatabase(name) {
			t.Errorf("служебная база %q прошла проверку", name)
		}
		for _, r := range name {
			switch r {
			case '/', '\\', '\'', '"', ';', ' ', '\t', '\n', '\r', '\x00', '$', '`', '|', '&', '*':
				t.Errorf("опасный символ %q разрешён в имени %q", r, name)
			}
		}
	})
}

// FuzzSplitBackupID проверяет разбор идентификатора бэкапа: значение приходит
// из веб-формы и участвует в построении пути к файлу.
func FuzzSplitBackupID(f *testing.F) {
	seeds := []string{
		"fse-1234/20260921T020000Z", "fse-1234/20260921T020000Z-pre-restore",
		"fse-1234", "/20260921T020000Z", "fse-1234/../etc/passwd",
		"fse-1234/", "fse-1234/a/b", "",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, id string) {
		database, name, err := SplitBackupID(id)
		if err != nil {
			return
		}
		if database == "" || name == "" {
			t.Errorf("пустые части в идентификаторе %q: %q, %q", id, database, name)
		}
		if err := ValidateBackupID(id); err != nil {
			t.Errorf("ValidateBackupID(%q) вернула ошибку после успешного разбора: %v", id, err)
		}
		for _, part := range []string{database, name} {
			if part == "." || part == ".." {
				t.Errorf("часть %q в идентификаторе %q недопустима", part, id)
			}
		}
	})
}
