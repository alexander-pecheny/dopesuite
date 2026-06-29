package server

import (
	"log"
	"os"
	"time"
)

// debugTiming enables [timing] log lines on the heavy endpoints (export /
// handouts / split_fit). Off unless XY_DEBUG_TIMING is set, so prod stays quiet
// until we're profiling.
var debugTiming = os.Getenv("XY_DEBUG_TIMING") != ""

// timed returns a stop func that logs how long the labelled span took. Use as:
//
//	defer timed("handouts.pdf")()
//
// or capture the func to bound a narrower span. No-op (and no allocation churn
// worth caring about) when XY_DEBUG_TIMING is unset.
func timed(label string) func() {
	if !debugTiming {
		return func() {}
	}
	start := time.Now()
	return func() { log.Printf("[timing] %s took %s", label, time.Since(start)) }
}

// timingOn reports whether timing logging is enabled (for ad-hoc log lines).
func timingOn() bool { return debugTiming }
