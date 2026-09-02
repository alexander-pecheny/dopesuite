package dopeserver

import (
	"bytes"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	kit "pecheny.me/dopeuikit/kit"

	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
)

// serveCompiledPage serves a DSL-authored shell (e.g. the login page) through the
// same writeAppHTML pipeline as the other app shells: asset URLs get the
// "?v=<hash>" cache-buster and the shell stays no-cache.
func (s *server) serveCompiledPage(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := s.pageBytes(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s.writeAppHTML(w, r, body)
	}
}

// pageSources maps a served HTML shell path to its .dopeui source. These
// pages are authored in the constrained UI DSL (dope/web/assets/ui) and compiled
// to HTML — at startup in embed mode, per request in disk/dev mode. Their
// compiled bytes feed the existing init-splice + versionAssetRefs pipeline
// unchanged; everything else is read from the asset FS verbatim.
var pageSources = map[string]string{
	"static/login.html":  "ui/login.dopeui",
	"static/ek.html":     "ui/ek.dopeui",
	"static/od.html":     "ui/od.dopeui",
	"static/si.html":     "ui/si.dopeui",
	"static/brain.html":  "ui/brain.dopeui",
	"static/multi.html":  "ui/multi.dopeui",
	"static/troika.html": "ui/troika.dopeui",
	// The gallery renders every shared table and the Grid from fixtures —
	// the skin sheet the verify matrix shoots. Dev mode only (see main.go).
	"static/gallery.html": "ui/gallery.dopeui",
}

// pageBytes returns the HTML for a shell path: the compiled .dopeui page for the
// DSL-authored shells (cached in embed mode, recompiled per request in disk
// mode), else the raw asset bytes.
func (s *server) pageBytes(path string) ([]byte, error) {
	if src, ok := pageSources[path]; ok {
		return s.pageSet().Bytes(src)
	}
	return fs.ReadFile(s.eng.Assets, path)
}

// pageSet is built on first use from the engine's asset source, so a test that
// only sets eng.Assets compiles pages the same way the server does.
func (s *server) pageSet() *kit.PageSet {
	s.pagesOnce.Do(func() {
		s.pages = kit.NewPageSet(s.eng.Assets, s.eng.AssetNoCache, dopeui.Compile).
			Provide("ui/login.dopeui", kit.LoginPage(dopestrings.Default.Server.Login.Title(), "/host"))
	})
	return s.pages
}

// warmPageCache compiles every DSL page up front in embed mode so a broken page
// fails at startup, not on a request. No-op in disk mode (recompiles per request).
func (s *server) warmPageCache() error {
	srcs := make([]string, 0, len(pageSources))
	for _, src := range pageSources {
		srcs = append(srcs, src)
	}
	return s.pageSet().Warm(srcs...)
}

// serveLoginPage is /login. The compiled page carries one redirect target;
// ?next= replaces it so a registration link can send a visitor through the
// Telegram handshake and back to itself. Only a local path is honoured.
func (s *server) serveLoginPage() http.HandlerFunc {
	page := s.serveCompiledPage("static/login.html")
	return func(w http.ResponseWriter, r *http.Request) {
		next := SafeNextPath(r.URL.Query().Get("next"))
		if next == "" {
			page(w, r)
			return
		}
		rec := httptest.NewRecorder()
		page(rec, r)
		for key, values := range rec.Header() {
			w.Header()[key] = values
		}
		body := bytes.ReplaceAll(rec.Body.Bytes(),
			[]byte(`data-login-redirect="/host"`),
			[]byte(`data-login-redirect="`+html.EscapeString(next)+`"`))
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(rec.Code)
		_, _ = w.Write(body)
	}
}

// SafeNextPath keeps only a same-site path, so ?next= can never bounce a
// visitor off the site.
func SafeNextPath(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\r\n\"") {
		return ""
	}
	return next
}
