package core

import (
	"log"
	"time"

	"dope/dope/realtime"
)

// BroadcastState fans out a full scoped state snapshot to all subscribers and
// returns the seq assigned to it. Any buffered deltas for the scope are flushed
// first (they carry lower seqs) so viewers never see the snapshot before them.
func (e *Engine) BroadcastState(festID int64, scope string, revision int64, payload []byte) uint64 {
	e.InvalidateFestViewCache(festID)
	e.SeqMu.Lock()
	defer e.SeqMu.Unlock()
	// A snapshot supersedes any buffered deltas for this scope; flush them first
	// (they carry lower seqs) so viewers never see the snapshot before them.
	e.flushDeltaLocked(scope)
	seq := e.bumpSeqLocked(scope)
	if e.DB != nil {
		payload = realtime.EventSnapshotJSON(scope, e.Epoch, revision, seq, payload)
	}
	e.RT.Broadcast(realtime.Event{FestID: festID, Revision: revision, Data: payload})
	return seq
}

// deltaCoalesceWindow bounds how long delta ops for one scope buffer before they
// fan out to VIEWERS as a single merged delta. Editors are exempt — they get
// every delta immediately (see BroadcastStateDelta) — so this adds latency only
// to spectators, a trade the product accepts (viewers may lag; editors stay
// current, both for their own edits and co-editors').
const deltaCoalesceWindow = 150 * time.Millisecond

// pendingDelta accumulates one scope's delta ops for the VIEWER fan-out within a
// coalescing window. Each edit still bumps the per-scope seq immediately (so the
// editor stream and HTTP responses carry per-edit seqs); the window's merged
// viewer delta spans [prevSeq, lastSeq] and applies as one step. A viewer that
// connected mid-window fetched state at the already-bumped seq, so the merged
// delta's seq <= its lastSeq and the client ignores it (seq-monotonic guard)
// instead of gap-resyncing.
type pendingDelta struct {
	festID   int64
	prevSeq  uint64
	lastSeq  uint64
	revision int64
	ops      [][]byte
	timer    *time.Timer
}

// BroadcastStateDelta fans out a scoped DELTA (the ops that produced the new
// state) instead of the whole state — the core fan-out win: every client gets a
// ~100-byte op list rather than the full game blob. Editors receive the delta
// IMMEDIATELY (there are only a handful, so the per-edit fan-out is cheap, and
// they must always see co-editors' changes without delay); viewers receive a
// single merged delta per coalescing window. Seq is bumped per edit under SeqMu
// so per-scope order matches seq order and HTTP responses can carry it.
func (e *Engine) BroadcastStateDelta(festID int64, scope string, revision int64, ops []byte) uint64 {
	e.InvalidateFestViewCache(festID)
	e.SeqMu.Lock()
	defer e.SeqMu.Unlock()
	prev := e.stateSeqLocked(scope)
	seq := e.bumpSeqLocked(scope)
	// Editors: immediate, uncoalesced — chains per edit (prevSeq = seq-1).
	e.RT.BroadcastTo(realtime.Event{FestID: festID, Revision: revision,
		Data: realtime.EventDeltaJSON(scope, e.Epoch, revision, seq, prev, ops)}, realtime.AudEditors)
	// Viewers: buffer for a merged broadcast at window end.
	if e.DeltaBuf == nil {
		e.DeltaBuf = map[string]*pendingDelta{}
	}
	pd := e.DeltaBuf[scope]
	if pd == nil {
		pd = &pendingDelta{festID: festID, prevSeq: prev}
		e.DeltaBuf[scope] = pd
		sc := scope
		// This fires in a detached timer goroutine with no net/http recover
		// above it, so any panic here would crash the whole process. The
		// close-race that caused exactly that is fixed in removeSubscriber, but
		// keep a recover as defense-in-depth: a stray panic in the viewer
		// fan-out must degrade to a dropped broadcast, never a server crash.
		pd.timer = time.AfterFunc(deltaCoalesceWindow, func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("recovered panic in delta flush for scope %s: %v", sc, r)
				}
			}()
			e.FlushDelta(sc)
		})
	}
	pd.festID = festID
	pd.revision = revision
	pd.lastSeq = seq
	pd.ops = append(pd.ops, ops)
	return seq
}

// BroadcastBatchedDelta fans out an editor-batched window's merged ops as ONE
// delta to BOTH editors and viewers immediately. Unlike BroadcastStateDelta,
// which buffers a per-edit stream for viewers, the editor-side batcher
// (edit_batch.go) already coalesced a whole window into these ops, so there is
// nothing left to buffer — both audiences get the single merged delta at once.
// Any stray viewer delta still buffered for the scope (from a non-batched path)
// is flushed first so seqs stay ordered.
func (e *Engine) BroadcastBatchedDelta(festID int64, scope string, revision int64, ops []byte) uint64 {
	e.InvalidateFestViewCache(festID)
	e.SeqMu.Lock()
	defer e.SeqMu.Unlock()
	e.flushDeltaLocked(scope)
	prev := e.stateSeqLocked(scope)
	seq := e.bumpSeqLocked(scope)
	e.RT.BroadcastTo(realtime.Event{FestID: festID, Revision: revision,
		Data: realtime.EventDeltaJSON(scope, e.Epoch, revision, seq, prev, ops)}, realtime.AudAll)
	return seq
}

// FlushDelta emits a scope's buffered viewer delta as one merged broadcast.
// Called from the coalescing timer (and from tests that force a flush).
func (e *Engine) FlushDelta(scope string) {
	e.SeqMu.Lock()
	defer e.SeqMu.Unlock()
	e.flushDeltaLocked(scope)
}

// flushDeltaLocked fans the buffered window's merged ops out to VIEWERS as a
// single delta spanning [prevSeq, lastSeq]. Caller holds SeqMu. No-op if nothing
// is buffered (a snapshot already flushed this window, or the timer raced one).
func (e *Engine) flushDeltaLocked(scope string) {
	pd := e.DeltaBuf[scope]
	if pd == nil {
		return
	}
	delete(e.DeltaBuf, scope)
	if pd.timer != nil {
		pd.timer.Stop()
	}
	payload := realtime.EventDeltaJSON(scope, e.Epoch, pd.revision, pd.lastSeq, pd.prevSeq, realtime.MergeOpsArrays(pd.ops))
	e.RT.BroadcastTo(realtime.Event{FestID: pd.festID, Revision: pd.revision, Data: payload}, realtime.AudViewers)
}

// bumpSeqLocked increments and returns the scope's seq. Caller holds SeqMu.
func (e *Engine) bumpSeqLocked(scope string) uint64 {
	if e.StateSeq == nil {
		e.StateSeq = map[string]uint64{}
	}
	e.StateSeq[scope]++
	return e.StateSeq[scope]
}

// stateSeqLocked returns the scope's current seq. Caller holds SeqMu.
func (e *Engine) stateSeqLocked(scope string) uint64 {
	if e.StateSeq == nil {
		return 0
	}
	return e.StateSeq[scope]
}

// CurrentStateSeq returns the scope's current seq (for the GET /state resync
// header). Safe to call without holding SeqMu.
func (e *Engine) CurrentStateSeq(scope string) uint64 {
	e.SeqMu.Lock()
	defer e.SeqMu.Unlock()
	return e.stateSeqLocked(scope)
}

// InvalidateFestViewCache drops the cached FestView JSON for a fest (called on
// every broadcast so the next reader rebuilds it).
func (e *Engine) InvalidateFestViewCache(festID int64) {
	e.FestViewMu.Lock()
	defer e.FestViewMu.Unlock()
	delete(e.FestViewCache, festID)
}
