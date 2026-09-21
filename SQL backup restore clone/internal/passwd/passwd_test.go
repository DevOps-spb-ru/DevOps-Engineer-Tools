package passwd

import (
	"strings"
	"testing"
)

// TestHashAndVerify проверяет основной путь: созданный хэш принимает исходный
// пароль и не принимает другой; одинаковые пароли дают разные хэши (соль).
func TestHashAndVerify(t *testing.T) {
	const password = "пароль-стенда-42"
	hash, err := Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$2") || len(hash) != 60 {
		t.Fatalf("хэш %q не похож на bcrypt-хэш из конфига", hash)
	}
	if err := Verify(hash, password); err != nil {
		t.Errorf("Verify с верным паролем: %v", err)
	}
	if err := Verify(hash, password+"!"); err == nil {
		t.Error("Verify принял неверный пароль")
	}
	other, err := Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if other == hash {
		t.Error("одинаковые пароли дали одинаковый хэш: соль не используется")
	}
}

// TestVerifyRejectsBrokenHash проверяет, что повреждённый хэш из конфига не
// выглядит как «пароль не подходит»: администратор должен видеть причину.
func TestVerifyRejectsBrokenHash(t *testing.T) {
	err := Verify("не-хэш", "пароль-стенда-42")
	if err == nil {
		t.Fatal("Verify принял повреждённый хэш")
	}
	if !strings.Contains(err.Error(), "не разобран") {
		t.Errorf("сообщение об ошибке %q", err)
	}
}

// TestValidate проверяет ограничения пароля: пустой, короткий, чрезмерно длинный
// (bcrypt обрезал бы его) и с управляющими символами отвергаются.
func TestValidate(t *testing.T) {
	valid := strings.Repeat("a", MinPasswordLength)
	if err := Validate(valid); err != nil {
		t.Errorf("пароль длиной %d символов отвергнут: %v", MinPasswordLength, err)
	}
	long := strings.Repeat("b", MaxPasswordBytes+1)
	cases := []struct {
		name     string
		password string
	}{
		{"пустой", ""},
		{"из пробелов", "          "},
		{"пробелы по краям", " пароль-стенда-42 "},
		{"короткий", "12345"},
		{"слишком длинный", long},
		{"с переводом строки", "пароль-стенда-42\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := Validate(testCase.password); err == nil {
				t.Error("пароль прошёл проверку")
			}
			if _, err := Hash(testCase.password); err == nil {
				t.Error("Hash принял неподходящий пароль")
			}
		})
	}
}
