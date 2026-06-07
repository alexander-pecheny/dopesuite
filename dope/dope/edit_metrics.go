package main

import (
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Edit-path instrumentation for live concurrent-editor tests. Everything here is
// inert unless DOPE_EDIT_METRICS is truthy, so production carries no overhead.
//
// Two artifacts, both plain log lines (no endpoint, no DB writes — so the
// measurement never perturbs the very write path it measures):
//
//   - one "editmetric edit" line per committed game-state PATCH, carrying the
//     lock-contention breakdown (wait/hold) and the work done under the lock
//     (unmarshal/marshal/db) plus the end-to-end handler time. This is the raw
//     timeline; scripts/editmetrics.py turns it into percentiles.
//   - one "editmetric summary" rollup every editMetricsSummaryInterval with
//     per-window p50/p95/max so a `tail -f | grep summary` gives a live glance.
//
// The headline question — "is the global write mutex the bottleneck under
// concurrent editors?" — is answered by wait_ms (time blocked acquiring s.mu)
// versus hold_ms (time the critical section runs) and the waiters gauge.

const editMetricsSummaryInterval = 15 * time.Second

// editSample is one game-state PATCH's timing breakdown. Durations left zero are
// simply omitted from stats. Populated partly by patchGameState (under/around
// the lock) and partly by the handler (e2e, broadcast).
type editSample struct {
	fest, game int64
	ops        int
	bytes      int   // size of the re-marshaled state blob written back
	waiters    int64 // writeWaiters depth observed when this edit joined the queue

	wait      time.Duration // blocked acquiring the global write mutex
	hold      time.Duration // critical section (lock held)
	unmarshal time.Duration // json.Unmarshal of current state
	marshal   time.Duration // json.Marshal of next state
	db        time.Duration // UPDATE + revision bump + COMMIT
	broadcast time.Duration // scoped delta fan-out (off the write lock)
	e2e       time.Duration // whole PATCH handler, request-in to response-out
}

func (s *server) initEditMetrics() {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("DOPE_EDIT_METRICS")))
	if v == "" || v == "0" || v == "false" || v == "off" || v == "no" {
		return
	}
	s.editMetricsOn = true
	log.Printf("edit metrics: ON (per-edit + %s summary lines, prefix 'editmetric')", editMetricsSummaryInterval)
	go s.runEditMetricsSummary()
}

// recordEdit logs one per-edit line and buffers the sample for the summary. No-op
// when metrics are off. Called from the handler after the response is written, so
// neither the logging nor the buffering happens under the global write mutex.
func (s *server) recordEdit(m editSample) {
	if !s.editMetricsOn {
		return
	}
	log.Printf("editmetric edit fest=%d game=%d ops=%d bytes=%d waiters=%d wait_ms=%s hold_ms=%s unmarshal_ms=%s marshal_ms=%s db_ms=%s broadcast_ms=%s e2e_ms=%s",
		m.fest, m.game, m.ops, m.bytes, m.waiters,
		fmtMs(m.wait), fmtMs(m.hold), fmtMs(m.unmarshal), fmtMs(m.marshal),
		fmtMs(m.db), fmtMs(m.broadcast), fmtMs(m.e2e))
	s.editMu.Lock()
	// Cap the buffer so a stalled/dead summary goroutine can't grow it without
	// bound. The summary drains every 15s; this cap is a safety net far above any
	// realistic human-editor rate.
	if len(s.editWindow) < 100000 {
		s.editWindow = append(s.editWindow, m)
	}
	s.editMu.Unlock()
}

// runEditMetricsSummary drains the sample window every interval and logs one
// rollup line: count, the contention percentiles, cache hit rate, and peak
// waiter depth for the window.
func (s *server) runEditMetricsSummary() {
	ticker := time.NewTicker(editMetricsSummaryInterval)
	defer ticker.Stop()
	var hitsPrev, missPrev int64
	for range ticker.C {
		s.editMu.Lock()
		window := s.editWindow
		s.editWindow = nil
		s.editMu.Unlock()

		hits := s.festViewHits.Load()
		misses := s.festViewMisses.Load()
		dHits := hits - hitsPrev
		dMiss := misses - missPrev
		hitsPrev, missPrev = hits, misses

		if len(window) == 0 {
			// Stay quiet when nobody edited, but surface cache churn if a viewer
			// load forced rebuilds in an otherwise idle window.
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
			waits[i] = m.wait
			holds[i] = m.hold
			dbs[i] = m.db
			e2es[i] = m.e2e
			if m.waiters > maxWaiters {
				maxWaiters = m.waiters
			}
		}
		log.Printf("editmetric summary edits=%d max_waiters=%d "+
			"wait_ms[p50/p95/max]=%s/%s/%s hold_ms[p50/p95/max]=%s/%s/%s "+
			"db_ms[p50/p95/max]=%s/%s/%s e2e_ms[p50/p95/max]=%s/%s/%s "+
			"festview_hits=%d festview_misses=%d hit_rate=%s",
			len(window), maxWaiters,
			pctMs(waits, 50), pctMs(waits, 95), pctMs(waits, 100),
			pctMs(holds, 50), pctMs(holds, 95), pctMs(holds, 100),
			pctMs(dbs, 50), pctMs(dbs, 95), pctMs(dbs, 100),
			pctMs(e2es, 50), pctMs(e2es, 95), pctMs(e2es, 100),
			dHits, dMiss, fmtRate(dHits, dHits+dMiss))
	}
}

// nowIf returns time.Now() when cond, else the zero time — lets the hot path
// skip the clock read entirely when metrics are off.
func nowIf(cond bool) time.Time {
	if cond {
		return time.Now()
	}
	return time.Time{}
}

// fmtMs renders a duration as milliseconds with two decimals (microsecond
// resolution), compact and grep/parse-friendly.
func fmtMs(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 2, 64)
}

// pctMs returns the p-th percentile (nearest-rank) of durs in milliseconds.
func pctMs(durs []time.Duration, p int) string {
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
	return fmtMs(sorted[idx])
}

func fmtRate(num, den int64) string {
	if den == 0 {
		return "n/a"
	}
	return strconv.FormatFloat(float64(num)/float64(den), 'f', 3, 64)
}
