// Package sameorigin is the CSRF check every app runs on a request that
// changes state. The session cookie is SameSite=Lax, so a cross-site POST does
// not carry it anyway; this refuses such a request outright as a second lock.
//
// The rule is dope's, which went through two fixes in June 2026:
//
//   - a request with no Origin header passes. Same-origin GETs and old clients
//     send none, and there is no Referer fallback: an empty Referer is as
//     common as an empty Origin, so checking it would refuse real users and
//     stop nobody;
//   - an Origin passes when its host is the request's own Host, compared
//     without regard to case, or one of the trusted hosts;
//   - X-Forwarded-Host is never consulted. Anyone can send it, so trusting it
//     would let a forged header vouch for a forged Origin. A mirror that
//     proxies to the app with Host rewritten (dope.pecheny.kz,
//     xy.pecheny.ru) is listed in the trusted hosts instead.
package sameorigin

import (
	"net/http"
	"net/url"
	"strings"
)

// Safe reports whether method cannot change state, so the check skips it.
func Safe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// Allowed reports whether r may proceed. trusted is the app's list of extra
// hosts as its env var holds it: comma separated, each a bare host or a URL.
func Allowed(r *http.Request, trusted string) bool {
	if Safe(r.Method) {
		return true
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return Host(u.Host, r, trusted)
}

// Host reports whether originHost is the request's own host or a trusted one.
func Host(originHost string, r *http.Request, trusted string) bool {
	return strings.EqualFold(originHost, r.Host) || Trusted(originHost, trusted)
}

// Trusted reports whether originHost is in the trusted list.
func Trusted(originHost, trusted string) bool {
	for _, candidate := range strings.Split(trusted, ",") {
		host := strings.TrimSpace(candidate)
		if host == "" {
			continue
		}
		if u, err := url.Parse(host); err == nil && u.Host != "" {
			host = u.Host
		}
		if strings.EqualFold(originHost, host) {
			return true
		}
	}
	return false
}

// Guard is Allowed at an HTTP edge: when the check fails it writes a 403 and
// returns false.
func Guard(w http.ResponseWriter, r *http.Request, trusted string) bool {
	if Allowed(r, trusted) {
		return true
	}
	http.Error(w, "forbidden", http.StatusForbidden)
	return false
}

// Middleware guards every request next serves. trusted is read per request,
// so a test (or an operator's restart) sees the current value.
func Middleware(trusted func() string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Guard(w, r, trusted()) {
			next.ServeHTTP(w, r)
		}
	})
}
