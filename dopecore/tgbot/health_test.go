package tgbot

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

var errFailed = errors.New("getUpdates failed")

// A bot that is up but no longer polling — a revoked token, a blocked network —
// is as useless to a login page as a dead one, and this is what says so.
func TestHealthOfFollowsPolling(t *testing.T) {
	c := New(Config{Token: "x", PollTimeout: 60 * time.Second})
	now := time.Now()

	if h := HealthOf(c, now); h.OK {
		t.Error("a client that never started polling must not read as healthy")
	}

	// Starting up is healthy: the first long poll on a quiet bot takes the whole
	// timeout to answer, and calling that minute an outage would report every
	// deploy as one.
	c.markStarted()
	if h := HealthOf(c, now); !h.OK {
		t.Error("a bot still waiting for its first poll must read as healthy")
	}
	if h := HealthOf(c, now.Add(150*time.Second)); h.OK {
		t.Error("a bot that never polled at all must stop being given the benefit of the doubt")
	}

	// ...unless polling is already failing, which is what a revoked token does
	// within seconds. No grace for that.
	failing := New(Config{Token: "x", PollTimeout: 60 * time.Second})
	failing.markStarted()
	failing.markPoll(errFailed)
	if h := HealthOf(failing, now); h.OK {
		t.Error("a bot whose polling is already failing must not read as healthy")
	}

	c.markPoll(nil)
	if h := HealthOf(c, now); !h.OK || h.LastPoll == "" {
		t.Errorf("a bot that just polled must be healthy, got %+v", h)
	}

	// One poll timeout of quiet is normal; two means the loop is not coming back.
	if h := HealthOf(c, now.Add(90*time.Second)); !h.OK {
		t.Error("90s with a 60s poll timeout is still within one exchange")
	}
	h := HealthOf(c, now.Add(150*time.Second))
	if h.OK || h.StaleFor == "" {
		t.Errorf("150s must read as stale and say for how long, got %+v", h)
	}
}

// The /send endpoint is the server's way to DM through the bot: guarded by the
// shared secret, absent entirely when no secret is configured.
func TestServeLocalSend(t *testing.T) {
	var mu sync.Mutex
	var sent url.Values
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			mu.Lock()
			sent = r.Form
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer api.Close()
	c := New(Config{Token: "t", APIBase: api.URL})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ServeLocal(ctx, addr, c, "s3cret")

	base := "http://" + addr
	var up bool
	for i := 0; i < 100; i++ {
		if resp, err := http.Get(base + "/healthz"); err == nil {
			resp.Body.Close()
			up = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !up {
		t.Fatal("local endpoint never came up")
	}

	post := func(secret, body string) int {
		req, _ := http.NewRequest(http.MethodPost, base+"/send", strings.NewReader(body))
		if secret != "" {
			req.Header.Set("X-Bot-Secret", secret)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := post("", `{"telegram_user_id":42,"text":"привет"}`); code != http.StatusForbidden {
		t.Fatalf("no secret → %d, want 403", code)
	}
	if code := post("wrong", `{"telegram_user_id":42,"text":"привет"}`); code != http.StatusForbidden {
		t.Fatalf("wrong secret → %d, want 403", code)
	}
	if code := post("s3cret", `{"telegram_user_id":0,"text":"x"}`); code != http.StatusBadRequest {
		t.Fatalf("no target → %d, want 400", code)
	}
	if code := post("s3cret", `{"telegram_user_id":42,"text":"привет"}`); code != http.StatusNoContent {
		t.Fatalf("send → %d, want 204", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if sent.Get("chat_id") != "42" || sent.Get("text") != "привет" {
		t.Fatalf("telegram saw %v, want chat_id=42 text=привет", sent)
	}
}
