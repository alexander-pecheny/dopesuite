package server

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRegisterReloginSameTelegram: a telegram account that already exists logs
// straight back in, whatever username it types.
func TestRegisterReloginSameTelegram(t *testing.T) {
	ts, srv := newTestServer(t)
	registerUser(t, srv, ts, 557003, "carol") // first registration

	c := &apiClient{t: t, base: ts.URL}
	resp := c.do("POST", "/api/auth/tg/start", nil)
	mustStatus(t, resp, 200)
	var rs tgStartResponse
	c.decode(resp, &rs)
	if err := srv.simulateBotRegister(context.Background(), rs.Code, 557003, "carol"); err != nil {
		t.Fatal(err)
	}
	resp = c.do("GET", "/api/auth/tg/status?code="+rs.Code, nil)
	mustStatus(t, resp, 200)
	var st tgStatusResponse
	c.decode(resp, &st)
	if st.Status != "ready" || st.Username == nil || *st.Username != "carol" {
		t.Fatalf("status = %+v, want ready/carol", st)
	}
}

// TestRegisterUsernameTakenPasswordless: claiming a username held by a
// telegram-only account (no password to prove ownership) → username_taken.
func TestRegisterUsernameTakenPasswordless(t *testing.T) {
	ts, srv := newTestServer(t)
	registerUser(t, srv, ts, 557010, "bob") // telegram-only, no password

	c := &apiClient{t: t, base: ts.URL}
	resp := c.do("POST", "/api/auth/tg/start", nil)
	mustStatus(t, resp, 200)
	var rs tgStartResponse
	c.decode(resp, &rs)
	if err := srv.simulateBotRegister(context.Background(), rs.Code, 557011, "bob2"); err != nil {
		t.Fatal(err)
	}
	resp = c.do("GET", "/api/auth/tg/status?code="+rs.Code, nil)
	mustStatus(t, resp, 200)
	resp = c.do("POST", "/api/auth/tg/claim", map[string]string{"code": rs.Code, "username": "bob"})
	mustStatus(t, resp, 200)
	var st tgStatusResponse
	c.decode(resp, &st)
	if st.Status != "username_taken" {
		t.Fatalf("status = %q, want username_taken", st.Status)
	}
}

// TestRegisterLinkPasswordAccount: an admin-created password account (no telegram)
// is linked when the registrant proves the password.
func TestRegisterLinkPasswordAccount(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	if err := srv.addUser(ctx, "alice", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}

	c := &apiClient{t: t, base: ts.URL}
	resp := c.do("POST", "/api/auth/tg/start", nil)
	mustStatus(t, resp, 200)
	var rs tgStartResponse
	c.decode(resp, &rs)
	if err := srv.simulateBotRegister(ctx, rs.Code, 557020, "alice_tg"); err != nil {
		t.Fatal(err)
	}
	resp = c.do("GET", "/api/auth/tg/status?code="+rs.Code, nil)
	mustStatus(t, resp, 200)

	// claim the password account's username with no password → password_required
	resp = c.do("POST", "/api/auth/tg/claim", map[string]string{"code": rs.Code, "username": "alice"})
	mustStatus(t, resp, 200)
	var st tgStatusResponse
	c.decode(resp, &st)
	if st.Status != "password_required" {
		t.Fatalf("status = %q, want password_required", st.Status)
	}

	// wrong password is rejected
	resp = c.do("POST", "/api/auth/tg/claim", map[string]string{"code": rs.Code, "username": "alice", "password": "nope"})
	mustStatus(t, resp, 400)

	// correct password links and logs in
	resp = c.do("POST", "/api/auth/tg/claim", map[string]string{"code": rs.Code, "username": "alice", "password": "correct-horse-battery"})
	mustStatus(t, resp, 200)
	resp = c.do("GET", "/api/auth/me", nil)
	mustStatus(t, resp, 200)
	var me meResponse
	c.decode(resp, &me)
	if me.Username == nil || *me.Username != "alice" {
		t.Fatalf("me = %+v, want username alice", me)
	}
}

// TestQuotaEnforced: an over-quota attachment upload is refused with 413.
func TestQuotaEnforced(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 557030, "quotauser")

	// board + list + card
	resp := c.do("POST", "/api/boards", map[string]string{
		"name": "b", "kdf_salt": enc("s"), "kdf_params": "{}", "wrapped_key": enc("w"), "verify_token": enc("v"),
	})
	mustStatus(t, resp, 200)
	var b struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &b)
	resp = c.do("POST", "/api/boards/"+itoa(b.ID)+"/lists", map[string]string{"title_enc": enc("l"), "rank": "m"})
	mustStatus(t, resp, 200)
	var l struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &l)
	resp = c.do("POST", "/api/lists/"+itoa(l.ID)+"/cards", map[string]string{"description_enc": enc("d"), "rank": "m"})
	mustStatus(t, resp, 200)
	var card struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &card)

	// squeeze the quota to a few bytes
	if _, err := srv.db.Exec(`update users set quota_bytes = 8 where telegram_user_id = 557030`); err != nil {
		t.Fatal(err)
	}

	uresp := c.uploadAttachment(t, ts, card.ID, bytes.Repeat([]byte("x"), 64))
	mustStatus(t, uresp, http.StatusRequestEntityTooLarge)
}

// startConfirm runs tg/start + simulated bot confirm and returns the code.
func startConfirm(t *testing.T, srv *server, ts *httptest.Server, c *apiClient, tgID int64, tgName string) string {
	t.Helper()
	resp := c.do("POST", "/api/auth/tg/start", nil)
	mustStatus(t, resp, 200)
	var rs tgStartResponse
	c.decode(resp, &rs)
	if err := srv.simulateBotRegister(context.Background(), rs.Code, tgID, tgName); err != nil {
		t.Fatal(err)
	}
	return rs.Code
}

// TestTgCodeSingleUse: a code is burned on a successful claim, so it can't be replayed.
func TestTgCodeSingleUse(t *testing.T) {
	ts, srv := newTestServer(t)
	c := &apiClient{t: t, base: ts.URL}
	code := startConfirm(t, srv, ts, c, 558001, "solo")
	c.do("GET", "/api/auth/tg/status?code="+code, nil)
	mustStatus(t, c.do("POST", "/api/auth/tg/claim", map[string]string{"code": code, "username": "solo"}), 200)

	// Replaying the consumed code now finds nothing.
	resp := c.do("GET", "/api/auth/tg/status?code="+code, nil)
	mustStatus(t, resp, 200)
	var st tgStatusResponse
	c.decode(resp, &st)
	if st.Status != "not_found" {
		t.Fatalf("replay status = %q, want not_found", st.Status)
	}
}

// TestTgCodeExpiredNotReplayable: an expired consumed code cannot mint a session.
func TestTgCodeExpiredNotReplayable(t *testing.T) {
	ts, srv := newTestServer(t)
	registerUser(t, srv, ts, 558010, "backagain") // existing telegram account

	c := &apiClient{t: t, base: ts.URL}
	code := startConfirm(t, srv, ts, c, 558010, "backagain")
	// Backdate the code past expiry.
	if _, err := srv.db.Exec(`update telegram_login_codes set expires_at = ? where code = ?`,
		rfc3339(time.Now().Add(-time.Minute)), code); err != nil {
		t.Fatal(err)
	}
	resp := c.do("GET", "/api/auth/tg/status?code="+code, nil)
	mustStatus(t, resp, 200)
	var st tgStatusResponse
	c.decode(resp, &st)
	if st.Status != "expired" {
		t.Fatalf("status = %q, want expired", st.Status)
	}
}

// TestAdminUsernameReserved: open signup cannot mint the configured admin login.
func TestAdminUsernameReserved(t *testing.T) {
	ts, srv := newTestServer(t)
	c := &apiClient{t: t, base: ts.URL}
	code := startConfirm(t, srv, ts, c, 558020, "attacker")
	c.do("GET", "/api/auth/tg/status?code="+code, nil)
	// default admin username is "pecheny"
	resp := c.do("POST", "/api/auth/tg/claim", map[string]string{"code": code, "username": "pecheny"})
	mustStatus(t, resp, 403)
}

// uploadAttachment posts a blob to a card's attachment endpoint with the client's cookies.
func (c *apiClient) uploadAttachment(t *testing.T, ts *httptest.Server, cardID int64, cipher []byte) *http.Response {
	t.Helper()
	body := new(bytes.Buffer)
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("meta", `{"filename_enc":"`+enc("f")+`","mime":"image/webp","lossless":false}`)
	fw, _ := mw.CreateFormFile("blob", "blob")
	fw.Write(cipher)
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/cards/"+itoa(cardID)+"/attachments", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range c.jar {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
