package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginMethodsFollowsBotConfig(t *testing.T) {
	srv := newBackupServer(t)
	for _, c := range []struct {
		name   string
		secret string
		want   bool
	}{
		{"with a bot", "s3cret", true},
		{"without one", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XY_BOT_SECRET", c.secret)
			w := httptest.NewRecorder()
			srv.handleLoginMethods(w, httptest.NewRequest(http.MethodGet, "/api/auth/methods", nil))
			var got struct {
				Telegram bool `json:"telegram"`
			}
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Telegram != c.want {
				t.Errorf("telegram = %v, want %v", got.Telegram, c.want)
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
