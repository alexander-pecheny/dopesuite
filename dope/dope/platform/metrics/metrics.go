// Package metrics is the edit-path instrumentation for live concurrent-editor
// tests. Everything is inert unless DOPE_EDIT_METRICS is truthy, so production
// carries no overhead. The server holds a *Recorder; the game-state PATCH path
// fills a Sample and calls RecordEdit. Two plain log-line artifacts: one
// "editmetric edit" per committed PATCH, and a periodic "editmetric summary"
// rollup. (Moved out of the server as a self-contained sub-state — first step of
// the server→shell restructure.)
package metrics

import (
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const summaryInterval = 15 * time.Second

// Sample is one game-state PATCH's timing breakdown. Durations left zero are
// omitted from stats. Populated partly under/around the write lock and partly by
// the handler (e2e, broadcast).
type Sample struct {
	Fest, Game int64
	Ops        int
	Bytes      int   // size of the re-marshaled state blob written back
	Waiters    int64 // WriteWaiters depth observed when this edit joined the queue

	Wait      time.Duration // blocked acquiring the global write mutex
	Hold      time.Duration // critical section (lock held)
	Unmarshal time.Duration // json.Unmarshal of current state
	Marshal   time.Duration // json.Marshal of next state
	DB        time.Duration // UPDATE + revision bump + COMMIT
	Broadcast time.Duration // scoped delta fan-out (off the write lock)
	E2E       time.Duration // whole PATCH handler, request-in to response-out
}

// Recorder holds the edit-metrics state. On gates everything (so production pays
// nothing). WriteWaiters is the live gauge of goroutines queued on the global
// write mutex; FestViewHits/Misses tally the FestView cache.
type Recorder struct {
	On             bool
	WriteWaiters   atomic.Int64
	FestViewHits   atomic.Int64
	FestViewMisses atomic.Int64

	mu     sync.Mutex
	window []Sample
}

// Init reads DOPE_EDIT_METRICS and, when truthy, turns the recorder on and starts
// the background summary goroutine. The zero Recorder is a valid, disabled
// recorder (so a server built without Init — e.g. in tests — is safe).
func (r *Recorder) Init() {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("DOPE_EDIT_METRICS")))
	if v == "" || v == "0" || v == "false" || v == "off" || v == "no" {
		return
	}
	r.On = true
	log.Printf("edit metrics: ON (per-edit + %s summary lines, prefix 'editmetric')", summaryInterval)
	go r.runSummary()
}

// RecordEdit logs one per-edit line and buffers the sample for the summary. No-op
// when metrics are off. Called from the handler after the response is written, so
// neither the logging nor the buffering happens under the global write mutex.
func (r *Recorder) RecordEdit(m Sample) {
	if !r.On {
		return
	}
	log.Printf("editmetric edit fest=%d game=%d ops=%d bytes=%d waiters=%d wait_ms=%s hold_ms=%s unmarshal_ms=%s marshal_ms=%s db_ms=%s broadcast_ms=%s e2e_ms=%s",
		m.Fest, m.Game, m.Ops, m.Bytes, m.Waiters,
		FmtMs(m.Wait), FmtMs(m.Hold), FmtMs(m.Unmarshal), FmtMs(m.Marshal),
		FmtMs(m.DB), FmtMs(m.Broadcast), FmtMs(m.E2E))
	r.mu.Lock()
	// Cap the buffer so a stalled summary goroutine can't grow it without bound.
	if len(r.window) < 100000 {
		r.window = append(r.window, m)
	}
	r.mu.Unlock()
}

func (r *Recorder) runSummary() {
	ticker := time.NewTicker(summaryInterval)
	defer ticker.Stop()
	var hitsPrev, missPrev int64
	for range ticker.C {
		r.mu.Lock()
		window := r.window
		r.window = nil
		r.mu.Unlock()

		hits := r.FestViewHits.Load()
		misses := r.FestViewMisses.Load()
		dHits := hits - hitsPrev
		dMiss := misses - missPrev
		hitsPrev, missPrev = hits, misses

		if len(window) == 0 {
			if dHits+dMiss > 0 {
				log.Printf("editmetric summary edits=0 festview_hits=%d festview_misses=%d hit_rate=%s",
					dHits, dMiss, fmtRate(dHits, dHits+dMiss))
			}
			continue
		}

		waits := make([]time.Duration, len(window))
		holds := make([]time.Duration, len(window))
		dbs := make([]time.Duration, len(window))
		e2es := make([]time.Duration, len(window))
		var maxWaiters int64
		for i, m := range window {
			waits[i] = m.Wait
			holds[i] = m.Hold
			dbs[i] = m.DB
			e2es[i] = m.E2E
			if m.Waiters > maxWaiters {
				maxWaiters = m.Waiters
			}
		}
		log.Printf("editmetric summary edits=%d max_waiters=%d "+
			"wait_ms[p50/p95/max]=%s/%s/%s hold_ms[p50/p95/max]=%s/%s/%s "+
			"db_ms[p50/p95/max]=%s/%s/%s e2e_ms[p50/p95/max]=%s/%s/%s "+
			"festview_hits=%d festview_misses=%d hit_rate=%s",
			len(window), maxWaiters,
			PctMs(waits, 50), PctMs(waits, 95), PctMs(waits, 100),
			PctMs(holds, 50), PctMs(holds, 95), PctMs(holds, 100),
			PctMs(dbs, 50), PctMs(dbs, 95), PctMs(dbs, 100),
			PctMs(e2es, 50), PctMs(e2es, 95), PctMs(e2es, 100),
			dHits, dMiss, fmtRate(dHits, dHits+dMiss))
	}
}

// NowIf returns time.Now() when cond, else the zero time — lets the hot path skip
// the clock read entirely when metrics are off.
func NowIf(cond bool) time.Time {
	if cond {
		return time.Now()
	}
	return time.Time{}
}

// FmtMs renders a duration as milliseconds with two decimals.
func FmtMs(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 2, 64)
}

// PctMs returns the p-th percentile (nearest-rank) of durs in milliseconds.
func PctMs(durs []time.Duration, p int) string {
	if len(durs) == 0 {
		return "0.00"
	}
	sorted := make([]time.Duration, len(durs))
	copy(sorted, durs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := (p * len(sorted)) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return FmtMs(sorted[idx])
}

func fmtRate(num, den int64) string {
	if den == 0 {
		return "n/a"
	}
	return strconv.FormatFloat(float64(num)/float64(den), 'f', 3, 64)
}
