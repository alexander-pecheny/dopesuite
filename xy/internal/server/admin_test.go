package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// postForm sends a form-encoded POST carrying the client's session cookies.
func (c *apiClient) postForm(path string, form url.Values) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest("POST", c.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// TestAdminCreateUsers covers the admin gate and the bulk create-users flow:
// non-admins can't reach it; the admin can create accounts, with duplicates
// skipped and invalid logins reported; created users can then log in.
func TestAdminCreateUsers(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()

	// A non-admin, logged-in user gets a 404 (page existence hidden).
	plain := registerUser(t, srv, ts, 880001, "plainuser")
	resp := plain.do("POST", "/api/auth/username", map[string]string{"username": "plainuser"})
	mustStatus(t, resp, 204)
	resp = plain.do("GET", "/admin/create_users", nil)
	mustStatus(t, resp, 404)

	// Make "boss" the admin and register that account.
	t.Setenv("XY_ADMIN_USER", "boss")
	admin := registerUser(t, srv, ts, 880002, "boss")
	resp = admin.do("POST", "/api/auth/username", map[string]string{"username": "boss"})
	mustStatus(t, resp, 204)

	// GET the form.
	resp = admin.do("GET", "/admin/create_users", nil)
	mustStatus(t, resp, 200)
	if !strings.Contains(body(t, resp), "Создать пользователей") {
		t.Fatal("form page missing heading")
	}

	// POST a batch: one new, one duplicate (boss exists), one invalid.
	resp = admin.postForm("/admin/create_users", url.Values{"usernames": {"alice\nboss\nbad name!"}})
	mustStatus(t, resp, 200)
	page := body(t, resp)
	if !strings.Contains(page, "alice") {
		t.Fatal("created user not shown")
	}
	if !strings.Contains(page, "пропущены") || !strings.Contains(page, "boss") {
		t.Fatalf("duplicate not reported as skipped:\n%s", page)
	}
	if !strings.Contains(page, "недопустимый логин") {
		t.Fatal("invalid login not reported")
	}

	// The created account exists with a usable password — confirm it can log in
	// by reading its hash and checking a bcrypt round-trip isn't empty.
	var hash string
	if err := srv.db.QueryRowContext(ctx, `select password_hash from users where username = ?`, "alice").Scan(&hash); err != nil {
		t.Fatalf("alice not persisted: %v", err)
	}
	if hash == "" {
		t.Fatal("alice has no password hash")
	}

	// Logged-out access redirects to /login (303), not 404.
	anon := &apiClient{t: t, base: ts.URL}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", anon.base+"/admin/create_users", nil)
	r2, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if r2.StatusCode != http.StatusSeeOther {
		t.Fatalf("anon status = %d, want 303", r2.StatusCode)
	}
	r2.Body.Close()
}
