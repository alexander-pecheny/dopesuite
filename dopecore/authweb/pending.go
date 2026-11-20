package authweb

import (
	"log"
	"net/http"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
	"code.pecheny.me/pecheny/dopesuite/dopecore/tglogin"
)

// PendingCookieName holds the code of a telegram login this browser started
// and has not finished. The code is already the login's whole secret — the
// status URL trades it for a session — so keeping it in an HttpOnly cookie
// next to the session cookie adds nothing an attacker could use, and it is
// what lets the login finish in whichever tab comes back: people confirm in
// Telegram and then open the site afresh instead of returning to the tab that
// was polling, which a phone has frozen anyway.
const PendingCookieName = "tg_pending"

func setPending(w http.ResponseWriter, code string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     PendingCookieName,
		Value:    code,
		Path:     "/",
		HttpOnly: true,
		Secure:   session.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   max(1, int(time.Until(expires)/time.Second)),
	})
}

func clearPending(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     PendingCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   session.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// PendingCode is the code of the login this browser has pending, or "".
func PendingCode(r *http.Request) string {
	c, err := r.Cookie(PendingCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

// isNavigation is a page load: a tab opened or reloaded, not an asset, an API
// call or an event stream. Browsers say so in Sec-Fetch-Mode; an older one is
// read by what it accepts.
func isNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" {
		return mode == "navigate"
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// FinishPending wraps an app's handler so a page load from a browser with a
// pending telegram login finishes it: once the bot has confirmed a known
// telegram, the load gets the session cookie and is sent to the same URL
// again, now logged in — whatever tab it came from. A brand-new telegram still
// has a username to pick, which is the login page's job; that page reads the
// pending login itself (Status with no code). A login the bot has not
// confirmed yet, or a lookup that fails, leaves the load as it was.
func (web Web) FinishPending(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := PendingCode(r)
		if code == "" || !isNavigation(r) {
			next.ServeHTTP(w, r)
			return
		}
		out, err := web.resolve(r.Context(), code)
		if err != nil {
			log.Printf("authweb: finishing a pending telegram login: %v", err)
			next.ServeHTTP(w, r)
			return
		}
		settle(w, out)
		if out.Status == tglogin.Ready {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, r.URL.RequestURI(), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
