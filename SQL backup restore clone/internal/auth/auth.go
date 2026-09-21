// Package auth отвечает за вход в веб-интерфейс и доступ к API сервиса.
//
// Пользователи описаны в конфиге (auth.users): логин и bcrypt-хэш пароля,
// который создаёт `sqlbrc hash-password` (см. internal/passwd). Открытых
// паролей в конфиге нет: файл читают администраторы, и он попадает в бэкап
// системы вместе с остальными настройками.
//
// Браузер после входа получает cookie с идентификатором сессии; сами сессии
// живут в памяти процесса (auth.session_ttl) и не переживают перезапуск
// сервиса. Благодаря этому не нужно хранить сессии в файле и убирать их оттуда
// при выходе. API работает без cookie: клиент присылает токен из
// auth.token_file (заголовок «Authorization: Bearer …»), поэтому к API
// неприменимы ни сессии, ни защита от CSRF.
//
// Неудачные попытки входа ограничены: после каждой пауза растёт
// (auth.login_backoff), а после auth.max_login_attempts вход по этому логину
// временно блокируется. Счётчики живут в памяти: перезапуск сервиса их
// сбрасывает, и это осознанный размен — подбор пароля не должен превращаться
// в запись на диск.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/config"
	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/passwd"
)

const (
	// SessionCookieName — имя cookie сессии веб-интерфейса.
	SessionCookieName = "sqlbrc_session"
	// MinTokenLength — минимальная длина токена API. Короткий токен подбирается
	// перебором: сервис внутренний и не ограничивает частоту запросов к API.
	MinTokenLength = 32
	// randomBytes — длина идентификатора сессии и CSRF-токена (в байтах).
	randomBytes = 32
	// maxBackoff — предел паузы после неудачной попытки: дальше растёт только
	// блокировка, иначе задержка стала бы неотличима от отказа сервиса.
	maxBackoff = 5 * time.Minute
	// lockoutDuration — на сколько блокируется вход после исчерпания попыток.
	lockoutDuration = 15 * time.Minute
)

// Ошибки входа. «Неверный логин» и «неверный пароль» намеренно не различаются:
// иначе по сообщению можно собрать список логинов сервиса.
var (
	// ErrInvalidCredentials — логин или пароль не подошли.
	ErrInvalidCredentials = errors.New("неверный логин или пароль")
	// ErrLocked — вход по этому логину временно заблокирован.
	ErrLocked = errors.New("слишком много неудачных попыток входа")
	// ErrTokenInvalid — токен API не подходит.
	ErrTokenInvalid = errors.New("неверный токен API")
	// ErrNoSession — сессия не найдена или истекла.
	ErrNoSession = errors.New("сессия не найдена или истекла")
)

// Via — как выполнено действие: из веб-интерфейса или через API. Значение
// попадает в журнал задач, чтобы по записи было видно, откуда пришла операция.
type Via string

const (
	// ViaWeb — действие выполнено в веб-интерфейсе (cookie-сессия).
	ViaWeb Via = "web"
	// ViaAPI — действие выполнено через API (токен).
	ViaAPI Via = "api"
)

// Identity — кто выполняет операцию.
type Identity struct {
	// Login — имя пользователя.
	Login string
	// Via — откуда пришло действие.
	Via Via
}

// Operator возвращает значение для журнала задач: имя и источник, например
// «ivan (web)» — так в записи видно, кто снял бэкап.
func (i Identity) Operator() string {
	if i.Login == "" {
		return string(i.Via)
	}
	return fmt.Sprintf("%s (%s)", i.Login, i.Via)
}

// Session — сессия веб-интерфейса.
type Session struct {
	// ID — идентификатор сессии; он лежит в cookie и служит ключом в базе сессий.
	ID string
	// Identity — кому принадлежит сессия.
	Identity
	// CSRFToken — токен защиты от подделки запроса: он подставляется в формы и
	// сверяется при каждом изменяющем запросе.
	CSRFToken string
	// ExpiresAt — до какого времени сессия считается действительной.
	ExpiresAt time.Time
	// LastSeenAt — когда сессия использовалась последний раз.
	LastSeenAt time.Time
}

// Valid отвечает, жива ли сессия в момент now.
func (s Session) Valid(now time.Time) bool { return now.Before(s.ExpiresAt) }

// VerifyCSRF сверяет токен из формы с токеном сессии. Сравнение постоянное по
// времени: иначе по времени ответа можно было бы подбирать токен посимвольно.
func (s Session) VerifyCSRF(token string) bool {
	if s.CSRFToken == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s.CSRFToken), []byte(token)) == 1
}

// Options — настройки аутентификации.
type Options struct {
	// Users — встроенные пользователи из auth.users.
	Users []config.User
	// Tokens — токены API из auth.token_file.
	Tokens []Token
	// SessionTTL — время жизни сессии.
	SessionTTL time.Duration
	// LoginBackoff — пауза после первой неудачной попытки входа.
	LoginBackoff time.Duration
	// MaxLoginAttempts — число неудачных попыток до блокировки входа.
	MaxLoginAttempts int
	// Now подменяет текущее время в тестах.
	Now func() time.Time
}

// Authenticator проверяет пароли и токены и выдаёт сессии.
type Authenticator struct {
	users  map[string]string
	tokens map[string]Token

	ttl         time.Duration
	backoff     time.Duration
	maxAttempts int
	now         func() time.Time

	mu       sync.Mutex
	sessions map[string]Session
	failures map[string]*failure
}

// failure — счётчик неудачных попыток входа по логину.
type failure struct {
	// count — сколько раз подряд не подошёл пароль.
	count int
	// next — время, раньше которого следующая попытка не проверяется.
	next time.Time
}

// New создаёт проверяющего вход. Ошибка означает, что конфигурация входа
// непригодна: без пользователей, токенов или с недостижимыми пределами сессий
// веб-интерфейс работал бы «сам по себе».
func New(opts Options) (*Authenticator, error) {
	if len(opts.Users) == 0 && len(opts.Tokens) == 0 {
		return nil, errors.New("не задан ни один пользователь (auth.users) и нет токенов API")
	}
	if opts.SessionTTL <= 0 {
		return nil, fmt.Errorf("время жизни сессии %s: должно быть больше нуля", opts.SessionTTL)
	}
	if opts.LoginBackoff < 0 {
		return nil, errors.New("пауза между попытками входа не может быть отрицательной")
	}
	if opts.MaxLoginAttempts < 1 {
		return nil, fmt.Errorf("предел попыток входа %d: должен быть не меньше одной", opts.MaxLoginAttempts)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	users := make(map[string]string, len(opts.Users))
	for index, user := range opts.Users {
		login := strings.TrimSpace(user.Login)
		if login == "" {
			return nil, fmt.Errorf("auth.users[%d]: не задан логин", index)
		}
		if strings.TrimSpace(user.PasswordBcrypt) == "" {
			return nil, fmt.Errorf(
				"auth.users[%d] (%s): не задан password_bcrypt — создайте его командой `sqlbrc hash-password`",
				index, login)
		}
		if _, exists := users[login]; exists {
			return nil, fmt.Errorf("auth.users: логин %s задан дважды", login)
		}
		users[login] = user.PasswordBcrypt
	}
	tokens := make(map[string]Token, len(opts.Tokens))
	for _, token := range opts.Tokens {
		value := strings.TrimSpace(token.Value)
		if len(value) < MinTokenLength {
			return nil, fmt.Errorf("токен API для %s короче %d символов", token.Login, MinTokenLength)
		}
		if _, exists := tokens[value]; exists {
			return nil, errors.New("один и тот же токен API задан дважды")
		}
		token.Value = value
		tokens[value] = token
	}
	return &Authenticator{
		users:       users,
		tokens:      tokens,
		ttl:         opts.SessionTTL,
		backoff:     opts.LoginBackoff,
		maxAttempts: opts.MaxLoginAttempts,
		now:         now,
		sessions:    make(map[string]Session),
		failures:    make(map[string]*failure),
	}, nil
}

// Login проверяет пароль и создаёт сессию. Неудачные попытки ограничены: пауза
// растёт после каждой из них, а после auth.max_login_attempts вход по логину
// блокируется на время lockoutDuration.
func (a *Authenticator) Login(login, password string) (Session, error) {
	login = strings.TrimSpace(login)
	now := a.now()
	if err := a.checkLock(login, now); err != nil {
		return Session{}, err
	}
	hash, known := a.users[login]
	if !known {
		// Неизвестный логин тоже считается неудачной попыткой: иначе перебор
		// логинов не ограничивался бы ничем.
		a.registerFailure(login, now)
		return Session{}, ErrInvalidCredentials
	}
	if err := passwd.Verify(hash, password); err != nil {
		a.registerFailure(login, now)
		return Session{}, ErrInvalidCredentials
	}
	a.mu.Lock()
	delete(a.failures, login)
	a.mu.Unlock()
	return a.newSession(Identity{Login: login, Via: ViaWeb}, now)
}

// Token проверяет токен API и возвращает, кому он принадлежит.
func (a *Authenticator) Token(value string) (Identity, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Identity{}, ErrTokenInvalid
	}
	token, ok := a.tokens[value]
	if !ok {
		return Identity{}, ErrTokenInvalid
	}
	return Identity{Login: token.Login, Via: ViaAPI}, nil
}

// Session возвращает живую сессию по идентификатору из cookie. Истёкшая сессия
// удаляется: иначе карта сессий росла бы до перезапуска сервиса.
func (a *Authenticator) Session(id string) (Session, error) {
	if id == "" {
		return Session{}, ErrNoSession
	}
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[id]
	if !ok {
		return Session{}, ErrNoSession
	}
	if !session.Valid(now) {
		delete(a.sessions, id)
		return Session{}, ErrNoSession
	}
	session.LastSeenAt = now
	a.sessions[id] = session
	return session, nil
}

// Logout удаляет сессию: выход из интерфейса должен отзывать доступ сразу, а не
// надеяться на истечение времени жизни.
func (a *Authenticator) Logout(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, id)
}

// Sessions возвращает число живых сессий: значение показывается в разделе
// диагностики и используется тестами. Истёкшие сессии при этом убираются.
func (a *Authenticator) Sessions() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	count := 0
	for id, session := range a.sessions {
		if !session.Valid(now) {
			delete(a.sessions, id)
			continue
		}
		count++
	}
	return count
}

// newSession выдаёт новую сессию: идентификатор и CSRF-токен случайные, время
// жизни берётся из конфига. Перед записью убираются истёкшие сессии — так карта
// не растёт от брошенных вкладок.
func (a *Authenticator) newSession(identity Identity, now time.Time) (Session, error) {
	id, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	session := Session{
		ID:         id,
		Identity:   identity,
		CSRFToken:  csrf,
		ExpiresAt:  now.Add(a.ttl),
		LastSeenAt: now,
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for existing, current := range a.sessions {
		if !current.Valid(now) {
			delete(a.sessions, existing)
		}
	}
	a.sessions[id] = session
	return session, nil
}

// registerFailure увеличивает счётчик неудачных попыток и переносит время
// следующей попытки: пауза удваивается с каждой попыткой, а после предела
// попыток включается блокировка.
func (a *Authenticator) registerFailure(login string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.failures[login]
	if current == nil {
		current = &failure{}
		a.failures[login] = current
	}
	current.count++
	pause := a.backoff
	for i := 1; i < current.count && pause < maxBackoff; i++ {
		pause *= 2
	}
	if pause > maxBackoff {
		pause = maxBackoff
	}
	if current.count >= a.maxAttempts {
		pause = lockoutDuration
	}
	current.next = now.Add(pause)
}

// checkLock отвечает, можно ли проверять пароль сейчас.
func (a *Authenticator) checkLock(login string, now time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.failures[login]
	if current == nil {
		return nil
	}
	if now.Before(current.next) {
		return fmt.Errorf("%w: повторите попытку после %s",
			ErrLocked, current.next.UTC().Format(time.RFC3339))
	}
	return nil
}

// randomToken возвращает случайную строку для идентификатора сессии и
// CSRF-токена: предсказуемый токен обесценил бы всю защиту.
func randomToken() (string, error) {
	buf := make([]byte, randomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("не удалось получить случайные данные: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
