package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pecheny.me/dopecore/tglogin"
)

func TestLoginMethodsFollowsBotConfig(t *testing.T) {
	srv := newBackupServer(t)
	for _, c := range []struct {
		name    string
		hasBot  bool
		polling bool
		botName string
		want    string
	}{
		{"a polling bot with a handle", true, true, "xy_bot", tglogin.StatusOK},
		{"no bot on this instance", false, false, "xy_bot", tglogin.StatusMisconfigured},
		{"a bot but no @handle — the state that showed a dead link", true, true, "", tglogin.StatusMisconfigured},
		{"a bot that is not polling", true, false, "xy_bot", tglogin.StatusUnreachable},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv.bot = nil
			if c.hasBot {
				srv.bot = stubBot(t, c.polling)
			}
			t.Setenv("XY_BOT_NAME", c.botName)
			w := httptest.NewRecorder()
			srv.handleLoginMethods(w, httptest.NewRequest(http.MethodGet, "/api/auth/methods", nil))
			var got tglogin.MethodsResponse
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Status != c.want {
				t.Errorf("telegram_status = %q, want %q", got.Status, c.want)
			}
			// The old boolean still has to mean "offer the button", for a page
			// older than telegram_status.
			if want := c.want == tglogin.StatusOK; got.Telegram != want {
				t.Errorf("telegram = %v, want %v", got.Telegram, want)
			}
		})
	}
}

func TestTgStartRefusedWithoutBot(t *testing.T) {
	srv := newBackupServer(t)
	srv.bot = nil
	w := httptest.NewRecorder()
	srv.handleTgStart(w, httptest.NewRequest(http.MethodPost, "/api/auth/tg/start", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
