// Package webassets serves both apps' static assets: the disk-or-embed source
// switch, content-hash ETags, the cache policy, the concatenated
// core+app stylesheet, the shared fonts, and asset-ref versioning.
//
// It knows nothing about DopeUIKit — the caller passes the core CSS bytes and
// the font FS in, so the UI kit stays a dependency of the apps, not of this
// package.
package webassets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// Config describes where an app's assets come from.
type Config struct {
	// Embedded is the //go:embed FS used when no disk source is found.
	Embedded fs.FS
	// DiskRoots are candidate directories checked (in order) for a "static"
	// subdirectory. The first hit wins and enables disk/dev mode: assets are
	// read live for hot reload and served no-cache.
	DiskRoots []string

	// CoreCSS is the design system's stylesheet, prepended to the app layer.
	CoreCSS []byte
	// CoreCSSDiskPath is where core.css is read from in disk/dev mode, so an
	// edit in the sibling kit checkout hot-reloads. Optional.
	CoreCSSDiskPath string

	// Fonts is the kit's font FS, rooted at "fonts/…".
	Fonts fs.FS
	// FontsDiskRoot is the directory containing "fonts" in disk/dev mode.
	// Optional.
	FontsDiskRoot string
}

// Assets is the resolved asset source plus everything derived from it.
type Assets struct {
	// Source is the asset FS, rooted so "static/…" resolves.
	Source fs.FS
	// Mode is "disk" or "embed".
	Mode string
	// NoCache is true in disk mode: assets are re-read and served no-cache.
	NoCache bool
	// ETags maps a request path ("/static/…") to its strong content-hash ETag.
	// Empty in disk mode.
	ETags map[string]string

	cfg        Config
	stylesheet []byte
}

// New resolves the asset source and precomputes the ETags and the stylesheet.
func New(cfg Config) *Assets {
	a := &Assets{cfg: cfg, Source: cfg.Embedded, Mode: "embed"}
	for _, root := range cfg.DiskRoots {
		if info, err := os.Stat(root + "/static"); err == nil && info.IsDir() {
			a.Source, a.Mode, a.NoCache = os.DirFS(root), "disk", true
			break
		}
	}
	a.stylesheet = a.BuildStylesheet()
	if !a.NoCache {
		a.ETags = buildETags(a.Source)
		// The served /static/styles.css is core+app concatenated, so its ETag
		// (and the ?v= derived from it) must hash the concatenation. Hashing
		// only the app layer would leave a core.css-only change invisible to
		// caches holding an immutable ?v= copy.
		a.ETags["/static/styles.css"] = etag(a.stylesheet)
	}
	return a
}

func etag(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// buildETags hashes every embedded static file, keyed by its request path.
// Embed-mode bytes change only on deploy, so a strong validator lets an expired
// cache entry cost a 304 instead of a full re-download (http.FileServer has no
// Last-Modified for embedded files and would re-send the whole body).
func buildETags(source fs.FS) map[string]string {
	etags := map[string]string{}
	_ = fs.WalkDir(source, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, rerr := fs.ReadFile(source, path)
		if rerr != nil {
			return nil
		}
		etags["/"+path] = etag(b)
		return nil
	})
	return etags
}

// setCachePolicy applies the shared cache headers for a static asset request.
// A "?v=<hash>" request is content-addressed — the HTML shell only emits it for
// the current deploy's bytes — so it can be cached forever; a new deploy changes
// the hash, i.e. the URL. Bare requests get a revalidating policy where
// stale-while-revalidate keeps the revalidation off the critical path.
func (a *Assets) setCachePolicy(w http.ResponseWriter, r *http.Request, path string) {
	if a.NoCache {
		w.Header().Set("Cache-Control", "no-cache")
		return
	}
	if tag := a.ETags[path]; tag != "" {
		w.Header().Set("ETag", tag)
	}
	if strings.Contains(r.URL.RawQuery, "v=") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600, stale-while-revalidate=604800")
	}
}

// FileServer serves the asset FS with the cache policy above. Fonts are
// immutable regardless of versioning.
func (a *Assets) FileServer() http.Handler {
	handler := http.FileServer(http.FS(a.Source))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case a.NoCache:
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(r.URL.Path, "/static/fonts/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			a.setCachePolicy(w, r, r.URL.Path)
		}
		handler.ServeHTTP(w, r)
	})
}

// BuildStylesheet concatenates the design system's core.css ahead of the app's
// own layer. In disk mode core.css is re-read from CoreCSSDiskPath when present,
// so an edit in the sibling kit checkout hot-reloads.
func (a *Assets) BuildStylesheet() []byte {
	core := a.cfg.CoreCSS
	if a.NoCache && a.cfg.CoreCSSDiskPath != "" {
		if b, err := os.ReadFile(a.cfg.CoreCSSDiskPath); err == nil {
			core = b
		}
	}
	layer, _ := fs.ReadFile(a.Source, "static/styles.css")
	out := make([]byte, 0, len(core)+1+len(layer))
	out = append(out, core...)
	out = append(out, '\n')
	return append(out, layer...)
}

// ServeStylesheet serves the concatenated stylesheet, rebuilding it per request
// in disk mode.
func (a *Assets) ServeStylesheet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := a.stylesheet
		if a.NoCache {
			body = a.BuildStylesheet()
		}
		a.setCachePolicy(w, r, "/static/styles.css")
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(body)
	}
}

// ServeFonts serves /static/fonts/* from the kit's font FS (disk in dev, else
// embedded). The fonts are byte-identical across apps and live in the kit.
func (a *Assets) ServeFonts() http.Handler {
	fsys := a.cfg.Fonts
	if a.NoCache && a.cfg.FontsDiskRoot != "" {
		if info, err := os.Stat(a.cfg.FontsDiskRoot + "/fonts"); err == nil && info.IsDir() {
			fsys = os.DirFS(a.cfg.FontsDiskRoot)
		}
	}
	handler := http.StripPrefix("/static/", http.FileServer(http.FS(fsys)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		handler.ServeHTTP(w, r)
	})
}

var assetRefRe = regexp.MustCompile(`(src|href)="(/static/[^"?]+\.(?:js|css))"`)

// VersionRefs rewrites /static/*.js|css refs in an HTML body to carry
// ?v=<hash>, so those requests can be cached immutably.
func (a *Assets) VersionRefs(body []byte) []byte {
	if len(a.ETags) == 0 {
		return body
	}
	return assetRefRe.ReplaceAllFunc(body, func(m []byte) []byte {
		sub := assetRefRe.FindSubmatch(m)
		path := string(sub[2])
		tag := strings.Trim(a.ETags[path], `"`)
		if tag == "" {
			return m
		}
		return []byte(fmt.Sprintf(`%s="%s?v=%s"`, sub[1], path, tag))
	})
}
