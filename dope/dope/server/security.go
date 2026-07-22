package dopeserver

import (
	"net/http"
	"strings"

	"pecheny.me/dopecore/session"
)

// dopeCSP locks pages to same-origin scripts and styles: no inline script, no
// eval, no third-party origins. dope emits no inline <script> (the per-request
// init payload is a <script type="application/json"> data block read by init.js)
// and no inline style, so 'self' needs neither a nonce nor 'unsafe-inline'. The
// SSE streams (/events, /host-events) and the JSON API are same-origin, covered
// by connect-src 'self'; the projector board and every page are JS-built from
// same-origin assets. frame-ancestors 'none' (with X-Frame-Options) blocks
// clickjacking.
const dopeCSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// securityHeaders sets the CSP and companion hardening headers on every response
// (HTML, API, SSE and static assets alike), so no serving path can forget them.
// nosniff also guards the static file server from MIME-sniffing.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", dopeCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// slideSessionCookie re-emits the session cookie on every request that carries
// one, so its browser MaxAge tracks the sliding server session instead of dying
// 30 days after login however active the user is (the DB session already slides;
// the cookie did not). Handlers that set their own session cookie (login,
// register, logout) run after and their later Set-Cookie header wins.
func slideSessionCookie(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip cacheable static assets and the SSE streams: adding Set-Cookie to a
		// static response defeats shared caching, and the app-page/API responses
		// that actually carry the session already slide it.
		p := r.URL.Path
		if c, err := r.Cookie(session.CookieName); err == nil && c.Value != "" &&
			!strings.HasPrefix(p, "/static/") && p != "/events" && p != "/host-events" {
			session.SetCookie(w, c.Value)
		}
		next.ServeHTTP(w, r)
	})
}
