package server

import (
	"testing"
)

// mintToken creates an API token for the (cookie-authed) client and returns the
// raw value, shown once.
func mintToken(t *testing.T, c *apiClient, label string) string {
	t.Helper()
	resp := c.do("POST", "/api/tokens", map[string]string{"label": label})
	mustStatus(t, resp, 200)
	var out createTokenResponse
	c.decode(resp, &out)
	if out.Token == "" {
		t.Fatal("no token in response")
	}
	return out.Token
}

// newBoard creates a board through the cookie client and returns its id.
func newBoard(t *testing.T, c *apiClient, name string) string {
	t.Helper()
	resp := c.do("POST", "/api/boards", map[string]string{
		"name": name, "kdf_salt": enc("salt"), "kdf_params": `{"kdf":"scrypt","N":32768,"r":8,"p":1}`,
		"wrapped_key": enc("wrapped"), "verify_token": enc("verify"),
	})
	mustStatus(t, resp, 200)
	var created struct {
		ID int64 `json:"id"`
	}
	c.decode(resp, &created)
	return itoa(created.ID)
}

// TestAPITokenIsTheUser: a bearer token reads and writes the whole API as its
// owner (ADR-0015).
func TestAPITokenIsTheUser(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 555100, "tokenuser")
	boardID := newBoard(t, c, "board")
	raw := mintToken(t, c, "xy-cli")

	tok := &apiClient{t: t, base: ts.URL, bearer: raw}

	resp := tok.do("GET", "/api/auth/me", nil)
	mustStatus(t, resp, 200)
	var me meResponse
	tok.decode(resp, &me)
	if me.Username == nil || *me.Username != "tokenuser" {
		t.Fatalf("me.username = %v, want tokenuser", me.Username)
	}

	resp = tok.do("GET", "/api/boards/"+boardID, nil)
	mustStatus(t, resp, 200)

	// A write goes through too, attributed to the token's owner.
	resp = tok.do("POST", "/api/boards/"+boardID+"/lists", map[string]string{"title_enc": enc("список"), "rank": "m"})
	mustStatus(t, resp, 200)
}

func TestAPITokenRejected(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 555101, "rejectuser")
	raw := mintToken(t, c, "xy-cli")

	unknown := &apiClient{t: t, base: ts.URL, bearer: "deadbeef"}
	mustStatus(t, unknown.do("GET", "/api/boards", nil), 401)

	// Revoking kills it.
	resp := c.do("GET", "/api/tokens", nil)
	mustStatus(t, resp, 200)
	var list []apiTokenDTO
	c.decode(resp, &list)
	if len(list) != 1 {
		t.Fatalf("tokens = %d, want 1", len(list))
	}
	mustStatus(t, c.do("DELETE", "/api/tokens/"+itoa(list[0].ID), nil), 204)

	tok := &apiClient{t: t, base: ts.URL, bearer: raw}
	mustStatus(t, tok.do("GET", "/api/boards", nil), 401)
}

// TestAPITokenCarveOuts: the routes a token may not reach — the password (its
// own kill switch), the username, and /admin.
func TestAPITokenCarveOuts(t *testing.T) {
	ts, srv := newTestServer(t)
	// Registered first: an admin username is reserved against registration.
	c := registerUser(t, srv, ts, 555102, "carveuser")
	t.Setenv(adminUserEnv, "carveuser")
	raw := mintToken(t, c, "xy-cli")
	tok := &apiClient{t: t, base: ts.URL, bearer: raw}

	mustStatus(t, tok.do("POST", "/api/auth/password", map[string]string{"new_password": "hunter2hunter"}), 403)
	mustStatus(t, tok.do("POST", "/api/auth/username", map[string]string{"username": "someoneelse"}), 403)
	// /admin sends the unauthenticated to /login (303, followed here).
	resp := tok.do("GET", "/admin", nil)
	if resp.Request.URL.Path != "/login" {
		t.Fatalf("token reached %s, want a bounce to /login", resp.Request.URL.Path)
	}

	// The same page still opens with the cookie.
	resp = c.do("GET", "/admin", nil)
	mustStatus(t, resp, 200)
	if resp.Request.URL.Path != "/admin" {
		t.Fatalf("admin cookie bounced to %s", resp.Request.URL.Path)
	}
}

// TestPasswordChangeRevokesTokens: changing the password is the kill switch —
// every token and every OTHER session dies, the caller's stays.
func TestPasswordChangeRevokesTokens(t *testing.T) {
	ts, srv := newTestServer(t)
	c := registerUser(t, srv, ts, 555103, "killuser")
	raw := mintToken(t, c, "xy-cli")
	mustStatus(t, c.do("POST", "/api/auth/password", map[string]string{"new_password": "hunter2hunter"}), 204)

	// A second session, opened with the new password.
	other := &apiClient{t: t, base: ts.URL}
	mustStatus(t, other.do("POST", "/api/auth/login-password",
		map[string]string{"username": "killuser", "password": "hunter2hunter"}), 200)
	mustStatus(t, other.do("GET", "/api/auth/me", nil), 200)

	raw2 := mintToken(t, other, "second")

	// The kill switch, pressed from the second session.
	mustStatus(t, other.do("POST", "/api/auth/password",
		map[string]string{"current_password": "hunter2hunter", "new_password": "correcthorse"}), 204)

	for _, tokenValue := range []string{raw, raw2} {
		tok := &apiClient{t: t, base: ts.URL, bearer: tokenValue}
		mustStatus(t, tok.do("GET", "/api/boards", nil), 401)
	}
	// The first session is gone; the one that pressed it survives.
	mustStatus(t, c.do("GET", "/api/auth/me", nil), 401)
	mustStatus(t, other.do("GET", "/api/auth/me", nil), 200)
}
