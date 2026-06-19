package metrics

import (
	"testing"
	"time"
)

func TestPctMsAndFmtMs(t *testing.T) {
	if got := FmtMs(1500 * time.Microsecond); got != "1.50" {
		t.Errorf("FmtMs(1.5ms) = %q, want 1.50", got)
	}
	durs := []time.Duration{1 * time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond}
	if got := PctMs(durs, 50); got != "3.00" {
		t.Errorf("PctMs p50 = %q, want 3.00", got)
	}
	if got := PctMs(durs, 100); got != "4.00" {
		t.Errorf("PctMs p100 = %q, want 4.00", got)
	}
	if got := PctMs(nil, 95); got != "0.00" {
		t.Errorf("PctMs(nil) = %q, want 0.00", got)
	}
}
