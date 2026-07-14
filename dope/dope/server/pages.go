package dopeserver

import (
	"fmt"
	"io/fs"
	"net/http"

	dopeui "dope/dope/web/ui"
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

// pageSources maps a served HTML shell path to its .dopeui source. These five
// pages are authored in the constrained UI DSL (dope/web/assets/ui) and compiled
// to HTML — at startup in embed mode, per request in disk/dev mode. Their
// compiled bytes feed the existing init-splice + versionAssetRefs pipeline
// unchanged; everything else is read from the asset FS verbatim.
var pageSources = map[string]string{
	"static/login.html":  "ui/login.dopeui",
	"static/host.html":   "ui/host.dopeui",
	"static/viewer.html": "ui/viewer.dopeui",
	"static/od.html":     "ui/od.dopeui",
	"static/si.html":     "ui/si.dopeui",
}

// pageBytes returns the HTML for a shell path: the compiled .dopeui page for the
// five DSL-authored shells (cached in embed mode, recompiled per request in disk
// mode), else the raw asset bytes.
func (s *server) pageBytes(path string) ([]byte, error) {
	src, ok := pageSources[path]
	if !ok {
		return fs.ReadFile(s.eng.Assets, path)
	}
	if !s.eng.AssetNoCache {
		s.pageMu.Lock()
		body, hit := s.pageCache[path]
		s.pageMu.Unlock()
		if hit {
			return body, nil
		}
	}
	raw, err := fs.ReadFile(s.eng.Assets, src)
	if err != nil {
		return nil, err
	}
	body, err := dopeui.Compile(src, raw)
	if err != nil {
		return nil, err
	}
	if !s.eng.AssetNoCache {
		s.pageMu.Lock()
		if s.pageCache == nil {
			s.pageCache = map[string][]byte{}
		}
		s.pageCache[path] = body
		s.pageMu.Unlock()
	}
	return body, nil
}

// warmPageCache compiles every DSL page up front in embed mode so a broken page
// fails at startup, not on a request. No-op in disk mode (recompiles per request).
func (s *server) warmPageCache() error {
	if s.eng.AssetNoCache {
		return nil
	}
	for path := range pageSources {
		if _, err := s.pageBytes(path); err != nil {
			return fmt.Errorf("compile %s: %w", path, err)
		}
	}
	return nil
}
