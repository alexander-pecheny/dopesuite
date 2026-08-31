package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pecheny.me/dopecore/tgbot"
)

// stubBot is a bot wired to a fake Telegram. `polling` decides whether it has
// ever reached it, which is the only thing the login page asks about. Sends go
// to the stub, so a mention nudge never leaves the test — this box runs the
// real bot.
func stubBot(t *testing.T, polling bool) *tgbot.Client {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	t.Cleanup(api.Close)
	c := tgbot.New(tgbot.Config{Token: "stub", APIBase: api.URL, PollTimeout: time.Minute})
	if !polling {
		return c
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = c.Run(ctx, func(context.Context, *tgbot.Client, tgbot.Update) {}) }()
	t.Cleanup(cancel)
	deadline := time.Now().Add(2 * time.Second)
	for c.LastPoll().IsZero() {
		if time.Now().After(deadline) {
			t.Fatal("stub bot never polled")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	return c
}
