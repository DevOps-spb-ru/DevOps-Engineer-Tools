package web

import (
	"net"
	"net/http"
	"strings"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/auth"
)

// setSessionCookie ставит cookie сессии. Флаги выбраны так, чтобы cookie не
// читалась скриптами (HttpOnly) и не отправлялась с чужих сайтов
// (SameSite=Strict): этого достаточно, чтобы форма со стороннего сайта не
// работала от имени вошедшего человека.
func (s *Server) setSessionCookie(w http.ResponseWriter, session auth.Session) {
	// gosec: Secure зависит от TLS — без сертификата браузер не отправил бы
	// cookie по HTTP, и вход «молча» не работал бы (см. server.allow_insecure).
	//nolint:gosec // G124: HttpOnly и SameSite=Strict заданы, Secure включается вместе с TLS
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    session.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// Secure включается вместе с TLS: без него браузер не отправил бы cookie
		// по HTTP, и вход «молча» не работал бы.
		Secure:  s.cfg.Server.TLS.Enabled(),
		Expires: session.ExpiresAt,
	})
}

// clearSessionCookie удаляет cookie при выходе: одного удаления сессии на
// сервере мало — браузер иначе пришлёт её снова.
func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	// Тот же случай, что и при выдаче cookie: Secure включается вместе с TLS.
	//nolint:gosec // G124: HttpOnly и SameSite=Strict заданы, Secure включается вместе с TLS
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.cfg.Server.TLS.Enabled(),
		MaxAge:   -1,
	})
}

// clientIP возвращает адрес клиента для журнала сервиса. Адрес из
// X-Forwarded-For принимается только от доверенных прокси
// (server.trusted_proxies): иначе его подделал бы любой клиент одним заголовком,
// и «источник операции» в журнале стал бы бессмысленным.
func (s *Server) clientIP(r *http.Request) string {
	host := remoteHost(r.RemoteAddr)
	if host == "" {
		return r.RemoteAddr
	}
	if s.trustedProxy(host) {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			// В списке адресов первый — исходный клиент, остальные добавлены
			// прокси по пути.
			first, _, _ := strings.Cut(forwarded, ",")
			return strings.TrimSpace(first)
		}
	}
	return host
}

// trustedProxy отвечает, входит ли адрес в список доверенных прокси. Кроме
// точного адреса поддерживается подсеть в формате CIDR: прокси в другой сети
// иначе пришлось бы перечислять по одному.
func (s *Server) trustedProxy(host string) bool {
	ip := net.ParseIP(host)
	for _, trusted := range s.cfg.Server.TrustedProxies {
		trusted = strings.TrimSpace(trusted)
		switch trusted {
		case "":
			continue
		case host:
			return true
		}
		if _, network, err := net.ParseCIDR(trusted); err == nil && ip != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

// remoteHost отделяет адрес от порта в r.RemoteAddr.
func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
