package store

import (
	"strings"
	"testing"
)

// FuzzParseArchiveName проверяет разбор имён файлов: имя приходит из каталога, куда
// может попасть любой файл (администратор, другой инструмент), поэтому на любом
// вводе функция обязана либо вернуть корректное имя, либо признак отказа.
// Разобранное имя участвует в построении пути к файлу, поэтому опасных символов в
// нём быть не должно.
func FuzzParseArchiveName(f *testing.F) {
	seeds := []string{
		"20260921T020000Z.dump",
		"20260921T020000Z-pre-restore.dump",
		"20260921T020000Z.dump.part",
		"20260921T020000Z.json",
		"20260921T020000Z.dump.dump",
		"", ".", "..", "-.dump", ".dump", "dump",
		"2026-09-21.dump", "20260921T020000Z-.dump", "20260921T020000Z-..dump",
		"../20260921T020000Z.dump", "..\\..\\etc\\passwd.dump",
		"20260921T020000Z-pre restore.dump", "20260921T020000Z/../../../etc/passwd.dump",
		"00010101T000000Z.dump", "99999999T999999Z.dump",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, fileName string) {
		parsed, ok := ParseArchiveName(fileName)
		if !ok {
			return
		}
		if parsed.Time.IsZero() {
			t.Fatalf("имя %q разобрано, но время создания пустое", fileName)
		}
		// Разбор и сборка имени должны быть согласованы: иначе команда restore
		// ищет файл не по тому имени, которое показал список бэкапов.
		restored, err := FormatArchiveName(parsed.Time, parsed.Tag)
		if err != nil {
			t.Fatalf("имя %q разобрано, но обратная сборка не прошла: %v", fileName, err)
		}
		if restored != fileName {
			t.Fatalf("имя %q собрано обратно как %q", fileName, restored)
		}
		if strings.ContainsAny(fileName, `/\`) {
			t.Fatalf("разделитель пути в имени %q прошёл разбор", fileName)
		}
		if fileName == "." || fileName == ".." {
			t.Fatalf("имя %q прошло разбор", fileName)
		}
		if strings.ContainsRune(fileName, 0) {
			t.Fatalf("нулевой байт в имени %q прошёл разбор", fileName)
		}
	})
}
