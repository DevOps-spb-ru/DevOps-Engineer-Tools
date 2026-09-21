// Package passwd создаёт и проверяет хэши паролей сервиса.
//
// В конфиге и базе нет открытых паролей: хранится bcrypt-хэш, который создаёт
// команда `sqlbrc hash-password`. Правила проверки пароля и работы с хэшем живут
// в одном пакете, поэтому команда создания и вход в веб-интерфейс (`sqlbrc serve`)
// используют одну и ту же реализацию.
package passwd

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// DefaultCost — стоимость bcrypt. 10 — то же значение, что в примерах установки
// (`htpasswd -bnBC 10`): выше 12 вход в интерфейс становится заметно медленнее,
// а сервис внутренний и доступен только ответственным людям.
const DefaultCost = 10

// Минимальная и максимальная длина пароля. Верхняя граница важнее: bcrypt
// учитывает только первые 72 байта, и длинный пароль молча превращался бы в
// свой префикс — тогда «пароль из 100 символов» и его первые 72 символа были бы
// одним и тем же паролем.
const (
	MinPasswordLength = 10
	MaxPasswordBytes  = 72
)

// Hash создаёт bcrypt-хэш пароля для поля auth.users[].password_bcrypt.
func Hash(password string) (string, error) {
	if err := Validate(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), DefaultCost)
	if err != nil {
		return "", fmt.Errorf("не удалось создать хэш пароля: %w", err)
	}
	return string(hash), nil
}

// Verify сверяет пароль с хэшем. Ошибка означает, что пароль не подходит или
// хэш повреждён — оба случая для вызывающего кода равнозначны: вход запрещён.
func Verify(hash, password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return errors.New("пароль не подходит")
		}
		return fmt.Errorf("хэш пароля не разобран: %w", err)
	}
	return nil
}

// Validate проверяет пароль перед созданием хэша. Ограничения нужны не ради
// «сложности»: без них пароль из 80 символов работал бы по первым 72 символам,
// а пароль с переводом строки нельзя было бы передать в конфиг.
func Validate(password string) error {
	switch {
	case strings.TrimSpace(password) == "":
		return errors.New("пароль пуст")
	case password != strings.TrimSpace(password):
		return errors.New("пароль не должен начинаться или заканчиваться пробелом")
	case utf8.RuneCountInString(password) < MinPasswordLength:
		return fmt.Errorf("пароль короче %d символов", MinPasswordLength)
	case len(password) > MaxPasswordBytes:
		return fmt.Errorf("пароль длиннее %d байт: bcrypt учитывает только первые %d байт",
			MaxPasswordBytes, MaxPasswordBytes)
	}
	for _, r := range password {
		if unicode.IsControl(r) {
			return fmt.Errorf("пароль содержит управляющий символ %q", r)
		}
	}
	return nil
}
