package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// backupsAt собирает бэкапы, снятые указанное число дней назад: Plan работает
// только со временем создания, поэтому остальные поля не важны.
func backupsAt(days ...int) []Backup {
	backups := make([]Backup, 0, len(days))
	for _, age := range days {
		created := daysAgo(age)
		backups = append(backups, Backup{
			ID:        "fse-1234/" + created.UTC().Format(TimestampLayout),
			DB:        "fse-1234",
			Name:      created.UTC().Format(TimestampLayout),
			CreatedAt: created,
			Status:    StatusComplete,
		})
	}
	return backups
}

// daysOf печатает возрасты бэкапов в днях: в таблице так видно, что именно
// осталось и что удаляется, без длинных меток времени.
func daysOf(backups []Backup) string {
	ages := make([]string, 0, len(backups))
	for _, backup := range backups {
		if backup.CreatedAt.IsZero() {
			ages = append(ages, "?")
			continue
		}
		ages = append(ages, fmt.Sprint(int(testNow.Sub(backup.CreatedAt).Hours()/24)))
	}
	return strings.Join(ages, ",")
}

// TestPlan проверяет политику хранения на таблице случаев: обе границы из
// конфига действуют вместе («последние 7 бэкапов, но не старше 14 дней»),
// а бэкап с неразобранным временем не удаляется никогда.
func TestPlan(t *testing.T) {
	unknown := Backup{ID: "fse-1234/мусор", DB: "fse-1234", Name: "мусор"}
	cases := []struct {
		name     string
		keepLast int
		keepDays int
		input    []Backup
		wantKeep string
		wantDrop string
	}{
		{
			name:     "политика не задана",
			input:    backupsAt(0, 1),
			wantKeep: "0,1",
		},
		{
			name:     "последние два без ограничения по возрасту",
			keepLast: 2,
			input:    backupsAt(2, 0, 1),
			wantKeep: "0,1",
			wantDrop: "2",
		},
		{
			name:     "возраст без ограничения по числу",
			keepDays: 2,
			input:    backupsAt(0, 10, 1, 5),
			wantKeep: "0,1",
			wantDrop: "5,10",
		},
		{
			name:     "число бэкапов важнее возраста",
			keepLast: 3,
			keepDays: 14,
			input:    backupsAt(0, 1, 2, 15, 20),
			wantKeep: "0,1,2",
			wantDrop: "15,20",
		},
		{
			name:     "возраст важнее числа бэкапов",
			keepLast: 10,
			keepDays: 14,
			input:    backupsAt(0, 20),
			wantKeep: "0",
			wantDrop: "20",
		},
		{
			name:     "неизвестное время не удаляется",
			keepLast: 1,
			keepDays: 1,
			input:    append(backupsAt(30), unknown),
			wantKeep: "?",
			wantDrop: "30",
		},
		{
			name:     "бэкапов меньше порога",
			keepLast: 7,
			keepDays: 14,
			input:    backupsAt(0, 3),
			wantKeep: "0,3",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			keep, drop := Plan(testCase.input, RetainOptions{
				KeepLast: testCase.keepLast,
				KeepDays: testCase.keepDays,
				Now:      testNow,
			})
			if got := daysOf(keep); got != testCase.wantKeep {
				t.Errorf("остаются бэкапы возрастом %q, ожидалось %q", got, testCase.wantKeep)
			}
			if got := daysOf(drop); got != testCase.wantDrop {
				t.Errorf("удаляются бэкапы возрастом %q, ожидалось %q", got, testCase.wantDrop)
			}
			if len(keep)+len(drop) != len(testCase.input) {
				t.Errorf("разбор потерял бэкапы: остаётся %d, удаляется %d, всего %d",
					len(keep), len(drop), len(testCase.input))
			}
		})
	}
}

// TestPlanKeepsInputOrder проверяет, что Plan не меняет порядок переданного
// списка: он используется и для вывода, и для дальнейшей уборки.
func TestPlanKeepsInputOrder(t *testing.T) {
	input := backupsAt(1, 0, 2)
	keep, _ := Plan(input, RetainOptions{KeepLast: 1, Now: testNow})
	if got := daysOf(input); got != "1,0,2" {
		t.Errorf("входной список переставлен: %q", got)
	}
	if got := daysOf(keep); got != "0" {
		t.Errorf("остаётся %q, ожидался свежий бэкап", got)
	}
}

// createBackup снимает бэкап обслуживаемой базы в тестовом хранилище.
func createBackup(t *testing.T, store *Store, createdAt time.Time) Backup {
	t.Helper()
	writer, err := store.Begin(BeginOptions{Database: "fse-1234", Now: createdAt})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := writer.Write([]byte("PGDMP")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	backup, err := writer.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return backup
}

// TestApplyRetentionRemovesFiles проверяет, что политика хранения удаляет и архив,
// и файл метаданных: иначе в каталоге остались бы «сироты» без данных.
func TestApplyRetentionRemovesFiles(t *testing.T) {
	store, _ := newTestStore(t)
	old := createBackup(t, store, daysAgo(20))
	mid := createBackup(t, store, daysAgo(2))
	fresh := createBackup(t, store, testNow)

	removed, err := store.ApplyRetention("fse-1234", RetainOptions{
		KeepLast: 2, KeepDays: 14, Now: testNow,
	})
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if len(removed) != 1 || removed[0].ID != old.ID {
		t.Fatalf("удалено %+v, ожидался только старый бэкап %s", removed, old.ID)
	}
	for _, path := range old.Files() {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("файл %s не удалён", path)
		}
	}
	kept := append(mid.Files(), fresh.Files()...)
	for _, path := range kept {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("файл %s удалён по ошибке: %v", path, err)
		}
	}
	backups, err := store.List("fse-1234")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(backups) != 2 {
		t.Errorf("после уборки в каталоге %d бэкапов, ожидалось 2", len(backups))
	}
}

// TestApplyRetentionDisabled проверяет, что без политики хранения уборка ничего
// не удаляет: пустые keep_last/keep_days не должны означать «удалить всё».
func TestApplyRetentionDisabled(t *testing.T) {
	store, _ := newTestStore(t)
	backup := createBackup(t, store, daysAgo(100))
	removed, err := store.ApplyRetention("fse-1234", RetainOptions{Now: testNow})
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("без политики удалено %+v", removed)
	}
	if _, err := os.Stat(backup.Path); err != nil {
		t.Errorf("бэкап удалён без политики: %v", err)
	}
}

// TestApplyRetentionErrorKeepsBackup проверяет, что сбой удаления не выдаётся за
// успешную уборку: бэкап попадает в список удалённых только после реального
// удаления файлов, а причина попадает в сообщение об ошибке.
func TestApplyRetentionErrorKeepsBackup(t *testing.T) {
	fs := &fakeFS{removeErr: errors.New("файл занят")}
	store := newFakeStore(t, fs)
	createBackup(t, store, daysAgo(20))
	createBackup(t, store, testNow)

	removed, err := store.ApplyRetention("fse-1234", RetainOptions{
		KeepLast: 1, KeepDays: 14, Now: testNow,
	})
	if err == nil {
		t.Fatal("ApplyRetention не сообщил о сбое удаления")
	}
	if len(removed) != 0 {
		t.Errorf("в удалённые записан бэкап, файлы которого не удалены: %+v", removed)
	}
	if !strings.Contains(err.Error(), filepath.Join("fse-1234")) {
		t.Errorf("в ошибке нет пути к файлу: %v", err)
	}
}
