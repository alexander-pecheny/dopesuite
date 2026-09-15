package spliffserver

import (
	"net/http"
	"strconv"

	"pecheny.me/dopecore/webassets"
	kit "pecheny.me/dopeuikit/kit"

	"spliff/spliff/web/assets"
	"spliff/spliff/web/route"
	"spliff/spliff/web/ui"

	spliffstrings "spliff/i18nstrings"
)

// newAssets resolves Spliff's asset source (live disk in dev, else the embedded
// FS) with the kit's files wired in, and the page set that compiles ui/*.dopeui.
func newAssets() (*webassets.Assets, *kit.PageSet) {
	a := kit.Assets(assets.FS, ".", "spliff/web/assets")
	return a, kit.NewPageSet(a.Source, a.NoCache, ui.Compile).
		Provide("ui/login.dopeui", kit.LoginPage(spliffstrings.Default.Auth.Page.Title(), "/"))
}

// pagePaths are the .dopeui sources servePage compiles; warmed at startup in
// embed mode so a broken page fails there rather than on somebody's request.
var pagePaths = []string{
	"ui/login.dopeui",
	"ui/index.dopeui",
	"ui/group.dopeui",
	"ui/transaction.dopeui",
	"ui/join.dopeui",
}

// servePage compiles and serves a .dopeui page with asset-ref versioning and
// the CSP.
func (s *server) servePage(name string) route.Handler {
	return func(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
		body, err := s.pages.Bytes(name)
		if err != nil {
			return err
		}
		s.writePage(w, r, body)
		return nil
	}
}

// handleLogin serves the shared login page. Somebody sent to /login from a page
// of ours — an invitee who followed an Invite Link while logged out arrives as
// /login?next=/join/<code> — has to land back there afterwards, so that case
// compiles a copy carrying its own destination; everything else gets the page
// compiled at startup.
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	next := kit.SafeLoginRedirect(r.URL.Query().Get("next"))
	if next == "" {
		body, err := s.pages.Bytes("ui/login.dopeui")
		if err != nil {
			return err
		}
		s.writePage(w, r, body)
		return nil
	}
	body, err := ui.Compile("ui/login.dopeui",
		kit.LoginPage(spliffstrings.Default.Auth.Page.Title(), next))
	if err != nil {
		return err
	}
	s.writePage(w, r, body)
	return nil
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

// contentSecurityPolicy locks every page to same-origin scripts: no inline
// script, no eval, no third-party origin. img-src allows blob: because the
// Photo form previews the picture the phone picked before it is uploaded.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"
