package tgbot

import (
	"errors"
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
