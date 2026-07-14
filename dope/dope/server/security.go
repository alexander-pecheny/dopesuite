package dopeserver

import "net/http"

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
