package dopeserver

import (
	"sync/atomic"
	"testing"
)

func TestStaticGovernorEntersOnlyOnSustainedLoad(t *testing.T) {
	g := &staticGovernor{cfg: staticConfig{rateHigh: 400, rateLow: 150, sseMax: 1200, cooldown: 3}}
	if g.step(0, false, 999, 0) {
		t.Fatal("one hot tick must not enter lockdown")
	}
	if g.step(0, false, 10, 0) {
		t.Fatal("a blip followed by calm stays live")
	}
	g.step(0, false, 999, 0)
	if !g.step(0, false, 10, 2000) {
		t.Fatal("two sustained over-threshold ticks (rate, then sse) enter lockdown")
	}
}

func TestStaticGovernorExitsAfterDwellAndSustainedCalm(t *testing.T) {
	g := &staticGovernor{cfg: staticConfig{rateHigh: 400, rateLow: 150, cooldown: 3}}
	// Calm from the first tick: dwell and underTicks both reach 3 on the third.
	for i := 0; i < 2; i++ {
		if !g.step(0, true, 10, 0) {
			t.Fatalf("tick %d: exited before the cooldown", i)
		}
	}
	if g.step(0, true, 10, 0) {
		t.Fatal("the third calm tick completes the cooldown and exits")
	}
}

func TestStaticGovernorRateResetsTheCalmCounter(t *testing.T) {
	g := &staticGovernor{cfg: staticConfig{rateHigh: 400, rateLow: 150, cooldown: 2}}
	g.step(0, true, 10, 0)
	g.step(0, true, 500, 0) // still busy: underTicks back to 0
	if !g.step(0, true, 10, 0) {
		t.Fatal("dwell met but calm not sustained; must stay locked")
	}
	if g.step(0, true, 10, 0) {
		t.Fatal("two calm ticks after dwell exit lockdown")
	}
}

func TestStaticGovernorManualOverridePins(t *testing.T) {
	g := &staticGovernor{cfg: staticConfig{rateHigh: 400, rateLow: 150, cooldown: 30}}
	if !g.step(1, false, 0, 0) {
		t.Fatal("manual on pins lockdown on")
	}
	if g.step(2, true, 99999, 99999) {
		t.Fatal("manual off pins lockdown off")
	}
}

func TestLockdownServesSnapshotToViewersAndCapsEditors(t *testing.T) {
	var live atomic.Int64
	if on, _ := lockdownServes(false, false, false, &live); on {
		t.Fatal("no lockdown: live")
	}
	if on, _ := lockdownServes(true, false, true, &live); !on {
		t.Fatal("/static suffix forces the snapshot")
	}
	if on, _ := lockdownServes(false, true, false, &live); !on {
		t.Fatal("cookie-less viewer under lockdown gets the snapshot")
	}
	var releases []func()
	for i := 0; i < liveFallthroughCap; i++ {
		on, release := lockdownServes(false, true, true, &live)
		releases = append(releases, release)
		if on {
			t.Fatalf("editor %d of %d was pushed to the snapshot", i+1, liveFallthroughCap)
		}
	}
	on, release := lockdownServes(false, true, true, &live)
	release()
	if !on {
		t.Fatal("editor past the cap gets the snapshot")
	}
	for _, r := range releases {
		r()
	}
	if live.Load() != 0 {
		t.Fatalf("live slots leaked: %d", live.Load())
	}
	if on, _ := lockdownServes(false, true, true, &live); on {
		t.Fatal("once slots are released an editor gets the live path again")
	}
}

func TestSpliceInitReplacesTheMarkerOnce(t *testing.T) {
	page := []byte(`<script>window.__GAME_INIT__ = null;/*__GAME_INIT__*/;</script>`)
	out, err := spliceInit(page, gameInitMarker, []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `<script>window.__GAME_INIT__ = {"a":1};</script>` {
		t.Fatalf("got %s", out)
	}
	if _, err := spliceInit(page, ekInitMarker, nil); err == nil {
		t.Fatal("a page without the marker must be an error, not a silent pass-through")
	}
}
