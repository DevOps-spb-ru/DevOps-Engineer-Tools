package pg

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FieldSeparator — разделитель полей psql. Unit Separator (0x1f) выбран потому,
// что не встречается в именах объектов и коллациях, в отличие от «|» и табуляции.
const FieldSeparator = "\x1f"

// versionNumberPattern разбирает версию из начала строки, пропуская префикс.
var versionNumberPattern = regexp.MustCompile(`^[^0-9]*(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

// Row — строка результата psql в режиме --tuples-only --no-align.
type Row []string

// Field возвращает поле по индексу: если колонки нет, возвращается пустая строка,
// чтобы разбор не зависел от числа колонок в конкретном запросе.
func (r Row) Field(index int) string {
	if index < 0 || index >= len(r) {
		return ""
	}
	return r[index]
}

// Version — версия PostgreSQL.
type Version struct {
	Major int
	Minor int
	Patch int
	// Raw — исходная строка версии, например "15.19 (Debian 15.19-0+deb12u1)".
	Raw string
}

// String возвращает версию в виде "15.19".
func (v Version) String() string {
	text := strconv.Itoa(v.Major)
	if v.Minor > 0 || v.Patch > 0 {
		text += "." + strconv.Itoa(v.Minor)
	}
	if v.Patch > 0 {
		text += "." + strconv.Itoa(v.Patch)
	}
	return text
}

// AtLeastMajor отвечает, не старее ли версия указанной старшей версии.
func (v Version) AtLeastMajor(major int) bool { return v.Major >= major }

// ParsePgVersion разбирает вывод «pg_dump --version» или «SHOW server_version»:
// pg_dump (PostgreSQL) 15.19 (Debian 15.19-0+deb12u1)
func ParsePgVersion(out []byte) (Version, error) {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return Version{}, errors.New("пустой вывод версии PostgreSQL")
	}
	version := Version{Raw: text}
	matches := versionNumberPattern.FindStringSubmatch(versionAfterTag(text))
	if matches == nil {
		return Version{}, fmt.Errorf("не удалось разобрать версию PostgreSQL из %q", text)
	}
	version.Major = atoi(matches[1])
	version.Minor = atoi(matches[2])
	version.Patch = atoi(matches[3])
	if version.Major == 0 {
		return Version{}, fmt.Errorf("некорректная версия PostgreSQL в %q", text)
	}
	return version, nil
}

// versionAfterTag отрезает всё до слова PostgreSQL: в строке версии клиента
// встречаются числа от дистрибутива ("Debian 15.19-0+deb12u1"), а первое число
// после имени продукта — нужная версия.
func versionAfterTag(text string) string {
	const marker = "PostgreSQL"
	if index := strings.Index(text, marker); index >= 0 {
		return text[index+len(marker):]
	}
	return text
}

// atoi переводит строку в число, возвращая 0 для пустой строки и мусора:
// разбор идёт после проверки шаблоном, поэтому ошибки здесь невозможны.
func atoi(text string) int {
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0
	}
	return value
}

// ParseTuples разбирает вывод psql с разделителем FieldSeparator.
func ParseTuples(out []byte) ([]Row, error) {
	return ParseTuplesSeparated(out, FieldSeparator)
}

// ParseTuplesSeparated разбирает вывод psql с указанным разделителем полей.
func ParseTuplesSeparated(out []byte, separator string) ([]Row, error) {
	if separator == "" {
		return nil, errors.New("не задан разделитель полей")
	}
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	text = strings.TrimSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	rows := make([]Row, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, strings.Split(line, separator))
	}
	return rows, nil
}

// TOCEntry — объект из оглавления архива pg_restore --list.
type TOCEntry struct {
	// Type — тип объекта: TABLE, SEQUENCE, INDEX, CONSTRAINT и т. д.
	Type string
	// Schema — схема; для объектов вне схемы пусто.
	Schema string
	// Name — имя объекта.
	Name string
	// Owner — владелец объекта, записанный в дампе.
	Owner string
}

// String возвращает описание объекта для журнала и метаданных.
func (e TOCEntry) String() string {
	name := e.Name
	if e.Schema != "" {
		name = e.Schema + "." + e.Name
	}
	if e.Owner != "" {
		return fmt.Sprintf("%s %s (владелец %s)", e.Type, name, e.Owner)
	}
	return fmt.Sprintf("%s %s", e.Type, name)
}

// ParseRestoreList разбирает вывод «pg_restore --list»:
//
//	; Archive created at 2026-09-21 02:00:01 UTC
//	251; 1259 16409 TABLE public alembic_version
//
// Строки комментариев начинаются с «;» и пропускаются.
func ParseRestoreList(out []byte) ([]TOCEntry, error) {
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	entries := make([]TOCEntry, 0, 64)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		entry, ok := parseTOCLine(line)
		if !ok {
			continue
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 && strings.TrimSpace(text) != "" {
		return nil, errors.New("в выводе pg_restore --list не найдено ни одного объекта")
	}
	return entries, nil
}

// parseTOCLine разбирает одну строку оглавления. Строка имеет вид
//
//	<dumpId>; <tableoid> <oid> <описание> <схема> <имя> <владелец>
//
// и не содержит разделителей между колонками, а описание объекта может состоять
// из нескольких слов («SEQUENCE SET»), поэтому граница восстанавливается по
// списку известных описаний. Схема и владелец у объектов вне схемы печатаются
// как «-» и превращаются в пустые значения.
func parseTOCLine(line string) (TOCEntry, bool) {
	parts := strings.SplitN(line, ";", 2)
	if len(parts) != 2 {
		return TOCEntry{}, false
	}
	if _, err := strconv.Atoi(strings.TrimSpace(parts[0])); err != nil {
		return TOCEntry{}, false
	}
	fields := strings.Fields(parts[1])
	if len(fields) < 3 {
		return TOCEntry{}, false
	}
	descriptor, rest := splitDescriptor(fields[2:])
	if descriptor == "" || len(rest) == 0 {
		return TOCEntry{}, false
	}
	entry := TOCEntry{Type: descriptor}
	switch {
	case len(rest) >= 3:
		entry.Schema = rest[0]
		entry.Owner = rest[len(rest)-1]
		entry.Name = strings.Join(rest[1:len(rest)-1], " ")
	case len(rest) == 2:
		// Схема и имя без владельца (в списке он не печатался).
		entry.Schema = rest[0]
		entry.Name = rest[1]
	default:
		entry.Name = rest[0]
	}
	entry.Schema = normalizeDash(entry.Schema)
	entry.Owner = normalizeDash(entry.Owner)
	if entry.Name == "" {
		return TOCEntry{}, false
	}
	return entry, true
}

// tocDescriptors — описания объектов, которые печатает pg_restore --list.
// Список упорядочен по убыванию числа слов: разбор ищет самое длинное совпадение,
// иначе «SEQUENCE SET» распался бы на «SEQUENCE» и имя.
var tocDescriptors = []string{
	// Три слова.
	"TEXT SEARCH CONFIGURATION",
	"TEXT SEARCH DICTIONARY",
	"TEXT SEARCH TEMPLATE",
	"TEXT SEARCH PARSER",
	"SEQUENCE OWNED BY",
	"MATERIALIZED VIEW DATA",
	"FOREIGN TABLE DATA",
	// Два слова.
	"DEFAULT ACL",
	"SECURITY LABEL",
	"EVENT TRIGGER",
	"TABLE ATTACH",
	"TABLE DATA",
	"INDEX ATTACH",
	"SEQUENCE SET",
	"MATERIALIZED VIEW",
	"FOREIGN TABLE",
	"OPERATOR CLASS",
	"OPERATOR FAMILY",
	"DOMAIN CONSTRAINT",
	"CHECK CONSTRAINT",
	"FK CONSTRAINT",
	"ROW SECURITY",
	"PROCEDURAL LANGUAGE",
	"LARGE OBJECT",
	"BLOB METADATA",
	"BLOB COMMENTS",
	"ACCESS METHOD",
	"USER MAPPING",
	"PUBLICATION TABLE",
	"SUBSCRIPTION TABLE",
	// Одно слово.
	"ACL",
	"AGGREGATE",
	"CAST",
	"COLLATION",
	"COMMENT",
	"CONSTRAINT",
	"CONVERSION",
	"DATABASE",
	"DEFAULT",
	"DOMAIN",
	"EXTENSION",
	"FUNCTION",
	"INDEX",
	"POLICY",
	"PROCEDURE",
	"PUBLICATION",
	"RULE",
	"SCHEMA",
	"SEQUENCE",
	"SERVER",
	"STATISTICS",
	"SUBSCRIPTION",
	"TABLE",
	"TABLESPACE",
	"TRANSFORM",
	"TRIGGER",
	"TYPE",
	"VIEW",
}

// splitDescriptor отделяет описание объекта от остальных полей строки.
func splitDescriptor(rest []string) (string, []string) {
	for _, descriptor := range tocDescriptors {
		words := strings.Fields(descriptor)
		if len(words) > len(rest) {
			continue
		}
		if strings.Join(rest[:len(words)], " ") == descriptor {
			return descriptor, rest[len(words):]
		}
	}
	return rest[0], rest[1:]
}

// normalizeDash превращает заглушку pg_restore «-» в пустое значение.
func normalizeDash(value string) string {
	if value == "-" {
		return ""
	}
	return value
}

// SummarizeTOC возвращает сводку по типам объектов архива: «TABLE 12», «INDEX 30».
// Сводка попадает в метаданные бэкапа, чтобы содержимое архива было видно
// без запуска psql и pg_restore.
func SummarizeTOC(entries []TOCEntry) []string {
	counts := make(map[string]int, len(entries))
	for _, entry := range entries {
		counts[entry.Type]++
	}
	summary := make([]string, 0, len(counts))
	for kind, count := range counts {
		summary = append(summary, fmt.Sprintf("%s %d", kind, count))
	}
	sort.Strings(summary)
	return summary
}
