package pg

import (
	"errors"
	"fmt"
	"strings"
)

// QuoteIdentifier возвращает идентификатор PostgreSQL в двойных кавычках.
// Имена обслуживаемых баз содержат дефис («fse-1234»), и без кавычек сервер
// прочитал бы его как минус. Внутренние кавычки удваиваются: имя из конфига
// не может разорвать запрос.
func QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// QuoteLiteral возвращает строковый литерал PostgreSQL. В литералах оказываются
// имя базы, локаль и владелец из конфига, поэтому апострофы удваиваются.
func QuoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// sqlDatabaseExists проверяет наличие базы в каталоге.
func sqlDatabaseExists(database string) string {
	return fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = %s", QuoteLiteral(database))
}

// sqlDatabaseLocale возвращает параметры базы, нужные для CREATE DATABASE при
// восстановлении в отсутствующую базу: кодировку, локаль сравнения и
// классификации, владельца.
func sqlDatabaseLocale(database string) string {
	return fmt.Sprintf(
		"SELECT pg_encoding_to_char(encoding), datcollate, datctype, pg_get_userbyid(datdba) "+
			"FROM pg_database WHERE datname = %s", QuoteLiteral(database))
}

// sqlDatabaseSize возвращает размер базы в байтах.
func sqlDatabaseSize(database string) string {
	return fmt.Sprintf("SELECT pg_database_size(%s)", QuoteLiteral(database))
}

// sqlDatabaseSessions считает активные подключения к базе, кроме своего:
// счётчик показывает, кого завершит восстановление, и попадает в журнал задачи.
func sqlDatabaseSessions(database string) string {
	return fmt.Sprintf(
		"SELECT count(*) FROM pg_stat_activity WHERE datname = %s AND pid <> pg_backend_pid()",
		QuoteLiteral(database))
}

// sqlTerminateBackends завершает активные подключения к базе. Без этого
// pg_restore не может удалить объекты, которые кто-то держит, и восстановление
// падает на середине.
func sqlTerminateBackends(database string) string {
	return fmt.Sprintf(
		"SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE datname = %s AND pid <> pg_backend_pid()",
		QuoteLiteral(database))
}

// CreateDatabaseOptions — параметры создания базы.
type CreateDatabaseOptions struct {
	// Database — имя создаваемой базы: проверяется по шаблону обслуживаемых баз.
	Database string
	// Pattern — шаблон имён из databases.pattern (пусто — DefaultAllowPattern).
	Pattern string
	// Template — база-образец (template0 нужен, когда задаётся своя локаль).
	Template string
	// Owner — роль-владелец создаваемой базы.
	Owner string
	// Encoding, Collate, Ctype — параметры локали. Пустые значения сервер берёт
	// из template: восстановление в базу с чужой локалью ломается на объектах,
	// зависящих от сортировки.
	Encoding string
	Collate  string
	Ctype    string
	// Tablespace — табличное пространство по умолчанию.
	Tablespace string
}

// sqlCreateDatabase собирает CREATE DATABASE. Имя базы проверяется по шаблону
// (как и в argv): каталог обслуживаемых баз задаётся конфигом, и создавать базу
// за его пределами сервис не должен. Остальные значения подставляются
// идентификаторами или литералами, поэтому запрос безопасен независимо от того,
// что вернул каталог сервера.
func sqlCreateDatabase(opts CreateDatabaseOptions) (string, error) {
	if err := ValidateDBName(opts.Database, opts.Pattern); err != nil {
		return "", err
	}
	parts := []string{"CREATE DATABASE " + QuoteIdentifier(opts.Database)}
	if opts.Template != "" {
		if err := ValidateIdentifier(opts.Template, "template"); err != nil {
			return "", err
		}
		parts = append(parts, "TEMPLATE "+QuoteIdentifier(opts.Template))
	}
	if opts.Owner != "" {
		if err := ValidateIdentifier(opts.Owner, "databases.owner"); err != nil {
			return "", err
		}
		parts = append(parts, "OWNER "+QuoteIdentifier(opts.Owner))
	}
	for _, locale := range []struct {
		field  string
		value  string
		clause string
	}{
		{"encoding", opts.Encoding, "ENCODING"},
		{"LC_COLLATE", opts.Collate, "LC_COLLATE"},
		{"LC_CTYPE", opts.Ctype, "LC_CTYPE"},
	} {
		if locale.value == "" {
			continue
		}
		if err := validateLocaleValue(locale.value, locale.field); err != nil {
			return "", err
		}
		parts = append(parts, locale.clause+" "+QuoteLiteral(locale.value))
	}
	if opts.Tablespace != "" {
		if err := ValidateIdentifier(opts.Tablespace, "tablespace"); err != nil {
			return "", err
		}
		parts = append(parts, "TABLESPACE "+QuoteIdentifier(opts.Tablespace))
	}
	return strings.Join(parts, " "), nil
}

// sqlDropDatabase собирает DROP DATABASE. База проверяется по шаблону
// обслуживаемых: защита от «удалил не то» здесь важнее удобства.
func sqlDropDatabase(database, pattern string) (string, error) {
	if err := ValidateDBName(database, pattern); err != nil {
		return "", err
	}
	return "DROP DATABASE IF EXISTS " + QuoteIdentifier(database), nil
}

// sqlAlterDatabaseOwner собирает ALTER DATABASE ... OWNER TO: после
// восстановления база принадлежит владельцу из конфига, а не роли, под которой
// работал сервис.
func sqlAlterDatabaseOwner(database, owner, pattern string) (string, error) {
	if err := ValidateDBName(database, pattern); err != nil {
		return "", err
	}
	if owner == "" {
		return "", errors.New("не задана роль-владелец базы (databases.owner)")
	}
	if err := ValidateIdentifier(owner, "databases.owner"); err != nil {
		return "", err
	}
	return fmt.Sprintf("ALTER DATABASE %s OWNER TO %s",
		QuoteIdentifier(database), QuoteIdentifier(owner)), nil
}

// validateLocaleValue проверяет значение локали и кодировки перед подстановкой
// в запрос: в каталоге PostgreSQL это свободные строки, а нулевой байт и перевод
// строки сломали бы служебный запрос psql.
func validateLocaleValue(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s: пустое значение", field)
	}
	if strings.ContainsAny(value, "\x00\n\r") {
		return fmt.Errorf("%s: недопустимый символ в значении %q", field, value)
	}
	return nil
}
