package session

import (
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

// IsProdEnv reports whether the server is running in production (DOPE_ENV).
func IsProdEnv() bool {
	return strings.EqualFold(os.Getenv("DOPE_ENV"), "production")
}

// SetCookie writes the session cookie carrying token.
func SetCookie(w http.ResponseWriter, token string) {
	cookie := &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   IsProdEnv(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(Lifetime / time.Second),
	}
	http.SetCookie(w, cookie)
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
// used on hot read paths (e.g. static lockdown) before any DB lookup.
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
