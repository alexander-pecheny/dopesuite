package tgbot

import (
	"testing"
	"time"
)

// A bot that is up but no longer polling — a revoked token, a blocked network —
// is as useless to a login page as a dead one, and this is what says so.
func TestHealthOfFollowsPolling(t *testing.T) {
	c := New(Config{Token: "x", PollTimeout: 60 * time.Second})
	now := time.Now()

	if h := HealthOf(c, now); h.OK {
		t.Error("a bot that has never polled must not read as healthy")
	}

	c.markPolled()
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
