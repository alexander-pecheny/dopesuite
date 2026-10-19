package qyserver

import (
	"net/http"
	"strconv"

	"code.pecheny.me/pecheny/dopesuite/dopecore/webassets"
	kit "pecheny.me/dopeuikit/kit"

	"qy/qy/web/assets"
	"qy/qy/web/route"
	"qy/qy/web/ui"

	qystrings "qy/i18nstrings"
)

// newAssets resolves qy's asset source (live disk in dev, else the embedded
// FS) with the kit's files wired in. The only .dopeui page is the kit's login;
// every other page is built on the server with the typed builder.
func newAssets() (*webassets.Assets, *kit.PageSet) {
	a := kit.Assets(assets.FS, ".", "qy/web/assets")
	return a, kit.NewPageSet(a.Source, a.NoCache, ui.Compile).
		Provide("ui/login.dopeui", kit.LoginPage(qystrings.Default.Auth.Page.Title(), "/"))
}

var pagePaths = []string{"ui/login.dopeui"}

// handleLogin serves the shared login page, compiled with the visitor's own
// destination when they arrive with one.
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	next := kit.SafeLoginRedirect(r.URL.Query().Get("next"))
	if next == "" {
		body, err := s.pages.Bytes("ui/login.dopeui")
		if err != nil {
			return err
		}
		s.writePage(w, r, http.StatusOK, body)
		return nil
	}
	body, err := ui.Compile("ui/login.dopeui", kit.LoginPage(qystrings.Default.Auth.Page.Title(), next))
	if err != nil {
		return err
	}
	s.writePage(w, r, http.StatusOK, body)
	return nil
}

// writePage sends compiled page HTML with asset-ref versioning and the CSP.
func (s *server) writePage(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	body = s.assets.VersionRefs(body)
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// contentSecurityPolicy locks every page to same-origin scripts. Images may
// come from anywhere on https: a question's handout often lives on an image
// host, and the archived years link pictures that way too.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data: https:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"
