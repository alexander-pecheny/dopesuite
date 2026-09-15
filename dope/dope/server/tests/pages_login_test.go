package tests

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestLoginStampsNext: /login?next=<path> sends the visitor back where they came
// from, and only when the path is one of ours — the shared login script reads
// the destination off data-login-redirect, so a destination it should not have
// is an open redirect. The rule itself is kit.SafeLoginRedirect's test; this is
// the wiring.
func TestLoginStampsNext(t *testing.T) {
	srv := newAuthTestServer(t)
	page := func(query string) string {
		t.Helper()
		resp := httptest.NewRecorder()
		srv.HandleLogin(resp, httptest.NewRequest(http.MethodGet, "/login"+query, nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d for %q", resp.Code, query)
		}
		return resp.Body.String()
	}
	for _, next := range []string{"/fest/spring", "/host/1"} {
		if got := page("?next=" + url.QueryEscape(next)); !strings.Contains(got, `data-login-redirect="`+next+`"`) {
			t.Errorf("login page did not carry %q", next)
		}
	}
	for _, bad := range []string{"https://evil.example", "//evil.example", `/x" data-login-redirect="/y`} {
		if got := page("?next=" + url.QueryEscape(bad)); !strings.Contains(got, `data-login-redirect="/host"`) {
			t.Errorf("next=%q was honoured, want dope's own destination", bad)
		}
	}
	// No `next` at all is the page compiled at startup, /host and all.
	if got := page(""); !strings.Contains(got, `data-login-redirect="/host"`) {
		t.Error("the plain login page lost its default destination")
	}
}
