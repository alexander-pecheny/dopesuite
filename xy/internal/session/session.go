// Package session holds the authenticated-identity type and cookie helpers
// shared between the auth machinery and the HTTP handlers. It is a pure data
// leaf (no server coupling), ported from dope's platform/session.
package session

import (
	"database/sql"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// CookieName is the name of the HTTP-only session cookie.
	CookieName = "session"
	// Lifetime is how long a session (and its cookie) stays valid.
	Lifetime = 30 * 24 * time.Hour
	// TelegramAuthLifetime bounds a telegram login/register code's validity.
	TelegramAuthLifetime = time.Minute
)

// User is a resolved session identity: the session row id, the user it belongs
// to, and the display fields. Username/Telegram are nullable for telegram-only
// accounts.
type User struct {
	SessionID int64
	UserID    int64
	Username  sql.NullString
	Telegram  sql.NullString
}

// IsProdEnv reports whether the server is running in production (XY_ENV).
func IsProdEnv() bool {
	return strings.EqualFold(os.Getenv("XY_ENV"), "production")
}

// SetCookie writes the session cookie carrying token.
func SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   IsProdEnv(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(Lifetime / time.Second),
	})
}

// ClearCookie expires the session cookie.
func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   IsProdEnv(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// HasCookie reports whether a non-empty session cookie is present.
func HasCookie(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	return err == nil && c.Value != ""
}
