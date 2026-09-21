package auth

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Token — токен API: кому он принадлежит и его значение.
type Token struct {
	// Login — имя пользователя токена; попадает в журнал задач как оператор.
	Login string
	// Value — сам токен; в конфиге и в журнале он не печатается.
	Value string
}

// ReadTokens читает файл токенов API (auth.token_file): одна строка — один
// токен в формате «<логин>:<токен>». Пустые строки и строки, начинающиеся с
// «#», пропускаются: файл правят руками, и комментарии в нём полезны.
//
// Токены лежат отдельным файлом, а не в конфиге, потому что конфиг читают
// администраторы и он попадает в бэкап системы: файл токенов можно закрыть
// правами 0600 и не показывать в разделе диагностики.
func ReadTokens(path string) ([]Token, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть файл токенов %s: %w", path, err)
	}
	// Ошибка закрытия файла, открытого только для чтения, ничего не меняет:
	// содержимое уже прочитано, а причина сбоя — в разборе строк выше.
	defer func() { _ = file.Close() }()
	tokens, err := parseTokens(file)
	if err != nil {
		return nil, fmt.Errorf("файл токенов %s: %w", path, err)
	}
	return tokens, nil
}

// parseTokens разбирает содержимое файла токенов: отдельная функция нужна,
// чтобы проверять разбор без файловой системы.
func parseTokens(r io.Reader) ([]Token, error) {
	var (
		tokens []Token
		seen   = make(map[string]bool)
		line   int
	)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		login, value, found := strings.Cut(text, ":")
		if !found {
			return nil, fmt.Errorf("строка %d: ожидается формат «<логин>:<токен>»", line)
		}
		login = strings.TrimSpace(login)
		value = strings.TrimSpace(value)
		if login == "" {
			return nil, fmt.Errorf("строка %d: не задан логин", line)
		}
		if value == "" {
			return nil, fmt.Errorf("строка %d: не задан токен", line)
		}
		if seen[value] {
			return nil, fmt.Errorf("строка %d: токен повторяется", line)
		}
		seen[value] = true
		tokens = append(tokens, Token{Login: login, Value: value})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("не удалось прочитать файл токенов: %w", err)
	}
	return tokens, nil
}
