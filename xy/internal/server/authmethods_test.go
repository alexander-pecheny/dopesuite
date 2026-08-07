package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// resetBotHealth clears the probe cache between cases: it is a package global on
// purpose (one login page, one crowd, one probe), which means a test that skips
// this gets the previous case's verdict.
func resetBotHealth(t *testing.T) {
	t.Helper()
	botHealth.mu.Lock()
	botHealth.checked = time.Time{}
	botHealth.ok = false
	botHealth.mu.Unlock()
}

// botStub stands in for the bot's /healthz. `ok` false is the wedged bot: a live
// process whose polling has stopped, which is as useless as a dead one.
func botStub(t *testing.T, ok bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = w.Write([]byte(`{"ok":` + map[bool]string{true: "true", false: "false"}[ok] + `}`))
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func TestLoginMethodsFollowsBotConfig(t *testing.T) {
	srv := newBackupServer(t)
	for _, c := range []struct {
		name       string
		secret     string
		botName    string
		botUp      bool // whether a stub bot answers at all
		botHealthy bool
		want       string
	}{
		{"a configured, polling bot", "s3cret", "xy_bot", true, true, tgStatusOK},
		{"no secret at all", "", "xy_bot", true, true, tgStatusMisconfigured},
		{"secret but no @handle — the state that showed a dead link", "s3cret", "", true, true, tgStatusMisconfigured},
		{"configured, but nothing is listening", "s3cret", "xy_bot", false, false, tgStatusUnreachable},
		{"configured, but the bot stopped polling", "s3cret", "xy_bot", true, false, tgStatusUnreachable},
	} {
		t.Run(c.name, func(t *testing.T) {
			resetBotHealth(t)
			t.Setenv("XY_BOT_SECRET", c.secret)
			t.Setenv("XY_BOT_NAME", c.botName)
			if c.botUp {
				t.Setenv("XY_BOT_HEALTH_ADDR", botStub(t, c.botHealthy))
			} else {
				// A port nothing listens on: the probe must fail fast, not hang.
				t.Setenv("XY_BOT_HEALTH_ADDR", "127.0.0.1:1")
			}
			w := httptest.NewRecorder()
			srv.handleLoginMethods(w, httptest.NewRequest(http.MethodGet, "/api/auth/methods", nil))
			var got struct {
				Telegram bool   `json:"telegram"`
				Status   string `json:"telegram_status"`
			}
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Status != c.want {
				t.Errorf("telegram_status = %q, want %q", got.Status, c.want)
			}
			// The old boolean still has to mean "offer the button", for a page
			// older than telegram_status.
			if want := c.want == tgStatusOK; got.Telegram != want {
				t.Errorf("telegram = %v, want %v", got.Telegram, want)
			}
		})
	}
}

func TestTgStartRefusedWithoutBot(t *testing.T) {
	srv := newBackupServer(t)
	t.Setenv("XY_BOT_SECRET", "")
	w := httptest.NewRecorder()
	srv.handleTgStart(w, httptest.NewRequest(http.MethodPost, "/api/auth/tg/start", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
