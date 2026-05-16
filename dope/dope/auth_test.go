package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newAuthTestServer(t *testing.T) *server {
	t.Helper()
	db, err := openTournamentDB(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := bootstrapDefaultTournament(db, defaultMatch()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return &server{db: db, subscribers: make(map[chan event]struct{})}
}

func systemUserID(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`select id from users where is_system = 1`).Scan(&id); err != nil {
		t.Fatalf("system user: %v", err)
	}
	return id
}

func botConsumeRegister(t *testing.T, db *sql.DB, code string, tgUserID int64, tgUsername string) {
	t.Helper()
	res, err := db.Exec(`
update telegram_login_codes
set telegram_user_id = ?, telegram_username = ?, consumed_at = ?
where code = ? and kind = 'register' and consumed_at is null`,
		tgUserID, tgUsername, time.Now().UTC().Format(time.RFC3339), code)
	if err != nil {
		t.Fatalf("bot consume: %v", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		t.Fatalf("bot consume rows = %d, want 1", n)
	}
}

func botIssueLoginCode(t *testing.T, db *sql.DB, userID, tgUserID int64, tgUsername string) string {
	t.Helper()
	code, err := newTelegramLoginCode()
	if err != nil {
		t.Fatalf("new code: %v", err)
	}
	now := time.Now().UTC()
	_, err = db.Exec(`
insert into telegram_login_codes(code, kind, user_id, telegram_user_id, telegram_username, created_at, expires_at)
values(?, 'login', ?, ?, ?, ?, ?)`,
		code, userID, tgUserID, tgUsername, now.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("issue login: %v", err)
	}
	return code
}

func isShortLoginCode(s string) bool {
	if len(s) != telegramLoginCodeLen {
		return false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func sessionCookieFromHeader(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, raw := range w.Result().Header.Values("Set-Cookie") {
		req := http.Request{Header: http.Header{"Cookie": []string{raw}}}
		if c, err := req.Cookie(sessionCookieName); err == nil && c.Value != "" {
			return c.Value
		}
	}
	t.Fatalf("no session cookie in response")
	return ""
}

func createTestSession(t *testing.T, srv *server, userID int64) string {
	t.Helper()
	tx, err := srv.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin session tx: %v", err)
	}
	defer tx.Rollback()
	token, err := createSessionTx(context.Background(), tx, userID, time.Now().UTC())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit session: %v", err)
	}
	return token
}

func decodeJSON[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	return out
}

func TestProfilePageAndLogout(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 0, ?, ?)`, "profile_user", now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()
	cookie := createTestSession(t, srv, userID)

	hostReq := httptest.NewRequest(http.MethodGet, "/host", nil)
	hostReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	hostResp := httptest.NewRecorder()
	srv.handleHostLanding(hostResp, hostReq)
	if hostResp.Code != http.StatusOK {
		t.Fatalf("host: status %d, body %s", hostResp.Code, hostResp.Body.String())
	}
	if body := hostResp.Body.String(); !strings.Contains(body, `href="/profile"`) || !strings.Contains(body, "profile_user") {
		t.Fatalf("host page missing profile link/user: %s", body)
	}

	profileReq := httptest.NewRequest(http.MethodGet, "/profile", nil)
	profileReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	profileResp := httptest.NewRecorder()
	srv.handleProfilePage(profileResp, profileReq)
	if profileResp.Code != http.StatusOK {
		t.Fatalf("profile: status %d, body %s", profileResp.Code, profileResp.Body.String())
	}
	if body := profileResp.Body.String(); !strings.Contains(body, `action="/profile/logout"`) || !strings.Contains(body, "Разлогиниться") {
		t.Fatalf("profile page missing logout form: %s", body)
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/profile/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	logoutResp := httptest.NewRecorder()
	srv.handleProfileLogout(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusSeeOther {
		t.Fatalf("profile logout status = %d, want 303", logoutResp.Code)
	}
	if got := logoutResp.Header().Get("Location"); got != "/host" {
		t.Fatalf("profile logout location = %q, want /host", got)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	meResp := httptest.NewRecorder()
	srv.handleAuthMe(meResp, meReq)
	if meResp.Code != http.StatusUnauthorized {
		t.Fatalf("me after profile logout = %d, want 401", meResp.Code)
	}
}

func TestRegisterRedirectsCompletedUserToHost(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 0, ?, ?)`, "done_user", now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()
	cookie := createTestSession(t, srv, userID)

	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp := httptest.NewRecorder()
	srv.handleRegisterPage(resp, req)
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("register status = %d, want 303", resp.Code)
	}
	if got := resp.Header().Get("Location"); got != "/host" {
		t.Fatalf("register redirect = %q, want /host", got)
	}
}

func TestRegisterFlowHappyPath(t *testing.T) {
	srv := newAuthTestServer(t)
	systemID := systemUserID(t, srv.db)
	invite, err := createInvite(context.Background(), srv.db, systemID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// Step 1: site issues register code.
	startBody, _ := json.Marshal(startRegisterRequest{InviteCode: invite})
	startReq := httptest.NewRequest(http.MethodPost, "/api/auth/register/start", bytes.NewReader(startBody))
	startResp := httptest.NewRecorder()
	srv.handleAuthRegisterStart(startResp, startReq)
	if startResp.Code != http.StatusOK {
		t.Fatalf("start: status %d, body %s", startResp.Code, startResp.Body.String())
	}
	startData := decodeJSON[startRegisterResponse](t, startResp)
	if startData.Code == "" {
		t.Fatalf("start: empty code")
	}

	// Polling before bot consumes returns pending.
	pendingReq := httptest.NewRequest(http.MethodGet, "/api/auth/register/status?code="+startData.Code, nil)
	pendingResp := httptest.NewRecorder()
	srv.handleAuthRegisterStatus(pendingResp, pendingReq)
	if pendingResp.Code != http.StatusOK {
		t.Fatalf("pending status: %d", pendingResp.Code)
	}
	if got := decodeJSON[registerStatusResponse](t, pendingResp).Status; got != "pending" {
		t.Fatalf("status before consume = %q, want pending", got)
	}

	// Step 2: bot consumes the register code.
	botConsumeRegister(t, srv.db, startData.Code, 555, "tg_alice")

	// Step 3: site polls again, gets ready + session cookie + no username yet.
	readyReq := httptest.NewRequest(http.MethodGet, "/api/auth/register/status?code="+startData.Code, nil)
	readyResp := httptest.NewRecorder()
	srv.handleAuthRegisterStatus(readyResp, readyReq)
	if readyResp.Code != http.StatusOK {
		t.Fatalf("ready status: %d, body %s", readyResp.Code, readyResp.Body.String())
	}
	ready := decodeJSON[registerStatusResponse](t, readyResp)
	if ready.Status != "ready" {
		t.Fatalf("status = %q, want ready", ready.Status)
	}
	if ready.Username != nil {
		t.Fatalf("username should be nil after register, got %q", *ready.Username)
	}
	cookie := sessionCookieFromHeader(t, readyResp)

	// Invite is now used.
	var usedBy sql.NullInt64
	if err := srv.db.QueryRow(`select used_by from invites where code = ?`, invite).Scan(&usedBy); err != nil {
		t.Fatalf("invite check: %v", err)
	}
	if !usedBy.Valid {
		t.Fatalf("invite still unused after register")
	}

	// Step 4: set username.
	usernameBody, _ := json.Marshal(usernameRequest{Username: "alice"})
	usernameReq := httptest.NewRequest(http.MethodPost, "/api/auth/username", bytes.NewReader(usernameBody))
	usernameReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	usernameResp := httptest.NewRecorder()
	srv.handleAuthUsername(usernameResp, usernameReq)
	if usernameResp.Code != http.StatusOK {
		t.Fatalf("username: status %d, body %s", usernameResp.Code, usernameResp.Body.String())
	}

	// Username is now immutable: second attempt fails.
	rejectReq := httptest.NewRequest(http.MethodPost, "/api/auth/username", bytes.NewReader(usernameBody))
	rejectReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	rejectResp := httptest.NewRecorder()
	srv.handleAuthUsername(rejectResp, rejectReq)
	if rejectResp.Code != http.StatusConflict {
		t.Fatalf("second username attempt: status %d, want 409", rejectResp.Code)
	}

	// /api/auth/me reflects new state.
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	meResp := httptest.NewRecorder()
	srv.handleAuthMe(meResp, meReq)
	if meResp.Code != http.StatusOK {
		t.Fatalf("me: status %d", meResp.Code)
	}
	me := decodeJSON[meResponse](t, meResp)
	if me.Username == nil || *me.Username != "alice" {
		t.Fatalf("me.username = %v, want alice", me.Username)
	}
}

func TestRegisterRejectsExpiredInvite(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC()
	if _, err := srv.db.Exec(`
insert into invites(code, created_by, created_at, expires_at)
values('OLD', ?, ?, ?)`, systemUserID(t, srv.db), now.Add(-2*time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatalf("seed invite: %v", err)
	}
	body, _ := json.Marshal(startRegisterRequest{InviteCode: "OLD"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register/start", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	srv.handleAuthRegisterStart(resp, req)
	if resp.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", resp.Code)
	}
}

func TestRegisterStatusReportsExpiry(t *testing.T) {
	srv := newAuthTestServer(t)
	invite, err := createInvite(context.Background(), srv.db, systemUserID(t, srv.db))
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	body, _ := json.Marshal(startRegisterRequest{InviteCode: invite})
	startReq := httptest.NewRequest(http.MethodPost, "/api/auth/register/start", bytes.NewReader(body))
	startResp := httptest.NewRecorder()
	srv.handleAuthRegisterStart(startResp, startReq)
	if startResp.Code != http.StatusOK {
		t.Fatalf("start: %d", startResp.Code)
	}
	startData := decodeJSON[startRegisterResponse](t, startResp)

	// Backdate the expiry.
	if _, err := srv.db.Exec(`update telegram_login_codes set expires_at = ? where code = ?`,
		time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), startData.Code); err != nil {
		t.Fatalf("expire: %v", err)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/auth/register/status?code="+startData.Code, nil)
	statusResp := httptest.NewRecorder()
	srv.handleAuthRegisterStatus(statusResp, statusReq)
	if got := decodeJSON[registerStatusResponse](t, statusResp).Status; got != "expired" {
		t.Fatalf("status = %q, want expired", got)
	}
}

func TestLoginFlow(t *testing.T) {
	srv := newAuthTestServer(t)
	systemID := systemUserID(t, srv.db)

	// Pre-existing user already linked to telegram.
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, ?, 0, ?, ?)`, 777, "tg_bob", "bob", now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()

	code := botIssueLoginCode(t, srv.db, userID, 777, "tg_bob")

	body, _ := json.Marshal(loginRequest{Code: code})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	srv.handleAuthLogin(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("login: status %d, body %s", resp.Code, resp.Body.String())
	}
	cookie := sessionCookieFromHeader(t, resp)
	me := decodeJSON[meResponse](t, resp)
	if me.Username == nil || *me.Username != "bob" {
		t.Fatalf("me.username = %v, want bob", me.Username)
	}

	// Re-using the login code fails.
	reuseReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	reuseResp := httptest.NewRecorder()
	srv.handleAuthLogin(reuseResp, reuseReq)
	if reuseResp.Code != http.StatusGone {
		t.Fatalf("login reuse status = %d, want 410", reuseResp.Code)
	}

	// System user cannot log in even if a stale login code exists.
	badCode := botIssueLoginCode(t, srv.db, systemID, 0, "")
	badBody, _ := json.Marshal(loginRequest{Code: badCode})
	badReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(badBody))
	badResp := httptest.NewRecorder()
	srv.handleAuthLogin(badResp, badReq)
	if badResp.Code != http.StatusForbidden {
		t.Fatalf("system login status = %d, want 403", badResp.Code)
	}

	// Logout deletes the session.
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	logoutResp := httptest.NewRecorder()
	srv.handleAuthLogout(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", logoutResp.Code)
	}

	// /api/auth/me without a valid cookie -> 401.
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	meResp := httptest.NewRecorder()
	srv.handleAuthMe(meResp, meReq)
	if meResp.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d, want 401", meResp.Code)
	}
}

func TestLoginStartSendsCodeWhenPasswordIsMissing(t *testing.T) {
	srv := newAuthTestServer(t)
	var sentChatID int64
	var sentText string
	srv.sendTelegram = func(ctx context.Context, chatID int64, text string) error {
		sentChatID = chatID
		sentText = text
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, ?, 0, ?, ?)`, 888, "tg_code", "code_only", now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()

	body, _ := json.Marshal(loginStartRequest{Username: "code_only"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/start", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	srv.handleAuthLoginStart(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("login start: status %d, body %s", resp.Code, resp.Body.String())
	}
	start := decodeJSON[loginStartResponse](t, resp)
	if start.HasPassword {
		t.Fatalf("has_password = true, want false")
	}
	if !start.CodeSent {
		t.Fatalf("code_sent = false, want true")
	}
	if sentChatID != 888 {
		t.Fatalf("sent chat = %d, want 888", sentChatID)
	}

	var code string
	if err := srv.db.QueryRow(`
select code from telegram_login_codes where user_id = ? and kind = 'login'`, userID).Scan(&code); err != nil {
		t.Fatalf("read code: %v", err)
	}
	if code == "" || !strings.Contains(sentText, code) {
		t.Fatalf("telegram text %q does not contain issued code %q", sentText, code)
	}
	if !isShortLoginCode(code) {
		t.Fatalf("login code = %q, want [A-Z0-9]{5}", code)
	}
	if !strings.Contains(sentText, "<code>"+code+"</code>") {
		t.Fatalf("telegram text %q does not render code as monospace", sentText)
	}

	loginBody, _ := json.Marshal(loginRequest{Code: code})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	loginResp := httptest.NewRecorder()
	srv.handleAuthLogin(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("login with issued code: status %d, body %s", loginResp.Code, loginResp.Body.String())
	}
	_ = sessionCookieFromHeader(t, loginResp)
}

func TestLoginStartPasswordUserRequestsCodeExplicitly(t *testing.T) {
	srv := newAuthTestServer(t)
	var sent int
	srv.sendTelegram = func(ctx context.Context, chatID int64, text string) error {
		sent++
		if chatID != 999 {
			t.Fatalf("sent chat = %d, want 999", chatID)
		}
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	salt := "salt"
	if _, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, password_hash, password_salt, is_system, created_at, updated_at)
values(?, ?, ?, ?, ?, 0, ?, ?)`, 999, "tg_pass", "passy", hashPassword("secret", salt), salt, now, now); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	body, _ := json.Marshal(loginStartRequest{Username: "passy"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/start", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	srv.handleAuthLoginStart(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("login start: status %d, body %s", resp.Code, resp.Body.String())
	}
	start := decodeJSON[loginStartResponse](t, resp)
	if !start.HasPassword {
		t.Fatalf("has_password = false, want true")
	}
	if start.CodeSent {
		t.Fatalf("code_sent = true, want false")
	}
	if sent != 0 {
		t.Fatalf("sent codes = %d, want 0", sent)
	}

	codeBody, _ := json.Marshal(loginStartRequest{Username: "passy", SendCode: true})
	codeReq := httptest.NewRequest(http.MethodPost, "/api/auth/login/start", bytes.NewReader(codeBody))
	codeResp := httptest.NewRecorder()
	srv.handleAuthLoginStart(codeResp, codeReq)
	if codeResp.Code != http.StatusOK {
		t.Fatalf("login code start: status %d, body %s", codeResp.Code, codeResp.Body.String())
	}
	codeStart := decodeJSON[loginStartResponse](t, codeResp)
	if !codeStart.HasPassword || !codeStart.CodeSent {
		t.Fatalf("response = %+v, want password user with sent code", codeStart)
	}
	if sent != 1 {
		t.Fatalf("sent codes = %d, want 1", sent)
	}
}

func TestUsernameValidation(t *testing.T) {
	cases := map[string]bool{
		"alice":                 true,
		"a":                     false,
		"":                      false,
		"with space":            false,
		"со_spaces":             false,
		"привет":                false,
		"alice.bob":             true,
		strings.Repeat("a", 33): false,
	}
	for input, want := range cases {
		if got := validUsername(input); got != want {
			t.Fatalf("validUsername(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestUsernameUniqueness(t *testing.T) {
	srv := newAuthTestServer(t)
	systemID := systemUserID(t, srv.db)

	// Two users registering, both want the same username.
	now := time.Now().UTC().Format(time.RFC3339)
	for _, tg := range []int64{1001, 1002} {
		if _, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, null, 0, ?, ?)`, tg, "tg", now, now); err != nil {
			t.Fatalf("seed user %d: %v", tg, err)
		}
	}
	// Issue login codes & sessions for both.
	var userA, userB int64
	srv.db.QueryRow(`select id from users where telegram_user_id = ?`, int64(1001)).Scan(&userA)
	srv.db.QueryRow(`select id from users where telegram_user_id = ?`, int64(1002)).Scan(&userB)

	tokA, err := func() (string, error) {
		tx, err := srv.db.BeginTx(context.Background(), nil)
		if err != nil {
			return "", err
		}
		defer tx.Rollback()
		token, err := createSessionTx(context.Background(), tx, userA, time.Now().UTC())
		if err != nil {
			return "", err
		}
		return token, tx.Commit()
	}()
	if err != nil {
		t.Fatalf("session A: %v", err)
	}
	tokB, err := func() (string, error) {
		tx, err := srv.db.BeginTx(context.Background(), nil)
		if err != nil {
			return "", err
		}
		defer tx.Rollback()
		token, err := createSessionTx(context.Background(), tx, userB, time.Now().UTC())
		if err != nil {
			return "", err
		}
		return token, tx.Commit()
	}()
	if err != nil {
		t.Fatalf("session B: %v", err)
	}

	body, _ := json.Marshal(usernameRequest{Username: "shared"})

	reqA := httptest.NewRequest(http.MethodPost, "/api/auth/username", bytes.NewReader(body))
	reqA.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokA})
	respA := httptest.NewRecorder()
	srv.handleAuthUsername(respA, reqA)
	if respA.Code != http.StatusOK {
		t.Fatalf("user A username: %d %s", respA.Code, respA.Body.String())
	}

	reqB := httptest.NewRequest(http.MethodPost, "/api/auth/username", bytes.NewReader(body))
	reqB.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokB})
	respB := httptest.NewRecorder()
	srv.handleAuthUsername(respB, reqB)
	if respB.Code != http.StatusConflict {
		t.Fatalf("user B username: %d, want 409", respB.Code)
	}
	_ = systemID
}

func TestSessionSlidingExpiry(t *testing.T) {
	srv := newAuthTestServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := srv.db.Exec(`
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(?, ?, ?, 0, ?, ?)`, 4242, "tg_x", "x", now, now)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	userID, _ := res.LastInsertId()

	tx, _ := srv.db.BeginTx(context.Background(), nil)
	tok, err := createSessionTx(context.Background(), tx, userID, time.Now().UTC())
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Backdate expiry to one second from now, then call handler -> sliding extension should kick in.
	hash := hashSessionToken(tok)
	soon := time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339)
	if _, err := srv.db.Exec(`update sessions set expires_at = ? where token_hash = ?`, soon, hash); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	resp := httptest.NewRecorder()
	srv.handleAuthMe(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("me: status %d", resp.Code)
	}

	var newExpiry string
	if err := srv.db.QueryRow(`select expires_at from sessions where token_hash = ?`, hash).Scan(&newExpiry); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339, newExpiry)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if time.Until(parsed) < sessionLifetime/2 {
		t.Fatalf("expiry was not slid: %s", newExpiry)
	}
}
