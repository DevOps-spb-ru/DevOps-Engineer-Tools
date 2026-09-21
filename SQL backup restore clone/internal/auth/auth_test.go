package auth

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/passwd"
)

// testPassword — пароль тестовых пользователей. Хэш считается один раз: bcrypt
// намеренно медленный, и пересчёт в каждом тесте замедлил бы прогон.
const testPassword = "s3cret-password"

var testHash = sync.OnceValue(func() string {
	hash, err := passwd.Hash(testPassword)
	if err != nil {
		panic(err)
	}
	return hash
})

// testOptions — настройки аутентификации с одним пользователем и одним токеном.
// Время задаётся снаружи: тесты проверяют истечение сессий и паузы между
// попытками входа, не выжидая реальные секунды.
func testOptions(now *time.Time) Options {
	return Options{
		Users:            []config.User{{Login: "ivan", PasswordBcrypt: testHash()}},
		Tokens:           []Token{{Login: "ci", Value: strings.Repeat("k", MinTokenLength)}},
		SessionTTL:       time.Hour,
		LoginBackoff:     time.Second,
		MaxLoginAttempts: 3,
		Now:              func() time.Time { return *now },
	}
}

func TestNewRejectsUnusableOptions(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	base := testOptions(&now)
	cases := []struct {
		name   string
		tune   func(*Options)
		substr string
	}{
		{
			name:   "нет пользователей и токенов",
			tune:   func(o *Options) { o.Users = nil; o.Tokens = nil },
			substr: "не задан ни один пользователь",
		},
		{
			name:   "время жизни сессии не задано",
			tune:   func(o *Options) { o.SessionTTL = 0 },
			substr: "время жизни сессии",
		},
		{
			name:   "отрицательная пауза между попытками",
			tune:   func(o *Options) { o.LoginBackoff = -time.Second },
			substr: "не может быть отрицательной",
		},
		{
			name:   "предел попыток меньше одной",
			tune:   func(o *Options) { o.MaxLoginAttempts = 0 },
			substr: "предел попыток входа",
		},
		{
			name:   "логин без хэша пароля",
			tune:   func(o *Options) { o.Users = []config.User{{Login: "ivan"}} },
			substr: "password_bcrypt",
		},
		{
			name:   "логин задан дважды",
			tune:   func(o *Options) { o.Users = append(o.Users, o.Users[0]) },
			substr: "задан дважды",
		},
		{
			name:   "короткий токен",
			tune:   func(o *Options) { o.Tokens = []Token{{Login: "ci", Value: "short"}} },
			substr: "короче",
		},
		{
			name:   "токен задан дважды",
			tune:   func(o *Options) { o.Tokens = append(o.Tokens, o.Tokens[0]) },
			substr: "задан дважды",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			opts := base
			opts.Users = append([]config.User(nil), base.Users...)
			opts.Tokens = append([]Token(nil), base.Tokens...)
			testCase.tune(&opts)
			if _, err := New(opts); err == nil {
				t.Fatal("ожидалась ошибка, получено nil")
			} else if !strings.Contains(err.Error(), testCase.substr) {
				t.Fatalf("ошибка %q не содержит %q", err, testCase.substr)
			}
		})
	}
}

func TestLoginAndSession(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	auth, err := New(testOptions(&now))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session, err := auth.Login("ivan", testPassword)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if session.Login != "ivan" || session.Via != ViaWeb {
		t.Fatalf("сессия принадлежит %q (%s), ожидался ivan (web)", session.Login, session.Via)
	}
	if session.CSRFToken == "" || session.ID == "" {
		t.Fatal("идентификатор сессии и CSRF-токен должны быть заполнены")
	}
	if !session.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("время жизни сессии %s, ожидалось %s", session.ExpiresAt, now.Add(time.Hour))
	}
	if auth.Sessions() != 1 {
		t.Fatalf("живых сессий %d, ожидалась одна", auth.Sessions())
	}

	// Cookie приходит от клиента: сессия ищется по идентификатору.
	stored, err := auth.Session(session.ID)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !stored.VerifyCSRF(session.CSRFToken) {
		t.Fatal("CSRF-токен сессии не совпал сам с собой")
	}
	if stored.VerifyCSRF("чужой токен") {
		t.Fatal("чужой CSRF-токен принят")
	}

	auth.Logout(session.ID)
	if _, err := auth.Session(session.ID); !errors.Is(err, ErrNoSession) {
		t.Fatalf("после выхода ожидалась ErrNoSession, получено %v", err)
	}
}

func TestSessionExpiresAndIsRemoved(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	auth, err := New(testOptions(&now))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session, err := auth.Login("ivan", testPassword)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	now = now.Add(time.Hour + time.Second)
	if _, err := auth.Session(session.ID); !errors.Is(err, ErrNoSession) {
		t.Fatalf("истёкшая сессия принята: %v", err)
	}
	if auth.Sessions() != 0 {
		t.Fatalf("истёкшая сессия осталась в карте: %d", auth.Sessions())
	}
}

func TestLoginRejectsWrongCredentials(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	auth, err := New(testOptions(&now))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := auth.Login("ivan", "неверный-пароль"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("неверный пароль: ожидалась ErrInvalidCredentials, получено %v", err)
	}
	if _, err := auth.Login("петр", testPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("неизвестный логин: ожидалась ErrInvalidCredentials, получено %v", err)
	}
	if auth.Sessions() != 0 {
		t.Fatalf("после неудачных входов сессий быть не должно: %d", auth.Sessions())
	}
}

func TestLoginBackoffAndLockout(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	opts := testOptions(&now)
	opts.MaxLoginAttempts = 3
	auth, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Первая неудачная попытка: пауза login_backoff.
	if _, err := auth.Login("ivan", "неверный-пароль"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("первая попытка: %v", err)
	}
	if _, err := auth.Login("ivan", testPassword); !errors.Is(err, ErrLocked) {
		t.Fatalf("вторая попытка сразу: ожидалась ErrLocked, получено %v", err)
	}
	now = now.Add(time.Second)
	if _, err := auth.Login("ivan", "неверный-пароль"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("после паузы: %v", err)
	}
	// Третья неудачная попытка исчерпывает предел: вход блокируется надолго.
	now = now.Add(2 * time.Second)
	if _, err := auth.Login("ivan", "неверный-пароль"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("третья попытка: %v", err)
	}
	if _, err := auth.Login("ivan", testPassword); !errors.Is(err, ErrLocked) {
		t.Fatalf("после предела попыток: ожидалась ErrLocked, получено %v", err)
	}
	// Блокировка снимается сама: верный пароль после паузы проходит.
	now = now.Add(lockoutDuration + time.Second)
	if _, err := auth.Login("ivan", testPassword); err != nil {
		t.Fatalf("вход после блокировки: %v", err)
	}
	// Успешный вход сбрасывает счётчик: следующая неудача снова даёт малую паузу.
	now = now.Add(time.Hour)
	if _, err := auth.Login("ivan", "неверный-пароль"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("неудача после успешного входа: %v", err)
	}
	now = now.Add(time.Second)
	if _, err := auth.Login("ivan", testPassword); err != nil {
		t.Fatalf("вход после сброса счётчика: %v", err)
	}
}

func TestToken(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	auth, err := New(testOptions(&now))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	identity, err := auth.Token(strings.Repeat("k", MinTokenLength))
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if identity.Login != "ci" || identity.Via != ViaAPI {
		t.Fatalf("токен принадлежит %q (%s), ожидался ci (api)", identity.Login, identity.Via)
	}
	for _, value := range []string{"", "не тот токен", strings.Repeat("k", MinTokenLength-1)} {
		if _, err := auth.Token(value); !errors.Is(err, ErrTokenInvalid) {
			t.Fatalf("токен %q: ожидалась ErrTokenInvalid, получено %v", value, err)
		}
	}
}

func TestIdentityOperator(t *testing.T) {
	cases := []struct {
		identity Identity
		want     string
	}{
		{identity: Identity{Login: "ivan", Via: ViaWeb}, want: "ivan (web)"},
		{identity: Identity{Login: "ci", Via: ViaAPI}, want: "ci (api)"},
		{identity: Identity{Via: ViaAPI}, want: "api"},
	}
	for _, testCase := range cases {
		if got := testCase.identity.Operator(); got != testCase.want {
			t.Errorf("Operator() = %q, ожидалось %q", got, testCase.want)
		}
	}
}
