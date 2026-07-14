package webassets

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// gzipPool recycles gzip.Writer instances. Each writer holds a ~64KB internal
// buffer, so pooling cuts allocations sharply under concurrent fetches.
// BestSpeed is the sweet spot for low-CPU VPS hosting: still ~3x shrink on JSON,
// a fraction of the CPU of DefaultCompression.
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
	bypass     bool // write straight through, never gzip
}

// Gzip compresses text-ish responses. Requests whose path is in skipPaths are
// passed through untouched — SSE responses are flushed incrementally and gzip
// would buffer them, breaking the framing the client expects.
func Gzip(next http.Handler, skipPaths ...string) http.Handler {
	skip := make(map[string]bool, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skip[r.URL.Path] || !acceptsGzip(r) {
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
	if !shouldGzip(status, h) {
		g.bypass = true
		g.ResponseWriter.WriteHeader(status)
		return
	}
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	h.Del("Content-Length") // no longer accurate
	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(g.ResponseWriter)
	g.gz = gz
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.headerSent {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.bypass || g.gz == nil {
		return g.ResponseWriter.Write(b)
	}
	return g.gz.Write(b)
}

func (g *gzipResponseWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipResponseWriter) finish() {
	if g.gz == nil {
		return
	}
	_ = g.gz.Close()
	g.gz.Reset(io.Discard)
	gzipPool.Put(g.gz)
	g.gz = nil
}

// GzipBytes compresses raw through the shared pool, at the same level the Gzip
// middleware uses. For callers that pre-compress a cached body once instead of
// per response.
func GzipBytes(raw []byte) []byte {
	var buf bytes.Buffer
	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(&buf)
	_, _ = gz.Write(raw)
	_ = gz.Close()
	gz.Reset(io.Discard)
	gzipPool.Put(gz)
	return buf.Bytes()
}

// AcceptsGzip reports whether the request's Accept-Encoding names gzip. Exported
// for handlers that serve a pre-compressed body themselves.
func AcceptsGzip(r *http.Request) bool { return acceptsGzip(r) }

func acceptsGzip(r *http.Request) bool {
	enc := r.Header.Get("Accept-Encoding")
	if enc == "" {
		return false
	}
	for _, v := range strings.Split(enc, ",") {
		token := strings.TrimSpace(v)
		if i := strings.IndexByte(token, ';'); i >= 0 {
			token = token[:i]
		}
		if strings.EqualFold(token, "gzip") {
			return true
		}
	}
	return false
}

func shouldGzip(status int, h http.Header) bool {
	if status < 200 || status == http.StatusNoContent || status == http.StatusNotModified {
		return false
	}
	if h.Get("Content-Encoding") != "" {
		return false
	}
	return gzippableType(h.Get("Content-Type"))
}

func gzippableType(ct string) bool {
	if ct == "" {
		// Not known yet — let it through; DetectContentType will pick a
		// text-ish type for most handler payloads.
		return true
	}
	base := ct
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		base = ct[:i]
	}
	base = strings.TrimSpace(strings.ToLower(base))
	if base == "text/event-stream" {
		return false
	}
	switch {
	case strings.HasPrefix(base, "text/"),
		base == "application/json",
		base == "application/javascript",
		base == "application/xml",
		base == "image/svg+xml":
		return true
	}
	return false
}
