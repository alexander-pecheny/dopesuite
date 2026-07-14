package webassets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testAssets(t *testing.T) *Assets {
	t.Helper()
	return New(Config{
		Embedded: fstest.MapFS{
			"static/host.js":    &fstest.MapFile{Data: []byte("// host")},
			"static/styles.css": &fstest.MapFile{Data: []byte(".app{}")},
		},
		CoreCSS: []byte(".core{}"),
	})
}

func TestFileServerCachePolicy(t *testing.T) {
	h := testAssets(t).FileServer()
	serve := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	// A content-addressed "?v=" request is cached forever.
	versioned := serve("/static/host.js?v=abc")
	if versioned.Code != http.StatusOK {
		t.Fatalf("versioned status = %d", versioned.Code)
	}
	if cc := versioned.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("versioned Cache-Control = %q, want immutable", cc)
	}

	// A bare request keeps the revalidating policy and carries the ETag.
	bare := serve("/static/host.js")
	if cc := bare.Header().Get("Cache-Control"); !strings.Contains(cc, "stale-while-revalidate") {
		t.Fatalf("bare Cache-Control = %q, want stale-while-revalidate", cc)
	}
	if et := bare.Header().Get("ETag"); et == "" {
		t.Fatal("bare request has no ETag")
	}
}

func TestStylesheetIsCorePlusAppLayer(t *testing.T) {
	a := testAssets(t)
	got := string(a.BuildStylesheet())
	if want := ".core{}\n.app{}"; got != want {
		t.Fatalf("stylesheet = %q, want %q", got, want)
	}
	// The served stylesheet is the concatenation, so its ETag must hash the
	// concatenation — hashing only the app layer would leave a core-only change
	// invisible to caches holding an immutable ?v= copy.
	if a.ETags["/static/styles.css"] != etag(a.BuildStylesheet()) {
		t.Fatal("styles.css ETag does not hash the concatenated stylesheet")
	}
}

func TestVersionRefs(t *testing.T) {
	a := testAssets(t)
	in := []byte(`<link href="/static/styles.css"><script src="/static/host.js"></script>` +
		`<script src="/static/host.js?v=old"></script>`)
	out := string(a.VersionRefs(in))
	if !strings.Contains(out, `href="/static/styles.css?v=`) {
		t.Fatalf("stylesheet ref not versioned: %s", out)
	}
	if !strings.Contains(out, `src="/static/host.js?v=`) {
		t.Fatalf("script ref not versioned: %s", out)
	}
	if strings.Contains(out, `?v=old?v=`) {
		t.Fatalf("already-versioned URL should be left as-is: %s", out)
	}

	// Disk mode has no ETags, so versioning is a no-op.
	disk := &Assets{}
	if got := string(disk.VersionRefs(in)); got != string(in) {
		t.Fatal("disk-mode VersionRefs should be a no-op")
	}
}

func TestGzipSkipsAndCompresses(t *testing.T) {
	body := strings.Repeat("hello ", 200)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	})
	h := Gzip(next, "/events")

	req := httptest.NewRequest(http.MethodGet, "/page", nil)
	req.Header.Set("Accept-Encoding", "br, gzip;q=0.9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("gzippable response was not compressed")
	}
	if rec.Body.Len() >= len(body) {
		t.Fatal("gzip did not shrink the body")
	}

	// SSE is flushed incrementally; gzip would break the framing.
	sse := httptest.NewRequest(http.MethodGet, "/events", nil)
	sse.Header.Set("Accept-Encoding", "gzip")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, sse)
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("/events must not be gzipped")
	}
}
