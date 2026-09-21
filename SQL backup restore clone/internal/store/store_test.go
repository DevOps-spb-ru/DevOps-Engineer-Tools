package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/bytesize"
)

// TestBeginCommitWritesArchiveAndMetadata проверяет полный путь записи: данные
// идут в .part, затем одним переименованием появляется архив, а рядом — файл
// метаданных с контрольной суммой и параметрами дампа.
func TestBeginCommitWritesArchiveAndMetadata(t *testing.T) {
	store, dir := newTestStore(t)
	writer, err := store.Begin(BeginOptions{
		Database:          "fse-1234",
		Note:              "перед релизом",
		JobID:             "job-1",
		ServerVersion:     "15.14",
		DatabaseSizeBytes: 2048,
		Now:               testNow,
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if writer.Name() != "20260921T020000Z.dump" {
		t.Fatalf("имя бэкапа %q, ожидалось 20260921T020000Z.dump", writer.Name())
	}
	if filepath.Ext(writer.PartialPath()) != PartialExt {
		t.Fatalf("во время записи имя файла %q не оканчивается на %s", writer.PartialPath(), PartialExt)
	}
	payload := []byte("PGDMP-данные стенда")
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if writer.Size() != int64(len(payload)) {
		t.Errorf("записано %d байт, ожидалось %d", writer.Size(), len(payload))
	}
	backup, err := writer.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	sum := sha256.Sum256(payload)
	if backup.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 = %q, ожидался %q", backup.SHA256, hex.EncodeToString(sum[:]))
	}
	if backup.SizeBytes != int64(len(payload)) {
		t.Errorf("размер бэкапа %d, ожидался %d", backup.SizeBytes, len(payload))
	}
	if backup.ID != "fse-1234/20260921T020000Z" {
		t.Errorf("идентификатор бэкапа %q", backup.ID)
	}
	if backup.Status != StatusComplete {
		t.Errorf("состояние бэкапа %q, ожидалось %q", backup.Status, StatusComplete)
	}
	if backup.Format != "custom" || backup.Compression != 6 {
		t.Errorf("формат %q, сжатие %d: ожидались значения по умолчанию", backup.Format, backup.Compression)
	}
	// Длительность берётся из системных часов, а их разрешение зависит от
	// платформы (в Windows — миллисекунды и грубее), поэтому быстрый дамп может
	// уложиться в 0. Проверяется только отсутствие отрицательного значения.
	if backup.Duration < 0 {
		t.Errorf("длительность дампа %s не может быть отрицательной", backup.Duration)
	}
	if !backup.CreatedAt.Equal(testNow) {
		t.Errorf("время создания %s, ожидалось %s", backup.CreatedAt, testNow)
	}
	if backup.Path != filepath.Join(dir, "fse-1234", "20260921T020000Z.dump") {
		t.Errorf("путь к архиву %q", backup.Path)
	}
	written, err := os.ReadFile(backup.Path)
	if err != nil {
		t.Fatalf("чтение архива: %v", err)
	}
	if !bytes.Equal(written, payload) {
		t.Errorf("содержимое архива %q, ожидалось %q", written, payload)
	}
	if _, err := os.Stat(writer.PartialPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("после Commit остался файл %s", writer.PartialPath())
	}

	raw, err := os.ReadFile(backup.MetadataPath)
	if err != nil {
		t.Fatalf("чтение метаданных: %v", err)
	}
	parsed := metadata{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("разбор метаданных: %v", err)
	}
	if parsed.SHA256 != backup.SHA256 || parsed.SizeBytes != backup.SizeBytes {
		t.Errorf("метаданные не совпадают с бэкапом: %+v", parsed)
	}
	if parsed.Note != "перед релизом" || parsed.JobID != "job-1" || parsed.ServerVersion != "15.14" {
		t.Errorf("в метаданных потеряны параметры дампа: %+v", parsed)
	}

	// Список видит только что созданный бэкап, а список баз — его каталог.
	backups, err := store.List("fse-1234")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(backups) != 1 || backups[0].ID != backup.ID {
		t.Fatalf("List вернул %+v", backups)
	}
	databases, err := store.ListDatabases()
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if len(databases) != 1 || databases[0] != "fse-1234" {
		t.Errorf("ListDatabases вернул %v", databases)
	}
	// Повторное завершение записи не должно переписывать готовый бэкап.
	if _, err := writer.Commit(); err == nil {
		t.Error("повторный Commit не вернул ошибку")
	}
}

// TestBeginRejectsExistingBackup проверяет защиту от перезаписи: имя содержит
// метку времени с точностью до секунды, и второй дамп той же секунды обязан
// получить отказ, а не затереть готовый архив.
func TestBeginRejectsExistingBackup(t *testing.T) {
	store, _ := newTestStore(t)
	first, err := store.Begin(BeginOptions{Database: "fse-1234", Now: testNow})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := first.Write([]byte("PGDMP")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := first.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_, err = store.Begin(BeginOptions{Database: "fse-1234", Now: testNow})
	if err == nil {
		t.Fatal("повторный Begin с той же меткой времени не вернул ошибку")
	}
	if !strings.Contains(err.Error(), "уже есть") {
		t.Errorf("сообщение об ошибке %q: ожидалась подсказка про существующий бэкап", err)
	}
}

// TestBeginRejectsForeignNames проверяет, что каталог сервиса не используется для
// посторонних баз и меток: имя базы задаёт подкаталог, метка становится частью
// имени файла.
func TestBeginRejectsForeignNames(t *testing.T) {
	store, _ := newTestStore(t)
	cases := []struct {
		name string
		opts BeginOptions
	}{
		{"чужая база", BeginOptions{Database: "prod", Now: testNow}},
		{"обход каталога", BeginOptions{Database: "../../etc", Now: testNow}},
		{"служебная база", BeginOptions{Database: "postgres", Now: testNow}},
		{"метка с пробелом", BeginOptions{Database: "fse-1234", Tag: "pre restore", Now: testNow}},
		{"метка в верхнем регистре", BeginOptions{Database: "fse-1234", Tag: "PRE", Now: testNow}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := store.Begin(testCase.opts); err == nil {
				t.Error("Begin не вернул ошибку")
			}
		})
	}
}

// TestAbortRemovesPartial проверяет, что прерванный дамп не оставляет в каталоге
// ничего: файл .part убирается, и список бэкапов остаётся пустым.
func TestAbortRemovesPartial(t *testing.T) {
	store, _ := newTestStore(t)
	writer, err := store.Begin(BeginOptions{Database: "fse-1234", Now: testNow})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := writer.Write([]byte("половина дампа")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if _, err := os.Stat(writer.PartialPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("после Abort остался файл %s", writer.PartialPath())
	}
	backups, err := store.List("fse-1234")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("после Abort список бэкапов не пуст: %+v", backups)
	}
	partials, err := store.Partials("fse-1234")
	if err != nil {
		t.Fatalf("Partials: %v", err)
	}
	if len(partials) != 0 {
		t.Errorf("после Abort остались незавершённые записи: %v", partials)
	}
	if err := writer.Abort(); err != nil {
		t.Errorf("повторный Abort вернул ошибку: %v", err)
	}
}

// TestCommitKeepsArchiveWhenMetadataFails проверяет, что сбой записи метаданных
// не отменяет готовый архив: восстановление возможно и без них, а состояние
// бэкапа будет «неизвестно» — под автоматическую уборку такой файл не попадёт.
func TestCommitKeepsArchiveWhenMetadataFails(t *testing.T) {
	fs := &fakeFS{failCreate: func(path string) error {
		if strings.HasSuffix(path, MetadataExt) {
			return errors.New("нет места")
		}
		return nil
	}}
	store := newFakeStore(t, fs)
	writer, err := store.Begin(BeginOptions{Database: "fse-1234", Now: testNow})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := writer.Write([]byte("PGDMP")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	backup, err := writer.Commit()
	if err == nil {
		t.Fatal("Commit не вернул ошибку при недоступной записи метаданных")
	}
	if !strings.Contains(err.Error(), "метаданные") {
		t.Errorf("сообщение об ошибке %q: ожидалось упоминание метаданных", err)
	}
	if backup.SHA256 == "" || backup.Path != writer.Path() {
		t.Errorf("бэкап после ошибки метаданных неполон: %+v", backup)
	}
	listed, err := store.List("fse-1234")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Status != StatusUnknown {
		t.Fatalf("архив без метаданных не найден в списке: %+v", listed)
	}
	if listed[0].SizeBytes != int64(len("PGDMP")) {
		t.Errorf("размер архива без метаданных %d", listed[0].SizeBytes)
	}
}

// TestListIgnoresForeignFiles проверяет, что в список попадают только архивы
// сервиса: посторонние файлы и незавершённые записи (.part) пропускаются, а
// архив без метаданных виден с состоянием «неизвестно» и временем из имени.
func TestListIgnoresForeignFiles(t *testing.T) {
	store, dir := newTestStore(t)
	databaseDir := filepath.Join(dir, "fse-1234")
	if err := os.MkdirAll(databaseDir, dirPerm); err != nil {
		t.Fatalf("подготовка каталога: %v", err)
	}
	foreign := map[string]string{
		"notes.txt":                  "посторонний файл",
		"20260921T030000Z.dump.part": "незавершённая запись",
		"20260921T040000Z.tar":       "чужой формат",
		"20251101T050000Z.dump.part": "ещё одна незавершённая запись",
	}
	for name, content := range foreign {
		if err := os.WriteFile(filepath.Join(databaseDir, name), []byte(content), filePerm); err != nil {
			t.Fatalf("подготовка файла %s: %v", name, err)
		}
	}
	// Архив без файла метаданных: имя разбирается, параметров дампа нет.
	orphan := filepath.Join(databaseDir, "20251101T000000Z.dump")
	if err := os.WriteFile(orphan, []byte("PGDMP"), filePerm); err != nil {
		t.Fatalf("подготовка архива без метаданных: %v", err)
	}
	writer, err := store.Begin(BeginOptions{Database: "fse-1234", Now: testNow})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := writer.Write([]byte("PGDMP")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := writer.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	backups, err := store.List("fse-1234")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(backups) != 2 {
		t.Fatalf("List вернул %d бэкапов, ожидалось 2: %+v", len(backups), backups)
	}
	if backups[0].Name != "20260921T020000Z" || backups[0].Status != StatusComplete {
		t.Errorf("свежий бэкап описан неверно: %+v", backups[0])
	}
	if backups[1].Name != "20251101T000000Z" || backups[1].Status != StatusUnknown {
		t.Errorf("архив без метаданных описан неверно: %+v", backups[1])
	}
	if backups[1].SHA256 != "" || backups[1].MetadataPath != "" {
		t.Errorf("у архива без метаданных нет суммы и файла метаданных: %+v", backups[1])
	}
	if !backups[1].CreatedAt.Equal(time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("время архива без метаданных %s: ожидалось из имени файла", backups[1].CreatedAt)
	}
}

// TestFindAndLatest проверяет поиск бэкапа по идентификатору и выбор свежего:
// идентификатор приходит из CLI, поэтому проверяются и чужие базы, и обход пути.
func TestFindAndLatest(t *testing.T) {
	store, _ := newTestStore(t)
	for _, createdAt := range []time.Time{daysAgo(2), daysAgo(1), testNow} {
		writer, err := store.Begin(BeginOptions{Database: "fse-1234", Now: createdAt})
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if _, err := writer.Write([]byte("PGDMP")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if _, err := writer.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}

	found, err := store.Find("fse-1234/20260919T020000Z")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found.ID != "fse-1234/20260919T020000Z" || found.Status != StatusComplete {
		t.Errorf("Find вернул %+v", found)
	}

	latest, err := store.Latest("fse-1234")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.ID != "fse-1234/20260921T020000Z" {
		t.Errorf("Latest вернул %q, ожидался свежий бэкап", latest.ID)
	}

	cases := []struct {
		name string
		id   string
	}{
		{"чужая база", "prod/20260921T020000Z"},
		{"обход каталога", "fse-1234/../../etc/passwd"},
		{"служебная база", "postgres/20260921T020000Z"},
		{"без базы", "20260921T020000Z"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := store.Find(testCase.id); err == nil {
				t.Error("Find не вернул ошибку")
			}
		})
	}

	_, err = store.Find("fse-1234/20260101T000000Z")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("отсутствующий бэкап дал ошибку %v, ожидалась ErrNotFound", err)
	}
	if _, err := store.Latest("fssd-7"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Latest для базы без бэкапов дал ошибку %v, ожидалась ErrNotFound", err)
	}
}

// TestListDatabases проверяет список баз хранилища: учитываются только каталоги
// с именами обслуживаемых баз, посторонние подкаталоги пропускаются.
func TestListDatabases(t *testing.T) {
	store, dir := newTestStore(t)
	for _, name := range []string{"fssd-7", "fse-1234", "prod", "tmp-x", "readme.txt"} {
		path := filepath.Join(dir, name)
		if strings.HasSuffix(name, ".txt") {
			if err := os.WriteFile(path, []byte("не каталог"), filePerm); err != nil {
				t.Fatalf("подготовка файла: %v", err)
			}
			continue
		}
		if err := os.MkdirAll(path, dirPerm); err != nil {
			t.Fatalf("подготовка каталога: %v", err)
		}
	}
	databases, err := store.ListDatabases()
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	want := []string{"fse-1234", "fssd-7"}
	if len(databases) != len(want) {
		t.Fatalf("ListDatabases вернул %v, ожидалось %v", databases, want)
	}
	for index, name := range want {
		if databases[index] != name {
			t.Errorf("ListDatabases[%d] = %q, ожидалось %q", index, databases[index], name)
		}
	}
}

// TestEnsureSpace проверяет отказ по свободному месту: дамп не начинается, если
// остаток меньше порога storage.min_free_space с учётом будущего файла.
func TestEnsureSpace(t *testing.T) {
	gigabyte := int64(1000 * 1000 * 1000)
	cases := []struct {
		name    string
		free    int64
		need    int64
		minFree bytesize.Size
		wantErr bool
	}{
		{"места хватает", 10 * gigabyte, 0, bytesize.Size(5 * gigabyte), false},
		{"в остатке ровно порог", 5 * gigabyte, 0, bytesize.Size(5 * gigabyte), false},
		{"остаток меньше порога", 4 * gigabyte, 0, bytesize.Size(5 * gigabyte), true},
		{"порог учтён вместе с файлом", 5 * gigabyte, gigabyte, bytesize.Size(5 * gigabyte), true},
		{"файл помещается поверх порога", 6 * gigabyte, gigabyte, bytesize.Size(5 * gigabyte), false},
		{"порог не задан", 1, 0, 0, false},
		{"нулевой порог учитывает файл", 1, 2, 0, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := newFakeStore(t, &fakeFS{free: testCase.free, total: 100 * gigabyte})
			err := store.EnsureSpace(testCase.minFree, testCase.need)
			if !testCase.wantErr {
				if err != nil {
					t.Fatalf("EnsureSpace: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("EnsureSpace не вернул ошибку при нехватке места")
			}
			if !errors.Is(err, ErrInsufficientSpace) {
				t.Errorf("ошибка %v не распознаётся как ErrInsufficientSpace", err)
			}
			insufficient := &InsufficientSpaceError{}
			if !errors.As(err, &insufficient) {
				t.Fatalf("ошибка %v не содержит подробностей", err)
			}
			want := int64(testCase.minFree) + testCase.need
			if insufficient.Free != testCase.free || insufficient.Need != want {
				t.Errorf("подробности отказа: %+v, ожидалось free=%d need=%d",
					insufficient, testCase.free, want)
			}
		})
	}
}

// TestEnsureSpaceSkipsUnsupportedPlatform проверяет, что на платформе, где место
// на разделе посчитать нельзя, бэкап не блокируется: иначе разработка и тесты на
// Windows не смогли бы пройти путь записи.
func TestEnsureSpaceSkipsUnsupportedPlatform(t *testing.T) {
	store := newFakeStore(t, &fakeFS{freeErr: ErrUnsupported})
	if err := store.EnsureSpace(bytesize.Size(5*1000*1000*1000), 0); err != nil {
		t.Fatalf("EnsureSpace: %v", err)
	}
}

// TestEnsureSpaceReportsFilesystemError проверяет, что прочие сбои определения
// места не проглатываются: администратор должен увидеть причину отказа.
func TestEnsureSpaceReportsFilesystemError(t *testing.T) {
	store := newFakeStore(t, &fakeFS{freeErr: errors.New("нет доступа к каталогу")})
	err := store.EnsureSpace(1, 0)
	if err == nil {
		t.Fatal("EnsureSpace не вернул ошибку файловой системы")
	}
	if errors.Is(err, ErrInsufficientSpace) {
		t.Errorf("ошибка файловой системы выдана за нехватку места: %v", err)
	}
}

// TestSweepPartials проверяет уборку незавершённых записей: старый .part
// удаляется, свежий остаётся — иначе уборка отобрала бы файл у работающего дампа.
func TestSweepPartials(t *testing.T) {
	store, dir := newTestStore(t)
	databaseDir := filepath.Join(dir, "fse-1234")
	if err := os.MkdirAll(databaseDir, dirPerm); err != nil {
		t.Fatalf("подготовка каталога: %v", err)
	}
	oldPartial := filepath.Join(databaseDir, "20260919T020000Z.dump"+PartialExt)
	freshPartial := filepath.Join(databaseDir, "20260921T020000Z.dump"+PartialExt)
	for _, path := range []string{oldPartial, freshPartial} {
		if err := os.WriteFile(path, []byte("часть дампа"), filePerm); err != nil {
			t.Fatalf("подготовка файла %s: %v", path, err)
		}
	}
	stale := testNow.Add(-2 * time.Hour)
	if err := os.Chtimes(oldPartial, stale, stale); err != nil {
		t.Fatalf("установка времени файла: %v", err)
	}
	removed, err := store.SweepPartials("fse-1234", time.Hour)
	if err != nil {
		t.Fatalf("SweepPartials: %v", err)
	}
	if len(removed) != 1 || removed[0] != oldPartial {
		t.Errorf("SweepPartials убрал %v, ожидался только %s", removed, oldPartial)
	}
	if _, err := os.Stat(freshPartial); err != nil {
		t.Errorf("свежая незавершённая запись удалена: %v", err)
	}
	partials, err := store.Partials("fse-1234")
	if err != nil {
		t.Fatalf("Partials: %v", err)
	}
	if len(partials) != 1 || partials[0] != freshPartial {
		t.Errorf("Partials вернул %v", partials)
	}
}
