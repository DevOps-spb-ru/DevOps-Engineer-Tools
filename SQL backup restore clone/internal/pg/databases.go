package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Locale — параметры базы, нужные при её создании: кодировка, локаль сравнения
// и локаль классификации, владелец.
type Locale struct {
	Encoding string
	Collate  string
	Ctype    string
	Owner    string
}

// DatabaseExists отвечает, есть ли база в кластере: от этого зависит, создавать
// её при восстановлении или очищать существующую.
func (c *Client) DatabaseExists(ctx context.Context, database, pattern string) (bool, error) {
	if err := ValidateDBName(database, pattern); err != nil {
		return false, err
	}
	rows, err := c.Query(ctx, sqlDatabaseExists(database))
	if err != nil {
		return false, fmt.Errorf("не удалось проверить наличие базы %s: %w", database, err)
	}
	return len(rows) > 0 && strings.TrimSpace(rows[0].Field(0)) != "", nil
}

// DatabaseLocale возвращает параметры существующей базы: при восстановлении в
// отсутствующую базу её создают с той же локалью, иначе объекты, зависящие от
// сортировки, встают иначе, чем в источнике.
func (c *Client) DatabaseLocale(ctx context.Context, database, pattern string) (Locale, error) {
	if err := ValidateDBName(database, pattern); err != nil {
		return Locale{}, err
	}
	rows, err := c.Query(ctx, sqlDatabaseLocale(database))
	if err != nil {
		return Locale{}, fmt.Errorf("не удалось получить параметры базы %s: %w", database, err)
	}
	if len(rows) == 0 {
		return Locale{}, fmt.Errorf("база %s не найдена в каталоге", database)
	}
	row := rows[0]
	return Locale{
		Encoding: row.Field(0),
		Collate:  row.Field(1),
		Ctype:    row.Field(2),
		Owner:    row.Field(3),
	}, nil
}

// DatabaseSize возвращает размер базы в байтах: он попадает в метаданные бэкапа,
// поэтому размер дампа с ним сравнивают.
func (c *Client) DatabaseSize(ctx context.Context, database, pattern string) (int64, error) {
	if err := ValidateDBName(database, pattern); err != nil {
		return 0, err
	}
	rows, err := c.Query(ctx, sqlDatabaseSize(database))
	if err != nil {
		return 0, fmt.Errorf("не удалось определить размер базы %s: %w", database, err)
	}
	if len(rows) == 0 {
		return 0, fmt.Errorf("база %s не найдена в каталоге", database)
	}
	return parseInt64(rows[0].Field(0)), nil
}

// Sessions возвращает число активных подключений к базе.
func (c *Client) Sessions(ctx context.Context, database, pattern string) (int, error) {
	if err := ValidateDBName(database, pattern); err != nil {
		return 0, err
	}
	rows, err := c.Query(ctx, sqlDatabaseSessions(database))
	if err != nil {
		return 0, fmt.Errorf("не удалось получить список подключений к базе %s: %w", database, err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return int(parseInt64(rows[0].Field(0))), nil
}

// TerminateBackends завершает активные подключения к базе и возвращает их число.
// Вызов нужен перед восстановлением: pg_restore не удалит объекты, которые
// кто-то держит.
func (c *Client) TerminateBackends(ctx context.Context, database, pattern string) (int, error) {
	if err := ValidateDBName(database, pattern); err != nil {
		return 0, err
	}
	rows, err := c.Query(ctx, sqlTerminateBackends(database))
	if err != nil {
		return 0, fmt.Errorf("не удалось завершить подключения к базе %s: %w", database, err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return int(parseInt64(rows[0].Field(0))), nil
}

// CreateDatabase создаёт базу с заданными параметрами.
func (c *Client) CreateDatabase(ctx context.Context, opts CreateDatabaseOptions) error {
	query, err := sqlCreateDatabase(opts)
	if err != nil {
		return err
	}
	if _, err := c.Query(ctx, query); err != nil {
		return fmt.Errorf("не удалось создать базу %s: %w", opts.Database, err)
	}
	return nil
}

// DropDatabase удаляет базу. Активные подключения должны быть завершены заранее:
// сервис не удаляет базу «через силу», чтобы не потерять чужую работу молча.
func (c *Client) DropDatabase(ctx context.Context, database, pattern string) error {
	query, err := sqlDropDatabase(database, pattern)
	if err != nil {
		return err
	}
	if _, err := c.Query(ctx, query); err != nil {
		return fmt.Errorf("не удалось удалить базу %s: %w", database, err)
	}
	return nil
}

// AlterDatabaseOwner меняет владельца базы: после восстановления база должна
// принадлежать владельцу из конфига, а не роли, под которой работал сервис.
func (c *Client) AlterDatabaseOwner(ctx context.Context, database, owner, pattern string) error {
	query, err := sqlAlterDatabaseOwner(database, owner, pattern)
	if err != nil {
		return err
	}
	if _, err := c.Query(ctx, query); err != nil {
		return fmt.Errorf("не удалось сменить владельца базы %s на %s: %w", database, owner, err)
	}
	return nil
}

// ErrDatabaseMissing означает, что базы нет в кластере: проверка нужна и при
// восстановлении, и при клонировании.
var ErrDatabaseMissing = errors.New("база отсутствует в кластере")

// RequireOwnedDatabase проверяет, что база существует и принадлежит роли-владельцу
// из конфига: восстановление в чужую базу — это потеря чужих объектов.
func (c *Client) RequireOwnedDatabase(ctx context.Context, database, owner, pattern string) error {
	exists, err := c.DatabaseExists(ctx, database, pattern)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrDatabaseMissing, database)
	}
	if owner == "" {
		return nil
	}
	locale, err := c.DatabaseLocale(ctx, database, pattern)
	if err != nil {
		return err
	}
	if locale.Owner != "" && locale.Owner != owner {
		return fmt.Errorf("база %s принадлежит роли %s, а сервис обслуживает базы роли %s",
			database, locale.Owner, owner)
	}
	return nil
}
