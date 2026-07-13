package server

import (
	"strings"
	"testing"
)

// TestPagesServe covers every page route servePage handles (compiled from
// ui/*.dopeui): each responds 200 with an HTML body, regardless of login state
// (the server never gates these — the client-side JS redirects when
// unauthenticated).
func TestPagesServe(t *testing.T) {
	ts, _ := newTestServer(t)
	anon := &apiClient{t: t, base: ts.URL}

	for _, path := range []string{
		"/login", "/register", "/profile", "/profile/tokens", "/import", "/board/1", "/",
	} {
		resp := anon.do("GET", path, nil)
		mustStatus(t, resp, 200)
		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type = %q, want text/html", path, ct)
		}
		b := body(t, resp)
		if !strings.Contains(b, "<!doctype html>") {
			t.Errorf("%s: body missing doctype", path)
		}
	}
}
