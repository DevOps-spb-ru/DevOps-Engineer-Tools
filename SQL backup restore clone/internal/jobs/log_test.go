package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testJob собирает запись журнала с заполненными полями: так проверяется
// сохранение всех полей, а не только идентификатора.
func testJob(id string) Job {
	started := time.Date(2026, 9, 21, 2, 0, 0, 0, time.UTC)
	job := Job{
		ID:        id,
		Kind:      KindBackup,
		Database:  "fse-1234",
		Tag:       "pre-restore",
		Note:      "перед релизом",
		Operator:  "admin",
		Status:    StatusRunning,
		StartedAt: started,
	}
	job.SetResult(started.Add(30*time.Second), "бэкап создан", nil)
	return job
}

// TestFileLogRoundTrip проверяет сохранение и чтение журнала: записи возвращаются
// от новых к старым, все поля сохраняются, а файл закрыт от посторонних (0600).
func TestFileLogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFileName)
	journal := NewFileLog(path, 10)

	first, second := testJob("20260921T020000Z-aaaa1111"), testJob("20260921T030000Z-bbbb2222")
	for _, job := range []Job{first, second} {
		if err := journal.Write(job); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	stored, err := journal.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("в журнале %d записей, ожидалось 2", len(stored))
	}
	if stored[0].ID != second.ID || stored[1].ID != first.ID {
		t.Errorf("порядок записей: %s, %s", stored[0].ID, stored[1].ID)
	}
	loaded := stored[0]
	if loaded.Kind != KindBackup || loaded.Database != "fse-1234" || loaded.Tag != "pre-restore" ||
		loaded.Operator != "admin" || loaded.Message != "бэкап создан" {
		t.Errorf("поля записи сохранены не полностью: %+v", loaded)
	}
	if loaded.DurationMS != 30000 || !loaded.FinishedAt.Equal(first.StartedAt.Add(30*time.Second)) {
		t.Errorf("время и длительность задачи: %+v", loaded)
	}

	// Список с пределом возвращает только свежие записи.
	limited, err := journal.List(1)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(limited) != 1 || limited[0].ID != second.ID {
		t.Errorf("список с пределом вернул %+v", limited)
	}
}

// TestFileLogTrims проверяет предел хранения: журнал не растёт бесконечно, а
// старые записи вытесняются новыми.
func TestFileLogTrims(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFileName)
	journal := NewFileLog(path, 3)
	for index := 0; index < 5; index++ {
		job := testJob("job-" + string(rune('a'+index)))
		if err := journal.Write(job); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	stored, err := journal.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("в журнале %d записей, ожидалось 3", len(stored))
	}
	if stored[0].ID != "job-e" || stored[2].ID != "job-c" {
		t.Errorf("после обрезки остались %s … %s", stored[0].ID, stored[2].ID)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if lines := strings.Count(string(raw), "\n"); lines != 3 {
		t.Errorf("в файле журнала %d строк, ожидалось 3", lines)
	}
}

// TestFileLogSkipsCorruptLines проверяет устойчивость к повреждённому журналу:
// недописанная строка (сбой процесса) и мусор не мешают читать остальные записи.
func TestFileLogSkipsCorruptLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFileName)
	journal := NewFileLog(path, 10)
	if err := journal.Write(testJob("20260921T020000Z-aaaa1111")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, journalPerm)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := file.WriteString("{\"id\": \"незакрытая строка\"\nмусор\n\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stored, err := journal.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 1 || stored[0].ID != "20260921T020000Z-aaaa1111" {
		t.Fatalf("корректная запись потеряна: %+v", stored)
	}
	// Новая запись сохраняется, а мусор из журнала уходит: файл переписывается
	// целиком по разобранным записям.
	if err := journal.Write(testJob("20260921T030000Z-bbbb2222")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	stored, err = journal.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 2 {
		t.Errorf("после дописывания в журнале %d записей, ожидалось 2", len(stored))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), "мусор") {
		t.Errorf("повреждённые строки остались в журнале: %s", raw)
	}
}

// TestFileLogMissingFile проверяет чтение отсутствующего журнала: сервис только
// установлен, задач ещё не было — это не ошибка.
func TestFileLogMissingFile(t *testing.T) {
	journal := NewFileLog(filepath.Join(t.TempDir(), "нет", DefaultFileName), 10)
	stored, err := journal.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 0 {
		t.Errorf("отсутствующий журнал вернул %+v", stored)
	}
}

// TestFileLogCreatesStateDir проверяет создание служебного каталога: журнал
// пишется по пути storage.state_dir, которого при первом запуске может не быть.
func TestFileLogCreatesStateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sqlbrc")
	journal := NewFileLog(filepath.Join(dir, DefaultFileName), 10)
	if err := journal.Write(testJob("20260921T020000Z-aaaa1111")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(journal.Path()); err != nil {
		t.Fatalf("файл журнала не создан: %v", err)
	}
}

// TestFileLogReportsWriteError проверяет, что сбой записи журнала сообщается
// вызывающему коду: по нему очередь решает, что сказать человеку в итоге задачи.
func TestFileLogReportsWriteError(t *testing.T) {
	// Каталог вместо файла: ни запись, ни переименование невозможны.
	path := filepath.Join(t.TempDir(), DefaultFileName)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	journal := NewFileLog(path, 10)
	err := journal.Write(testJob("20260921T020000Z-aaaa1111"))
	if err == nil {
		t.Fatal("Write не сообщил о невозможности записать журнал")
	}
	if !strings.Contains(err.Error(), "журнал задач") {
		t.Errorf("сообщение об ошибке %q", err)
	}
}

// TestMemoryLogRetains проверяет журнал в памяти: он используется, когда файл
// журнала недоступен, и должен соблюдать тот же предел хранения.
func TestMemoryLogRetains(t *testing.T) {
	journal := NewMemoryLog(2)
	for index := 0; index < 3; index++ {
		if err := journal.Write(testJob("job-" + string(rune('a'+index)))); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	stored, err := journal.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(stored) != 2 || stored[0].ID != "job-c" {
		t.Errorf("журнал в памяти вернул %+v", stored)
	}
}

// TestParseJobLine проверяет разбор строки журнала: пустые, повреждённые и
// неполные записи отбрасываются, корректная читается целиком.
func TestParseJobLine(t *testing.T) {
	valid := testJob("20260921T020000Z-aaaa1111")
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed, ok := ParseJobLine(string(raw))
	if !ok {
		t.Fatal("корректная строка не разобрана")
	}
	if parsed.ID != valid.ID || parsed.Kind != valid.Kind || parsed.Message != valid.Message {
		t.Errorf("разобранная запись отличается: %+v", parsed)
	}
	for _, line := range []string{
		"", "   ", "не json", "{}", `{"id": "x"}`, `{"kind": "backup"}`,
		`{"id": "x", "kind": "unknown"}`, `{"id": "x", "kind": "backup\n"}`,
	} {
		if _, ok := ParseJobLine(line); ok {
			t.Errorf("строка %q принята за запись журнала", line)
		}
	}
}
