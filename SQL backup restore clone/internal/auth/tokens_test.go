package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTokens(t *testing.T) {
	long := strings.Repeat("a", MinTokenLength)
	cases := []struct {
		name    string
		content string
		want    []Token
		substr  string
	}{
		{
			name:    "токен с комментариями и пустыми строками",
			content: "# токены API\n\nci:" + long + "\n",
			want:    []Token{{Login: "ci", Value: long}},
		},
		{
			name:    "несколько токенов",
			content: "ci:" + long + "\nivan:" + strings.Repeat("b", MinTokenLength) + "\n",
			want: []Token{
				{Login: "ci", Value: long},
				{Login: "ivan", Value: strings.Repeat("b", MinTokenLength)},
			},
		},
		{
			name:    "строка без разделителя",
			content: long,
			substr:  "ожидается формат",
		},
		{
			name:    "пустой логин",
			content: ":" + long,
			substr:  "не задан логин",
		},
		{
			name:    "пустой токен",
			content: "ci:",
			substr:  "не задан токен",
		},
		{
			name:    "токен повторяется",
			content: "ci:" + long + "\nivan:" + long + "\n",
			substr:  "повторяется",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tokens, err := parseTokens(strings.NewReader(testCase.content))
			if testCase.substr != "" {
				if err == nil {
					t.Fatalf("ожидалась ошибка с текстом %q", testCase.substr)
				}
				if !strings.Contains(err.Error(), testCase.substr) {
					t.Fatalf("ошибка %q не содержит %q", err, testCase.substr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTokens: %v", err)
			}
			if len(tokens) != len(testCase.want) {
				t.Fatalf("разобрано %d токенов, ожидалось %d", len(tokens), len(testCase.want))
			}
			for index, token := range tokens {
				if token != testCase.want[index] {
					t.Errorf("токен %d = %+v, ожидался %+v", index, token, testCase.want[index])
				}
			}
		})
	}
}

func TestReadTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens")
	long := strings.Repeat("c", MinTokenLength)
	if err := os.WriteFile(path, []byte("ci:"+long+"\n"), 0o600); err != nil {
		t.Fatalf("подготовка файла токенов: %v", err)
	}
	tokens, err := ReadTokens(path)
	if err != nil {
		t.Fatalf("ReadTokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].Login != "ci" || tokens[0].Value != long {
		t.Fatalf("прочитаны токены %+v", tokens)
	}
}

func TestReadTokensMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "нет-такого-файла")
	if _, err := ReadTokens(path); err == nil {
		t.Fatal("отсутствующий файл токенов должен давать ошибку")
	}
}
