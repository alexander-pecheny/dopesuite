package dopeserver

import (
	"io/fs"
	"net/http"
	"os"
	"strings"

	kit "pecheny.me/dopeuikit/kit"
)

// buildStylesheet concatenates DopeUIKit's core.css ahead of dope's app layer
// (the on-disk static/styles.css). In disk/dev mode it reads core.css from the
// sibling dopeuikit checkout when present (hot reload), else the embedded copy.
func (s *server) buildStylesheet() []byte {
	core := kit.CoreCSS
	if s.eng.AssetNoCache {
		if b, err := os.ReadFile("../dopeuikit/assets/core.css"); err == nil {
			core = b
		}
	}
	layer, _ := fs.ReadFile(s.eng.Assets, "static/styles.css")
	out := make([]byte, 0, len(core)+1+len(layer))
	out = append(out, core...)
	out = append(out, '\n')
	return append(out, layer...)
}

// serveStylesheet serves the concatenated core+dope stylesheet with the same
// cache-busting semantics staticFileServer applies to versioned assets.
func (s *server) serveStylesheet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := s.stylesheet
		if s.eng.AssetNoCache {
			body = s.buildStylesheet() // hot reload in dev
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			if tag := s.eng.AssetETags["/static/styles.css"]; tag != "" {
				w.Header().Set("ETag", tag)
			}
			if strings.Contains(r.URL.RawQuery, "v=") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=3600, stale-while-revalidate=604800")
			}
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(body)
	}
}

// serveFonts serves /static/fonts/* from DopeUIKit's font FS (disk in dev, else
// embedded). The fonts are byte-identical across apps and live in the kit.
func (s *server) serveFonts() http.Handler {
	var fsys fs.FS = kit.Fonts // rooted at "fonts/…"
	if s.eng.AssetNoCache {
		if info, err := os.Stat("../dopeuikit/assets/fonts"); err == nil && info.IsDir() {
			fsys = os.DirFS("../dopeuikit/assets")
		}
	}
	handler := http.StripPrefix("/static/", http.FileServer(http.FS(fsys)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		handler.ServeHTTP(w, r)
	})
}
