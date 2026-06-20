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

// servePage serves a static HTML file from the asset source with asset-ref
// versioning and a strict Content-Security-Policy (see PLAN: XSS = total client
// compromise, so all crypto-bearing pages get a locked-down CSP).
func (s *server) servePage(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := fs.ReadFile(s.assetSource, name)
		if err != nil {
			http.NotFound(w, r)
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
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"worker-src 'self'; " +
	"manifest-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
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
