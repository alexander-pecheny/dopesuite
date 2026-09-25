package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	dopeserver "dope/dope/server"

	"pecheny.me/dopecore/session"
)

func seedPasswordUser(t *testing.T, srv *dopeserver.Server, username, password string) int64 {
	t.Helper()
	hash, err := dopeserver.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.Eng().DB.Exec(`
insert into users(username, password_hash, is_system, created_at, updated_at)
values(?, ?, 0, ?, ?)`, username, hash, now, now)
	if err != nil {
		t.Fatalf("seed %q: %v", username, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func postForm(path string, form url.Values, cookie string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	}
	return req
}

var resetLinkRe = regexp.MustCompile(`https?://[^\s<"]+/reset_password\?token=[A-Z0-9]+`)

func adminMakesResetLink(t *testing.T, srv *dopeserver.Server, adminCookie, username string) string {
	t.Helper()
	resp := httptest.NewRecorder()
	srv.PageServer().HandleAdminPasswordReset(resp, postForm("/admin/password_reset", url.Values{"username": {username}}, adminCookie))
	if resp.Code != http.StatusOK {
		t.Fatalf("make link: status %d, body %s", resp.Code, resp.Body.String())
	}
	link := resetLinkRe.FindString(html.UnescapeString(resp.Body.String()))
	if link == "" {
		t.Fatalf("no reset link on page: %s", resp.Body.String())
	}
	return link
}

func loginStatus(t *testing.T, srv *dopeserver.Server, username, password string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp := httptest.NewRecorder()
	srv.HandleAuthLoginPassword(resp, httptest.NewRequest(http.MethodPost, "/api/auth/login-password", bytes.NewReader(body)))
	return resp.Code
}

func TestPasswordResetLinkSetsANewPasswordOnce(t *testing.T) {
	t.Setenv("DOPE_ADMIN_USER", "pecheny")
	srv := newAuthTestServer(t)
	adminCookie := makeUserWithSession(t, srv, "pecheny")
	userID := seedPasswordUser(t, srv, "anton", "forgotten-password")
	oldSession := createTestSession(t, srv, userID)

	// The admin types the name in another case; the link is still anton's.
	link := adminMakesResetLink(t, srv, adminCookie, "Anton")
	u, _ := url.Parse(link)
	token := u.Query().Get("token")

	get := httptest.NewRecorder()
	srv.PageServer().HandleResetPassword(get, httptest.NewRequest(http.MethodGet, "/reset_password?"+u.RawQuery, nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "anton") || !strings.Contains(get.Body.String(), `name="new_password"`) {
		t.Fatalf("reset form: status %d, body %s", get.Code, get.Body.String())
	}

	mismatch := httptest.NewRecorder()
	srv.PageServer().HandleResetPassword(mismatch, postForm("/reset_password", url.Values{
		"token": {token}, "new_password": {"brand-new-pass"}, "confirm_password": {"something-else"}}, ""))
	if mismatch.Code != http.StatusOK || loginStatus(t, srv, "anton", "forgotten-password") != http.StatusOK {
		t.Fatalf("a mismatch must change nothing: status %d", mismatch.Code)
	}

	set := httptest.NewRecorder()
	srv.PageServer().HandleResetPassword(set, postForm("/reset_password", url.Values{
		"token": {token}, "new_password": {"brand-new-pass"}, "confirm_password": {"brand-new-pass"}}, ""))
	if set.Code != http.StatusSeeOther {
		t.Fatalf("set: status %d, body %s", set.Code, set.Body.String())
	}
	newSession := sessionCookieFromHeader(t, set)
	if _, ok := srv.Eng().LookupSession(withCookie(newSession)); !ok {
		t.Fatalf("the reset should log the user in")
	}
	if _, ok := srv.Eng().LookupSession(withCookie(oldSession)); ok {
		t.Fatalf("the reset should end the older sessions")
	}
	if got := loginStatus(t, srv, "anton", "brand-new-pass"); got != http.StatusOK {
		t.Fatalf("login with the new password: %d", got)
	}
	if got := loginStatus(t, srv, "anton", "forgotten-password"); got != http.StatusUnauthorized {
		t.Fatalf("login with the old password: %d, want 401", got)
	}

	again := httptest.NewRecorder()
	srv.PageServer().HandleResetPassword(again, postForm("/reset_password", url.Values{
		"token": {token}, "new_password": {"third-password"}, "confirm_password": {"third-password"}}, ""))
	if again.Code != http.StatusOK || strings.Contains(again.Body.String(), `name="new_password"`) {
		t.Fatalf("a used link must not show the form again: status %d", again.Code)
	}
	if got := loginStatus(t, srv, "anton", "brand-new-pass"); got != http.StatusOK {
		t.Fatalf("a used link must not change the password: %d", got)
	}
}

func TestPasswordResetNewLinkRetiresTheOldOne(t *testing.T) {
	t.Setenv("DOPE_ADMIN_USER", "pecheny")
	srv := newAuthTestServer(t)
	adminCookie := makeUserWithSession(t, srv, "pecheny")
	seedPasswordUser(t, srv, "anton", "forgotten-password")
	first, _ := url.Parse(adminMakesResetLink(t, srv, adminCookie, "anton"))
	adminMakesResetLink(t, srv, adminCookie, "anton")

	resp := httptest.NewRecorder()
	srv.PageServer().HandleResetPassword(resp, httptest.NewRequest(http.MethodGet, "/reset_password?"+first.RawQuery, nil))
	if strings.Contains(resp.Body.String(), `name="new_password"`) {
		t.Fatalf("the first link should stop working once a second is made")
	}
}

func TestPasswordResetExpiredLinkDoesNotWork(t *testing.T) {
	t.Setenv("DOPE_ADMIN_USER", "pecheny")
	srv := newAuthTestServer(t)
	adminCookie := makeUserWithSession(t, srv, "pecheny")
	seedPasswordUser(t, srv, "anton", "forgotten-password")
	u, _ := url.Parse(adminMakesResetLink(t, srv, adminCookie, "anton"))
	if _, err := srv.Eng().DB.Exec(`update password_resets set expires_at = ?`,
		time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("expire: %v", err)
	}
	resp := httptest.NewRecorder()
	srv.PageServer().HandleResetPassword(resp, postForm("/reset_password", url.Values{
		"token": {u.Query().Get("token")}, "new_password": {"brand-new-pass"}, "confirm_password": {"brand-new-pass"}}, ""))
	if resp.Code != http.StatusOK || loginStatus(t, srv, "anton", "forgotten-password") != http.StatusOK {
		t.Fatalf("an expired link must change nothing: status %d", resp.Code)
	}
}

func TestPasswordResetIsAdminOnly(t *testing.T) {
	t.Setenv("DOPE_ADMIN_USER", "pecheny")
	srv := newAuthTestServer(t)
	cookie := makeUserWithSession(t, srv, "someone")
	seedPasswordUser(t, srv, "anton", "forgotten-password")
	resp := httptest.NewRecorder()
	srv.PageServer().HandleAdminPasswordReset(resp, postForm("/admin/password_reset", url.Values{"username": {"anton"}}, cookie))
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
	var n int
	if err := srv.Eng().DB.QueryRow(`select count(*) from password_resets`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("resets = %d (%v), want none", n, err)
	}
}

func TestPasswordResetUnknownUser(t *testing.T) {
	t.Setenv("DOPE_ADMIN_USER", "pecheny")
	srv := newAuthTestServer(t)
	adminCookie := makeUserWithSession(t, srv, "pecheny")
	resp := httptest.NewRecorder()
	srv.PageServer().HandleAdminPasswordReset(resp, postForm("/admin/password_reset", url.Values{"username": {"nobody"}}, adminCookie))
	if resp.Code != http.StatusOK || resetLinkRe.MatchString(resp.Body.String()) || !strings.Contains(resp.Body.String(), "nobody") {
		t.Fatalf("status %d, body %s", resp.Code, resp.Body.String())
	}
}

func TestPasswordLoginIgnoresCase(t *testing.T) {
	srv := newAuthTestServer(t)
	seedPasswordUser(t, srv, "anton", "s3cretpassword")
	if got := loginStatus(t, srv, "Anton", "s3cretpassword"); got != http.StatusOK {
		t.Fatalf("Anton: %d, want 200", got)
	}
	// The database refuses a second account whose name differs only by case.
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := srv.Eng().DB.Exec(`
insert into users(username, is_system, created_at, updated_at) values('ANTON', 0, ?, ?)`, now, now); err == nil {
		t.Fatalf("ANTON was accepted next to anton")
	}
}

func TestSiteAdminReadsAndEditsAPrivateFestWithoutARole(t *testing.T) {
	t.Setenv("DOPE_ADMIN_USER", "pecheny")
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	if _, err := srv.Eng().DB.Exec(`update fests set is_public = 0 where id = ?`, festID); err != nil {
		t.Fatalf("make private: %v", err)
	}
	gamePath := fmt.Sprintf("/api/fest/%d/games/%d", festID, gameID)

	_, strangerToken := createAPITestSession(t, srv, "stranger")
	if got := scopedAPIRequest(t, srv, http.MethodGet, gamePath, nil, strangerToken).Code; got != http.StatusNotFound {
		t.Fatalf("stranger read = %d, want 404", got)
	}
	_, adminToken := createAPITestSession(t, srv, "pecheny")
	if got := scopedAPIRequest(t, srv, http.MethodGet, gamePath, nil, adminToken); got.Code != http.StatusOK {
		t.Fatalf("admin read = %d, body %s", got.Code, got.Body.String())
	}
	updatePath := matchStatePath(festID, gameID, dopeserver.DefaultMatchCode)
	payload := markBody(t, srv, festID, dopeserver.DefaultMatchCode, 0, 0, 0, "right")
	if got := scopedAPIRequest(t, srv, http.MethodPatch, updatePath, payload, adminToken); got.Code != http.StatusOK {
		t.Fatalf("admin write = %d, body %s", got.Code, got.Body.String())
	}
}

func withCookie(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	return req
}
