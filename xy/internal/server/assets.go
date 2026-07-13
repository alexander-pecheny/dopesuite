package server

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	kit "pecheny.me/dopeuikit/kit"
	"xy/internal/ui"
	"xy/web/assets"
)

// assetFS is the subset of fs.FS the server needs from the asset source.
type assetFS = fs.FS

// staticSource picks the asset source: live disk in dev (hot reload), else the
// embedded FS. Returns the FS rooted so that "static/..." paths resolve.
func staticSource() (fs.FS, string) {
	for _, dir := range []string{".", "web/assets"} {
		if info, err := os.Stat(dir + "/static"); err == nil && info.IsDir() {
			return os.DirFS(dir), "disk"
		}
	}
	return assets.FS, "embed"
}

// buildAssetETags precomputes a content-hash ETag for every embedded file
// (embed mode only; disk mode serves no-cache).
func buildAssetETags(source fs.FS) map[string]string {
	etags := map[string]string{}
	_ = fs.WalkDir(source, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, rerr := fs.ReadFile(source, path)
		if rerr != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		etags["/"+path] = `"` + hex.EncodeToString(sum[:16]) + `"`
		return nil
	})
	return etags
}

// staticFileServer serves the asset FS with cache-busting headers.
func staticFileServer(source fs.FS, noCache bool, etags map[string]string) http.Handler {
	handler := http.FileServer(http.FS(source))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case noCache:
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(r.URL.Path, "/static/fonts/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			if tag := etags[r.URL.Path]; tag != "" {
				w.Header().Set("ETag", tag)
			}
			if strings.Contains(r.URL.RawQuery, "v=") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=3600, stale-while-revalidate=604800")
			}
		}
		handler.ServeHTTP(w, r)
	})
}

// buildStylesheet concatenates DopeUIKit's core.css ahead of xy's layer (the
// on-disk static/styles.css). In disk dev mode it reads core.css from the
// sibling dopeuikit checkout when present (hot reload), else the embedded copy.
func (s *server) buildStylesheet() []byte {
	core := kit.CoreCSS
	if s.assetNoCache {
		if b, err := os.ReadFile("../dopeuikit/assets/core.css"); err == nil {
			core = b
		}
	}
	xyLayer, _ := fs.ReadFile(s.assetSource, "static/styles.css")
	out := make([]byte, 0, len(core)+1+len(xyLayer))
	out = append(out, core...)
	out = append(out, '\n')
	return append(out, xyLayer...)
}

// serveStylesheet serves the concatenated core+xy stylesheet with the same
// cache-busting semantics staticFileServer applies to versioned assets.
func (s *server) serveStylesheet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := s.stylesheet
		if s.assetNoCache {
			body = s.buildStylesheet() // hot reload in dev
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			if tag := s.assetETags["/static/styles.css"]; tag != "" {
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
	if s.assetNoCache {
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

// ---- HTML page serving with asset versioning ----

var assetRefRe = regexp.MustCompile(`(src|href)="(/static/[^"?]+\.(?:js|css))"`)

// versionAssetRefs rewrites /static/*.js|css refs to add ?v=<hash> so versioned
// requests can be cached immutably.
func (s *server) versionAssetRefs(body []byte) []byte {
	if len(s.assetETags) == 0 {
		return body
	}
	return assetRefRe.ReplaceAllFunc(body, func(m []byte) []byte {
		sub := assetRefRe.FindSubmatch(m)
		path := string(sub[2])
		tag := strings.Trim(s.assetETags[path], `"`)
		if tag == "" {
			return m
		}
		return []byte(fmt.Sprintf(`%s="%s?v=%s"`, sub[1], path, tag))
	})
}

// pagePaths are the ui/*.dopeui sources servePage compiles; warmPageCache
// compiles them all up front in embed mode.
var pagePaths = []string{
	"ui/login.dopeui",
	"ui/register.dopeui",
	"ui/profile.dopeui",
	"ui/tokens.dopeui",
	"ui/import.dopeui",
	"ui/board.dopeui",
	"ui/index.dopeui",
}

// warmPageCache compiles every page in embed mode so a broken page fails fast
// at startup rather than surfacing on a request. No-op in disk mode, which
// recompiles per request for hot reload.
func (s *server) warmPageCache() {
	if s.assetNoCache {
		return
	}
	for _, name := range pagePaths {
		if _, err := s.compiledPage(name); err != nil {
			panic(fmt.Sprintf("compile %s: %v", name, err))
		}
	}
}

// compiledPage returns the rendered HTML for a ui/*.dopeui page. In embed mode it
// serves from pageCache (populated by warmPageCache, or lazily on first miss);
// in disk mode it recompiles from disk on every call for hot reload.
func (s *server) compiledPage(name string) ([]byte, error) {
	if !s.assetNoCache {
		s.pageMu.Lock()
		body, ok := s.pageCache[name]
		s.pageMu.Unlock()
		if ok {
			return body, nil
		}
	}

	src, err := fs.ReadFile(s.assetSource, name)
	if err != nil {
		return nil, err
	}
	body, err := ui.Compile(name, src)
	if err != nil {
		return nil, err
	}

	if !s.assetNoCache {
		s.pageMu.Lock()
		if s.pageCache == nil {
			s.pageCache = map[string][]byte{}
		}
		s.pageCache[name] = body
		s.pageMu.Unlock()
	}
	return body, nil
}

// servePage compiles and serves a ui/*.dopeui page with asset-ref versioning and
// a strict Content-Security-Policy (see PLAN: XSS = total client compromise,
// so all crypto-bearing pages get a locked-down CSP).
func (s *server) servePage(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := s.compiledPage(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		body = s.versionAssetRefs(body)
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
}

// contentSecurityPolicy locks the page to same-origin scripts only: no inline
// scripts, no eval, no wasm, no third-party origins. This is the real defense
// for client-side crypto (see PLAN §1). worker-src/manifest-src keep the PWA's
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
		body, err := fs.ReadFile(s.assetSource, name)
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

// ---- gzip middleware (ported from dope) ----

var gzipPool = sync.Pool{
	New: func() any {
		gz, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return gz
	},
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz         *gzip.Writer
	headerSent bool
	bypass     bool
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}

func (g *gzipResponseWriter) WriteHeader(status int) {
	if g.headerSent {
		return
	}
	g.headerSent = true
	h := g.Header()
	ct := h.Get("Content-Type")
	compressible := strings.HasPrefix(ct, "text/") ||
		strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "json") ||
		strings.Contains(ct, "css") ||
		strings.Contains(ct, "svg")
	if status < 200 || status == http.StatusNoContent || !compressible || h.Get("Content-Encoding") != "" {
		g.bypass = true
		g.ResponseWriter.WriteHeader(status)
		return
	}
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	h.Del("Content-Length")
	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(g.ResponseWriter)
	g.gz = gz
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.headerSent {
		g.WriteHeader(http.StatusOK)
	}
	if g.bypass || g.gz == nil {
		return g.ResponseWriter.Write(b)
	}
	return g.gz.Write(b)
}

func (g *gzipResponseWriter) finish() {
	if g.gz != nil {
		_ = g.gz.Close()
		gzipPool.Put(g.gz)
		g.gz = nil
	}
}
