package server

import (
	"net/url"
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
		"/login", "/profile", "/profile/tokens", "/import", "/board/1", "/",
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

// TestLoginStampsNext: the accepted destination reaches the page as the
// attribute the shared login script reads, and a refused one leaves the default.
func TestLoginStampsNext(t *testing.T) {
	ts, _ := newTestServer(t)
	c := &apiClient{t: t, base: ts.URL}
	page := func(path string) string {
		t.Helper()
		resp := c.do("GET", path, nil)
		mustStatus(t, resp, 200)
		return body(t, resp)
	}
	for _, next := range []string{"/join/ABC123", "/board/42"} {
		if got := page("/login?next=" + url.QueryEscape(next)); !strings.Contains(got, `data-login-redirect="`+next+`"`) {
			t.Errorf("login page did not carry %q", next)
		}
	}
	for _, bad := range []string{"https://evil.example", "//evil.example", `/x" data-login-redirect="/y`} {
		if got := page("/login?next=" + url.QueryEscape(bad)); !strings.Contains(got, `data-login-redirect="/"`) {
			t.Errorf("next=%q was honoured, want the default destination", bad)
		}
	}
	// No `next` at all is the page compiled at startup, default destination and all.
	if got := page("/login"); !strings.Contains(got, `data-login-redirect="/"`) {
		t.Error("the plain login page lost its default destination")
	}
}
