package tests

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"pecheny.me/dopecore/authcred"
	"pecheny.me/dopecore/session"

	spliffserver "spliff/spliff/server"
)

// What /profile can do to the account behind the session: set a password, and
// link a Telegram to it. The state machine is dopecore/tglogin's own tests;
// what is checked here is that the routes ask for a session, refuse what they
// must, and leave the users and sessions tables as they say they do.

func TestPasswordChangeProvesTheCurrentOneAndForgetsEveryOtherSession(t *testing.T) {
	w := newWorld(t)
	// A second live session of the same account: the one this is supposed to
	// forget.
	other := w.ts.As(w.alice.UserID)

	// The wrong current password changes nothing, and says so in words.
	resp := w.alice.Do(http.MethodPost, "/api/auth/password", map[string]any{
		"current_password": "not-my-password",
		"new_password":     "correct-horse-battery",
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("wrong current password = %d: %s", resp.Code, resp.Body.String())
	}
	if body := strings.TrimSpace(resp.Body.String()); body == "" {
		t.Fatal("the refusal said nothing")
	}
	if other.Do(http.MethodGet, "/api/auth/me", nil).Code != http.StatusOK {
		t.Fatal("a refused password change dropped a session")
	}

	// The right one goes through, and takes every other browser with it.
	if resp := w.alice.Do(http.MethodPost, "/api/auth/password", map[string]any{
		"current_password": "hunter2hunter2",
		"new_password":     "correct-horse-battery",
	}); resp.Code != http.StatusNoContent {
		t.Fatalf("password change = %d: %s", resp.Code, resp.Body.String())
	}
	if code := other.Do(http.MethodGet, "/api/auth/me", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("the other browser is still logged in: %d", code)
	}
	// ...but not the one that did it: being logged out by your own act is a bug.
	if code := w.alice.Do(http.MethodGet, "/api/auth/me", nil).Code; code != http.StatusOK {
		t.Fatalf("the session that changed the password = %d", code)
	}

	// The new password is the one that logs in now.
	anon := w.ts.Anonymous()
	if code := anon.Do(http.MethodPost, "/api/auth/login-password", map[string]any{
		"username": "alice", "password": "hunter2hunter2",
	}).Code; code != http.StatusBadRequest {
		t.Fatalf("the old password still logs in: %d", code)
	}
	if code := anon.Do(http.MethodPost, "/api/auth/login-password", map[string]any{
		"username": "alice", "password": "correct-horse-battery",
	}).Code; code != http.StatusOK {
		t.Fatalf("the new password does not log in: %d", code)
	}
}

func TestPasswordLengthIsRefusedInWords(t *testing.T) {
	w := newWorld(t)
	for _, password := range []string{"short", strings.Repeat("x", authcred.PasswordMaxLen+1)} {
		resp := w.alice.Do(http.MethodPost, "/api/auth/password", map[string]any{
			"current_password": "hunter2hunter2",
			"new_password":     password,
		})
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("password of %d characters = %d", len(password), resp.Code)
		}
		if strings.TrimSpace(resp.Body.String()) == "" {
			t.Fatal("the refusal said nothing")
		}
	}
	// And nothing was written: the old password still logs in.
	if code := w.ts.Anonymous().Do(http.MethodPost, "/api/auth/login-password", map[string]any{
		"username": "alice", "password": "hunter2hunter2",
	}).Code; code != http.StatusOK {
		t.Fatalf("a refused length changed the password: %d", code)
	}
}

// An account made through Telegram has no password, and sets its first without
// proving a previous one.
func TestAPasswordlessAccountSetsItsFirstPassword(t *testing.T) {
	ts := spliffserver.NewTestServer(t, t.TempDir())
	client := ts.As(ts.AddUser("newcomer", ""))
	if resp := client.Do(http.MethodPost, "/api/auth/password", map[string]any{
		"new_password": "correct-horse-battery",
	}); resp.Code != http.StatusNoContent {
		t.Fatalf("first password = %d: %s", resp.Code, resp.Body.String())
	}
	if code := ts.Anonymous().Do(http.MethodPost, "/api/auth/login-password", map[string]any{
		"username": "newcomer", "password": "correct-horse-battery",
	}).Code; code != http.StatusOK {
		t.Fatalf("the first password does not log in: %d", code)
	}
}

func TestProfileAsksForASession(t *testing.T) {
	w := newWorld(t)
	anon := w.ts.Anonymous()
	// The page sends a logged-out visitor to the login page...
	page := anon.Do(http.MethodGet, "/profile", nil)
	if page.Code != http.StatusSeeOther {
		t.Fatalf("GET /profile anonymously = %d", page.Code)
	}
	if location := page.Header().Get("Location"); !strings.Contains(location, "/login") {
		t.Fatalf("GET /profile anonymously → %q", location)
	}
	// ...and both of its writes answer a status its fetch can read.
	for _, call := range []struct{ method, target string }{
		{http.MethodPost, "/api/auth/password"},
		{http.MethodPost, "/api/auth/tg/link/start"},
		{http.MethodGet, "/api/auth/tg/link/status?code=ABC123"},
	} {
		if code := anon.Do(call.method, call.target, nil).Code; code != http.StatusUnauthorized {
			t.Errorf("%s %s anonymously = %d", call.method, call.target, code)
		}
	}
}

// ---- linking a Telegram from a session ----

type linkAnswer struct {
	Status   string  `json:"status"`
	Telegram *string `json:"telegram"`
}

func linkStatus(t *testing.T, c *spliffserver.Client, code string) linkAnswer {
	t.Helper()
	var out linkAnswer
	c.JSON(http.MethodGet, "/api/auth/tg/link/status?code="+code, nil, &out)
	return out
}

func TestTelegramLinkWaitsForTheBotAndThenAttaches(t *testing.T) {
	w := newWorld(t)

	// A code nobody has answered yet is pending, and the account is untouched.
	w.ts.TgCode("PENDING1", 0, "", session.TelegramAuthLifetime)
	if got := linkStatus(t, w.alice, "PENDING1").Status; got != "pending" {
		t.Fatalf("status = %q, want pending", got)
	}

	// Once the bot has written back, the same poll attaches it.
	w.ts.TgCode("ANSWERED", 555, "alice_tg", session.TelegramAuthLifetime)
	out := linkStatus(t, w.alice, "ANSWERED")
	if out.Status != "linked" || out.Telegram == nil || *out.Telegram != "alice_tg" {
		t.Fatalf("status = %q telegram = %v", out.Status, out.Telegram)
	}
	// The account carries it, and /api/auth/me says so to the page.
	var me struct {
		Telegram *string `json:"telegram"`
	}
	w.alice.JSON(http.MethodGet, "/api/auth/me", nil, &me)
	if me.Telegram == nil || *me.Telegram != "alice_tg" {
		t.Fatalf("me.telegram = %v", me.Telegram)
	}

	// The code is burned: a replay finds nothing.
	if got := linkStatus(t, w.alice, "ANSWERED").Status; got != "not_found" {
		t.Fatalf("replayed code = %q, want not_found", got)
	}

	// And an account carries at most one: a second Telegram is refused in words.
	w.ts.TgCode("SECOND12", 556, "alice_other", session.TelegramAuthLifetime)
	resp := w.alice.Do(http.MethodGet, "/api/auth/tg/link/status?code=SECOND12", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("a second telegram = %d: %s", resp.Code, resp.Body.String())
	}
	if strings.TrimSpace(resp.Body.String()) == "" {
		t.Fatal("the refusal said nothing")
	}
}

func TestTelegramLinkRefusesSomebodyElsesTelegram(t *testing.T) {
	w := newWorld(t)
	w.ts.TgCode("ALICES12", 700, "shared_tg", session.TelegramAuthLifetime)
	if got := linkStatus(t, w.alice, "ALICES12").Status; got != "linked" {
		t.Fatalf("alice's link = %q", got)
	}

	w.ts.TgCode("BOBS1234", 700, "shared_tg", session.TelegramAuthLifetime)
	resp := w.bob.Do(http.MethodGet, "/api/auth/tg/link/status?code=BOBS1234", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("somebody else's telegram = %d: %s", resp.Code, resp.Body.String())
	}
	var me struct {
		Telegram *string `json:"telegram"`
	}
	w.bob.JSON(http.MethodGet, "/api/auth/me", nil, &me)
	if me.Telegram != nil {
		t.Fatalf("bob picked up a telegram anyway: %v", *me.Telegram)
	}
}

func TestTelegramLinkRefusesALapsedCode(t *testing.T) {
	w := newWorld(t)
	w.ts.TgCode("LAPSED12", 800, "late_tg", -time.Minute)
	if got := linkStatus(t, w.alice, "LAPSED12").Status; got != "expired" {
		t.Fatalf("status = %q, want expired", got)
	}
	var me struct {
		Telegram *string `json:"telegram"`
	}
	w.alice.JSON(http.MethodGet, "/api/auth/me", nil, &me)
	if me.Telegram != nil {
		t.Fatalf("a lapsed code linked something: %v", *me.Telegram)
	}
}

// The test server runs no bot, which is what an instance with no token looks
// like: the start route says so rather than minting a code nobody will collect.
func TestTelegramLinkStartNeedsABot(t *testing.T) {
	w := newWorld(t)
	resp := w.alice.Do(http.MethodPost, "/api/auth/tg/link/start", nil)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("link start with no bot = %d: %s", resp.Code, resp.Body.String())
	}
	if strings.TrimSpace(resp.Body.String()) == "" {
		t.Fatal("the refusal said nothing")
	}
}
