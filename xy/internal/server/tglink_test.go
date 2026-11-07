package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func telegramRequired(t *testing.T, c *apiClient) bool {
	t.Helper()
	resp := c.do("GET", "/api/auth/me", nil)
	mustStatus(t, resp, http.StatusOK)
	var me meResponse
	c.decode(resp, &me)
	return me.TelegramRequired
}

// linkTelegram runs the link handshake for c as if the bot saw the code from tg.
func linkTelegram(t *testing.T, srv *server, c *apiClient, tg int64) *http.Response {
	t.Helper()
	resp := c.do("POST", "/api/auth/tg/link/start", nil)
	mustStatus(t, resp, http.StatusOK)
	var rs tgStartResponse
	c.decode(resp, &rs)
	if err := srv.simulateBotRegister(context.Background(), rs.Code, tg, "handle"); err != nil {
		t.Fatal(err)
	}
	return c.do("GET", "/api/auth/tg/link/status?code="+rs.Code, nil)
}

func TestAnAccountWithoutTelegramLinksOne(t *testing.T) {
	ts, srv := newTestServer(t)
	srv.bot = stubBot(t, true)
	t.Setenv("XY_BOT_NAME", "xy_bot")
	c := registerUser(t, srv, ts, 882001, "byhand")
	other := registerUser(t, srv, ts, 882002, "other")
	// An account made by hand, before the bot: no Telegram on it.
	if _, err := srv.db.Exec(`update users set telegram_user_id = null where username = 'byhand'`); err != nil {
		t.Fatal(err)
	}
	if !telegramRequired(t, c) || telegramRequired(t, other) {
		t.Fatal("want telegram_required for the account without one only")
	}

	// A Telegram that is someone else's is refused.
	resp := linkTelegram(t, srv, c, 882002)
	mustStatus(t, resp, http.StatusBadRequest)
	if body, _ := io.ReadAll(resp.Body); !strings.Contains(string(body), "другому аккаунту") {
		t.Fatalf("refusal: %s", body)
	}

	resp = linkTelegram(t, srv, c, 882003)
	mustStatus(t, resp, http.StatusOK)
	var out tgLinkResponse
	c.decode(resp, &out)
	if out.Status != "linked" {
		t.Fatalf("status %q", out.Status)
	}
	if telegramRequired(t, c) {
		t.Fatal("still required after linking")
	}
	var tg int64
	if err := srv.db.QueryRow(`select telegram_user_id from users where username = 'byhand'`).Scan(&tg); err != nil || tg != 882003 {
		t.Fatalf("telegram_user_id = %d (%v)", tg, err)
	}
}

// A bot that is down is no reason to lock anyone out.
func TestNoTelegramIsRequiredWhileTheBotIsDown(t *testing.T) {
	ts, srv := newTestServer(t)
	t.Setenv("XY_BOT_NAME", "xy_bot")
	c := registerUser(t, srv, ts, 882011, "byhand")
	if _, err := srv.db.Exec(`update users set telegram_user_id = null`); err != nil {
		t.Fatal(err)
	}
	srv.bot = stubBot(t, false)
	if telegramRequired(t, c) {
		t.Fatal("bot not polling: want no telegram_required")
	}
	srv.bot = nil
	if telegramRequired(t, c) {
		t.Fatal("no bot: want no telegram_required")
	}
}
