package backup

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/pg"
)

// TestManagedDatabases проверяет список баз для расписания (`backup --all`):
// в него попадают только имена из databases.pattern, служебные базы и
// databases.protected исключаются, а порядок стабилен.
func TestManagedDatabases(t *testing.T) {
	env := newEnv(t)
	env.pg.databases = []pg.Database{
		{Name: "fssd-7"},
		{Name: "fse-1234"},
		{Name: "postgres"},
		{Name: "template0"},
		{Name: "fse_1234"}, // подчёркивание: шаблон не подходит
		{Name: "production"},
	}
	names, err := env.service.ManagedDatabases(context.Background())
	if err != nil {
		t.Fatalf("ManagedDatabases: %v", err)
	}
	want := []string{"fse-1234", "fssd-7"}
	if !slices.Equal(names, want) {
		t.Fatalf("обслуживаемые базы %v, ожидались %v", names, want)
	}
}

// TestManagedDatabasesEmptyAndError проверяет пустой список (нет стендов) и сбой
// чтения каталога: расписание должно получить понятный результат, а не молчание.
func TestManagedDatabasesEmptyAndError(t *testing.T) {
	env := newEnv(t)
	env.pg.databases = []pg.Database{{Name: "postgres"}, {Name: "production"}}
	names, err := env.service.ManagedDatabases(context.Background())
	if err != nil {
		t.Fatalf("ManagedDatabases: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("ожидался пустой список, получено %v", names)
	}

	env.pg.listErr = errors.New("сервер PostgreSQL недоступен")
	if _, err := env.service.ManagedDatabases(context.Background()); err == nil {
		t.Fatal("ожидалась ошибка чтения каталога, получено nil")
	}
}
