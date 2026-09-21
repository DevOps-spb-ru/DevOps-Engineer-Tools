package jobs

import (
	"strings"
	"testing"
)

// FuzzParseJobLine проверяет разбор строки журнала: файл журнала может быть
// исправлен вручную или повреждён сбоем процесса, поэтому на любом вводе функция
// обязана либо вернуть запись, либо признак отказа, и не паниковать.
func FuzzParseJobLine(f *testing.F) {
	seeds := []string{
		"",
		" ",
		"\n",
		"{}",
		"не json",
		`{"id":"20260921T020000Z-aaaa1111","kind":"backup","database":"fse-1234"}`,
		`{"id":"x","kind":"backup","status":"running","started_at":"2026-09-21T02:00:00Z"}`,
		`{"id":"x","kind":""}`,
		`{"id":"","kind":"backup"}`,
		`{"id":"x","kind":"backup","duration_ms":-1}`,
		`{"id":"x","kind":"backup","started_at":"не дата"}`,
		`{"id":"x","kind":"backup","note":"строка с \n переводом"}`,
		"{\"id\": \"x\", \"kind\": \"backup\"",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, line string) {
		job, ok := ParseJobLine(line)
		if !ok {
			return
		}
		// Запись без идентификатора или с неизвестным видом задачи бесполезна:
		// по ней нельзя понять, что делал сервис.
		if job.ID == "" || !IsKnownKind(job.Kind) {
			t.Fatalf("строка %q разобрана без идентификатора или вида задачи", line)
		}
		// Разобранная запись должна оставаться одной строкой журнала: иначе
		// следующая запись склеится с ней и журнал станет нечитаемым.
		if strings.ContainsAny(job.ID+string(job.Kind)+job.Message+job.Error, "\r\n") {
			t.Fatalf("в записи строки %q появился перевод строки: %+v", line, job)
		}
	})
}
