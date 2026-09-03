package server

import (
	"io/fs"
	"net/http"
	"strconv"

	"strings"

	kit "pecheny.me/dopeuikit/kit"

	"pecheny.me/dopecore/webassets"

	xystrings "xy/i18nstrings"
	"xy/internal/ui"
	"xy/web/assets"
)

// newAssets resolves xy's asset source (live disk in dev, else the embedded FS)
// with the kit's files wired in, and the page set that compiles ui/*.dopeui.
func newAssets() (*webassets.Assets, *kit.PageSet) {
	a := kit.Assets(assets.FS, ".", "web/assets")
	return a, kit.NewPageSet(a.Source, a.NoCache, ui.Compile).Provide("ui/login.dopeui", kit.LoginPage(xystrings.Default.Auth.Page.Title(), "/"))
}

// pagePaths are the ui/*.dopeui sources servePage compiles; warmed up front in
// embed mode so a broken page fails at startup rather than on a request.
var pagePaths = []string{
	"ui/login.dopeui",
	"ui/profile.dopeui",
	"ui/tokens.dopeui",
	"ui/import.dopeui",
	"ui/board.dopeui",
	"ui/join.dopeui",
	"ui/index.dopeui",
}

// servePage compiles and serves a ui/*.dopeui page with asset-ref versioning and
// a strict Content-Security-Policy (XSS = total client compromise, so all
// crypto-bearing pages get a locked-down CSP).
func (s *server) servePage(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := s.pages.Bytes(name)
		if err != nil {
			handleErr(w, err)
			return
		}
		s.writePage(w, r, body)
	}
}

// handleLogin serves the shared login page. An invitee who followed a link while
// logged out arrives as /login?next=/join/<code>, and the page has to send them
// back there afterwards — so that one case compiles a copy with its own
// destination, and everything else gets the page compiled at startup.
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	next := safeLoginNext(r.URL.Query().Get("next"))
	if next == "" {
		s.servePage("ui/login.dopeui")(w, r)
		return
	}
	body, err := ui.Compile("ui/login.dopeui", kit.LoginPage(xystrings.Default.Auth.Page.Title(), next))
	if err != nil {
		handleErr(w, err)
		return
	}
	s.writePage(w, r, body)
}

// safeLoginNext allows exactly one shape of post-login destination: an invite
// link's own page. `next` is whatever the URL carried, so an allow-list of one
// pattern is the whole defence against turning /login into an open redirect.
func safeLoginNext(next string) string {
	code, ok := strings.CutPrefix(next, "/join/")
	if !ok || code == "" || len(code) > 64 {
		return ""
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return next
}

// writePage sends compiled page HTML with asset-ref versioning and the CSP.
func (s *server) writePage(w http.ResponseWriter, r *http.Request, body []byte) {
	body = s.assets.VersionRefs(body)
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

// contentSecurityPolicy locks the page to same-origin scripts only: no inline
// scripts, no eval, no wasm, no third-party origins. This is the real defense
// for client-side crypto. worker-src/manifest-src keep the PWA's
// service worker and web manifest same-origin too.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data: blob:; " +
	"frame-src 'self' blob:; " + // handout PDF preview is an in-memory blob: iframe
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"worker-src 'self'; " +
	"manifest-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// serveRootAsset serves a single embedded static file at a root path (used for
// the PWA service worker and web manifest, which must live at the site root so
// the worker's scope covers the whole app). extra headers are applied verbatim.
func (s *server) serveRootAsset(name, contentType, cacheControl string, extra map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := fs.ReadFile(s.assets.Source, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if cacheControl != "" {
			w.Header().Set("Cache-Control", cacheControl)
		}
		for k, v := range extra {
			w.Header().Set(k, v)
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(body)
	}
}
