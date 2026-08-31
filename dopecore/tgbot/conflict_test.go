package tgbot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// conflictAPI is a Telegram that always says someone else is polling.
func conflictAPI(t *testing.T, calls *atomic.Int64) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			calls.Add(1)
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// A 409 is the one poll error that means "your updates are going somewhere
// else". It has to be distinguishable, or the loop retries it every 3s forever
// and nothing anywhere looks wrong.
func TestGetUpdatesConflictIsTyped(t *testing.T) {
	var calls atomic.Int64
	c := New(Config{Token: "x", APIBase: conflictAPI(t, &calls), PollTimeout: time.Second})

	_, err := c.GetUpdates(context.Background(), 0)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("a 409 must be ErrConflict, got %v", err)
	}
}

// Backing off at the ordinary cadence would hammer Telegram while the two
// pollers keep splitting updates; the long delay is what makes the conflict
// survivable until someone reads the log.
func TestRunBacksOffOnConflict(t *testing.T) {
	var calls atomic.Int64
	c := New(Config{
		Token: "x", APIBase: conflictAPI(t, &calls), PollTimeout: time.Second,
		RetryDelay: time.Millisecond, ConflictDelay: time.Minute,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_ = c.Run(ctx, func(context.Context, *Client, Update) {})

	if n := calls.Load(); n != 1 {
		t.Errorf("a conflicted poller must wait ConflictDelay, not RetryDelay; got %d polls", n)
	}
	if h := HealthOf(c, time.Now()); h.OK || !h.Conflict {
		t.Errorf("a conflicted bot must read as unusable and say why, got %+v", h)
	}
}

// The conflict clears itself: whoever else was polling stopped, our next poll
// answers, and the login page can offer telegram again without a restart.
func TestConflictClearsOnNextGoodPoll(t *testing.T) {
	c := New(Config{Token: "x", PollTimeout: time.Minute})
	c.markStarted()
	c.markPoll(ErrConflict)
	if h := HealthOf(c, time.Now()); h.OK {
		t.Fatal("a conflicted bot must not read as healthy")
	}
	c.markPoll(nil)
	if h := HealthOf(c, time.Now()); !h.OK || h.Conflict {
		t.Errorf("a recovered bot must read as healthy, got %+v", h)
	}
}
