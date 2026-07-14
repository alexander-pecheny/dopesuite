// Package session holds the authenticated-identity type and the session-cookie
// helpers shared by both apps. It is a pure data leaf — no server coupling.
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

// ProdEnvVar names the environment variable consulted by IsProdEnv. Each app
// sets it at startup; the apps' deployed environments predate this package and
// use different names, so it stays configurable rather than unified.
var ProdEnvVar = "APP_ENV"

// User is a resolved session identity: the session row id, the user it belongs
// to, and the display fields. Username/Telegram are nullable for telegram-only
// accounts. IsSystem is only meaningful in apps that have a system role.
type User struct {
	SessionID int64
	UserID    int64
	Username  sql.NullString
	Telegram  sql.NullString
	IsSystem  bool
}

// IsProdEnv reports whether the server is running in production.
func IsProdEnv() bool {
	return strings.EqualFold(os.Getenv(ProdEnvVar), "production")
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

// HasCookie reports whether a non-empty session cookie is present. Cheap probe
// used on hot read paths before any DB lookup.
func HasCookie(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	return err == nil && c.Value != ""
}

// StartRegisterResponse is returned when a registration is initiated.
type StartRegisterResponse struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expires_at"`
}

// RegisterStatusResponse reports the status of a pending registration.
type RegisterStatusResponse struct {
	Status   string  `json:"status"`
	Username *string `json:"username,omitempty"`
}
