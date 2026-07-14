package tests

import (
	dopeserver "dope/dope/server"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"dope/dope/platform/session"
)

// makeAdminUser inserts a user with the given username and returns a session
// cookie for it.
func makeUserWithSession(t *testing.T, srv *dopeserver.Server, username string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.Eng().DB.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 0, ?, ?)`, username, now, now)
	if err != nil {
		t.Fatalf("insert user %q: %v", username, err)
	}
	userID, _ := res.LastInsertId()
	return createTestSession(t, srv, userID)
}

func TestAdminCreateUsersHappyPath(t *testing.T) {
	t.Setenv("DOPE_ADMIN_USER", "pecheny")
	srv := newAuthTestServer(t)
	cookie := makeUserWithSession(t, srv, "pecheny")

	form := url.Values{"usernames": {"anton\nanya_a\n\n anton \ndasha"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/create_users", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	resp := httptest.NewRecorder()
	srv.PageServer().HandleAdminCreateUsers(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, name := range []string{"anton", "anya_a", "dasha"} {
		if !strings.Contains(body, name) {
			t.Fatalf("created username %q missing from page: %s", name, body)
		}
	}

	// Three distinct users created (the duplicate "anton" line is collapsed).
	var count int
	if err := srv.Eng().DB.QueryRow(
		`select count(*) from users where username in ('anton','anya_a','dasha')`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("created %d users, want 3", count)
	}

	// Every created user must be able to log in with the password shown on the
	// page, and the stored hash must be bcrypt (password_salt null).
	for _, name := range []string{"anton", "anya_a", "dasha"} {
		password := passwordFromPage(t, body, name)
		var hash, salt *string
		if err := srv.Eng().DB.QueryRow(
			`select password_hash, password_salt from users where username = ?`, name).Scan(&hash, &salt); err != nil {
			t.Fatalf("load %q: %v", name, err)
		}
		if salt != nil {
			t.Fatalf("%q: password_salt should be null for bcrypt", name)
		}
		ok, _, err := dopeserver.VerifyPassword(*hash, "", password)
		if err != nil || !ok {
			t.Fatalf("%q: password from page does not verify (ok=%v err=%v)", name, ok, err)
		}
	}
}

func TestAdminCreateUsersSkipsExisting(t *testing.T) {
	t.Setenv("DOPE_ADMIN_USER", "pecheny")
	srv := newAuthTestServer(t)
	cookie := makeUserWithSession(t, srv, "pecheny")
	// Pre-create "anton".
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := srv.Eng().DB.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, 'anton', 0, ?, ?)`, now, now); err != nil {
		t.Fatalf("seed anton: %v", err)
	}

	form := url.Values{"usernames": {"anton\nnikita\nx!bad"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/create_users", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	resp := httptest.NewRecorder()
	srv.PageServer().HandleAdminCreateUsers(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, "пропущены") || !strings.Contains(body, "anton") {
		t.Fatalf("expected anton reported as skipped: %s", body)
	}
	if !strings.Contains(body, "недопустимый") {
		t.Fatalf("expected x!bad reported as invalid: %s", body)
	}
	var antonCount int
	if err := srv.Eng().DB.QueryRow(`select count(*) from users where username = 'anton'`).Scan(&antonCount); err != nil {
		t.Fatalf("count anton: %v", err)
	}
	if antonCount != 1 {
		t.Fatalf("anton count = %d, want 1 (not duplicated)", antonCount)
	}
}

func TestAdminPagesRejectNonAdmin(t *testing.T) {
	t.Setenv("DOPE_ADMIN_USER", "pecheny")
	srv := newAuthTestServer(t)

	// Logged out → redirect to /login.
	for _, path := range []string{"/admin", "/admin/create_users"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		if path == "/admin" {
			srv.PageServer().HandleAdminLanding(resp, req)
		} else {
			srv.PageServer().HandleAdminCreateUsers(resp, req)
		}
		if resp.Code != http.StatusSeeOther {
			t.Fatalf("%s logged-out status = %d, want 303", path, resp.Code)
		}
	}

	// Logged in as a non-admin → 404 (existence hidden).
	cookie := makeUserWithSession(t, srv, "someone_else")
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	resp := httptest.NewRecorder()
	srv.PageServer().HandleAdminLanding(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("non-admin status = %d, want 404", resp.Code)
	}

	// Non-admin POST must not create users either.
	form := url.Values{"usernames": {"sneaky"}}
	postReq := httptest.NewRequest(http.MethodPost, "/admin/create_users", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	postResp := httptest.NewRecorder()
	srv.PageServer().HandleAdminCreateUsers(postResp, postReq)
	if postResp.Code != http.StatusNotFound {
		t.Fatalf("non-admin POST status = %d, want 404", postResp.Code)
	}
	var sneaky int
	if err := srv.Eng().DB.QueryRow(`select count(*) from users where username = 'sneaky'`).Scan(&sneaky); err != nil {
		t.Fatalf("count sneaky: %v", err)
	}
	if sneaky != 0 {
		t.Fatalf("non-admin created a user; count = %d", sneaky)
	}
}

// passwordFromPage extracts the generated password rendered next to a username in
// the results table: the username cell (<td>name</td>) followed by the adjacent
// password cell's <code>PASSWORD</code>. It is whitespace-tolerant because the DSL
// renderer lays each table cell on its own line, so the two cells are no longer
// byte-adjacent.
func passwordFromPage(t *testing.T, body, username string) string {
	t.Helper()
	i := strings.Index(body, ">"+username+"<")
	if i < 0 {
		t.Fatalf("username %q row not found in page", username)
	}
	rest := body[i:]
	open := strings.Index(rest, "<code>")
	if open < 0 {
		t.Fatalf("password cell for %q not found", username)
	}
	rest = rest[open+len("<code>"):]
	end := strings.Index(rest, "</code>")
	if end < 0 {
		t.Fatalf("password cell for %q not closed", username)
	}
	return rest[:end]
}
