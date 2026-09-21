package pg

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// DefaultAllowPattern — шаблон имён БД, которыми управляет сервис: стенды вида
// <префикс>-<номер>, например fse-1234, fssd-7, dops-fix-42. Префикс отделяет
// стенды от остальных баз кластера, а номер в конце обязателен, поэтому
// служебные базы (postgres, template0, template1) и базы других приложений
// (prod, app) не попадают ни в бэкапы, ни в восстановление. Другое именование
// стендов задаётся шаблоном databases.pattern в конфиге.
const DefaultAllowPattern = `^[a-z][a-z0-9]*(?:-[a-z0-9]+)*-[0-9]+$`

// MaxIdentifierLength — предел длины идентификатора PostgreSQL (NAMEDATALEN-1).
const MaxIdentifierLength = 63

// DefaultTagPattern — шаблон метки бэкапа (комментарий пользователя).
const DefaultTagPattern = `^[a-z0-9][a-z0-9._-]{0,31}$`

// ReservedDatabases — служебные БД, которые сервис не трогает никогда.
var ReservedDatabases = []string{"postgres", "template0", "template1"}

// backupIDPattern — допустимые символы второй части идентификатора бэкапа:
// метка времени (20260921T020000Z) или её суффикс (-pre-restore, -before-release).
var backupIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// tagPattern — скомпилированный шаблон метки бэкапа.
var tagPattern = regexp.MustCompile(DefaultTagPattern)

// IsReservedDatabase отвечает, входит ли имя в список служебных БД.
func IsReservedDatabase(name string) bool {
	for _, reserved := range ReservedDatabases {
		if name == reserved {
			return true
		}
	}
	return false
}

// ValidateDBName проверяет имя БД перед тем, как оно попадёт в argv PostgreSQL
// и в путь к файлу бэкапа: набор символов ограничен, длина — как у идентификатора,
// а шаблон владельца данных задаёт конфиг.
func ValidateDBName(name string, allowPattern string) error {
	pattern := strings.TrimSpace(allowPattern)
	if pattern == "" {
		pattern = DefaultAllowPattern
	}
	if name == "" {
		return errors.New("имя БД не задано")
	}
	if utf8.RuneCountInString(name) > MaxIdentifierLength {
		return fmt.Errorf("имя БД длиннее %d символов: %q", MaxIdentifierLength, name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("имя БД %q начинается с точки: такие имена не используются", name)
	}
	for _, r := range name {
		if !isSafeNameRune(r) {
			return fmt.Errorf(
				"недопустимый символ %q в имени БД %q (разрешены латиница, цифры, \"_\", \"-\" и \".\")", r, name)
		}
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("шаблон имён БД %q некорректен: %w", pattern, err)
	}
	if !compiled.MatchString(name) {
		return fmt.Errorf("имя БД %q не соответствует шаблону %s", name, pattern)
	}
	return nil
}

// ValidateTag проверяет метку бэкапа: она попадает в имя файла и в метаданные.
func ValidateTag(tag string) error {
	if strings.TrimSpace(tag) == "" {
		return errors.New("метка бэкапа не задана")
	}
	if !tagPattern.MatchString(tag) {
		return fmt.Errorf(
			"метка %q недопустима: допустимы строчные латинские буквы, цифры, \"_\", \"-\" и \".\" (до 32 символов)", tag)
	}
	return nil
}

// SplitBackupID разбирает идентификатор бэкапа вида «<бд>/<метка времени>».
// Идентификатор приходит из веб-формы, поэтому обе части проверяются отдельно:
// путь вида «../etc/passwd» до файловой системы не доходит.
func SplitBackupID(id string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(id), "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("идентификатор бэкапа %q должен иметь вид <бд>/<метка времени>", id)
	}
	db, name := parts[0], parts[1]
	if err := ValidateDBName(db, DefaultAllowPattern); err != nil {
		return "", "", err
	}
	if !backupIDPattern.MatchString(name) {
		return "", "", fmt.Errorf("недопустимое имя бэкапа %q в идентификаторе %q", name, id)
	}
	return db, name, nil
}

// ValidateBackupID проверяет идентификатор бэкапа целиком.
func ValidateBackupID(id string) error {
	_, _, err := SplitBackupID(id)
	return err
}

// isSafeNameRune разрешает латиницу, цифры и разделители, принятые в именах
// стендов. Кавычки, пробелы, точка с запятой, обратный слэш и юникод запрещены:
// они не нужны в именах вида fse-1234, но опасны в argv и путях.
func isSafeNameRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '_' || r == '-' || r == '.':
		return true
	default:
		return false
	}
}
