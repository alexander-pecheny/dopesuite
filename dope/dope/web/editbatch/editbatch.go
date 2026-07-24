// Package editbatch holds the editor-side write-batching runtime — the single
// write path for every game edit in the system (ADR-0005). Each edit used to
// take the global write mutex, run its own transaction and fsync-commit on its
// own, so N concurrent editors hammering a grid produced N lock acquisitions
// and N commits per burst: the dominant source of write contention, and the
// shape of the 2026-06-13 freeze where one stuck write pinned the lock for
// everyone.
//
// Batcher mirrors the viewer-side delta coalescing (deltaCoalesceWindow) on the
// WRITE side: edits to one game within editBatchWindow are collected and applied
// together under a SINGLE lock acquisition in ONE transaction with ONE
// commit/fsync, then broadcast once. That caps writes per actively-edited game
// at ~1000/editBatchWindow ≈ 6/sec regardless of how many editors are typing.
//
// Crucially this changes only the TRANSACTION boundary, not the per-edit
// semantics: inside the shared transaction each edit is still replayed in
// arrival order with its own actor re-seeded into audit_ctx, its own row-op
// triggers firing and its own journal record — so audit history, attribution,
// replay and undo/redo are byte-for-byte unchanged. Each job runs in its own
// SAVEPOINT, so one that fails halfway (per-match edits write as they go) rolls
// back alone without poisoning the window.
//
// Per-match Protocol edits additionally get, ONCE per window per touched match,
// the semantic recompute their per-edit endpoints used to run each: the scorer
// materialises match_results, then the resolver advances bracket slots.
//
// A 200 is returned to an editor only once the whole window's transaction has
// committed (and that editor's own ops applied) — exactly the "200 only when the
// batched edits succeed" contract.
package editbatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"dope/dope/domain/core"
	"dope/dope/domain/edit"
	"dope/dope/domain/matchops"
	"dope/dope/domain/resolver"
	"dope/dope/domain/scoring"
	"dope/dope/platform/metrics"
	"dope/dope/platform/realtime"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
)

// pointerFromSegments renders a parsed patch path as the JSON pointer an
// OpMatchPatch record carries.
func pointerFromSegments(path []edit.JSONPathSegment) string {
	var b strings.Builder
	for _, seg := range path {
		b.WriteByte('/')
		if seg.IsIndex {
			b.WriteString(strconv.Itoa(seg.Index))
			continue
		}
		escaped := strings.ReplaceAll(seg.Key, "~", "~0")
		b.WriteString(strings.ReplaceAll(escaped, "/", "~1"))
	}
	return b.String()
}

// editBatchWindow bounds how long game-state edits buffer before the batch is
// applied. Matches the viewer-side deltaCoalesceWindow so editor and viewer
// pacing are symmetric; 150ms is short enough not to feel laggy while long
// enough to fold a burst of co-editors into one write.
const editBatchWindow = 150 * time.Millisecond

// jobKind distinguishes what a queued write does. Protocol state travels as
// patches; the two Structure transitions keep their own endpoints but ride the
// same window so they serialize with the edits around them.
type jobKind uint8

const (
	kindGamePatch  jobKind = iota // flat game's state document (od/ksi)
	kindMatchPatch                // one match's Protocol state blob (ek)
	kindMatchFinish
	kindMatchVenue
)

// editResult is what a queued edit gets back once its window commits. For
// match-scoped jobs `next` is the committed MatchView; for flat games it is the
// game's state document.
type editResult struct {
	next     []byte
	revision int64
	seq      uint64
	err      error
}

// editJob is one pending write awaiting its window's flush.
type editJob struct {
	kind     jobKind
	scope    core.FestScope
	matchID  int64
	code     string
	req      edit.PatchRequest
	finished bool
	venue    int
	payload  string          // raw request body, recorded verbatim in the journal
	ctx      context.Context // detached audit context (actor/request/fest/game), festwrite.WriteTxTimeout-bound
	cancel   context.CancelFunc
	sample   *metrics.Sample // optional metricsOn, nil when off
	submitAt time.Time       // set only when metricsOn on, for the wait breakdown
	done     chan editResult
}

// finish delivers a result exactly once. The channel is buffered (cap 1) so the
// send never blocks even if the requesting goroutine already gave up waiting
// (client disconnect); the default guards against a double-send.
func (j *editJob) finish(res editResult) {
	select {
	case j.done <- res:
	default:
	}
}

// editBatch accumulates one game's pending edits within a window.
type editBatch struct {
	scope core.FestScope
	jobs  []*editJob
	timer *time.Timer
}

// Batcher holds the open (still-collecting) batch per game id, plus references
// to the engine and metrics recorder it applies edits against.
type Batcher struct {
	Eng *core.Engine
	Rec *metrics.Recorder

	// OnFestChanged, when set, is called after a window that changed a match's
	// finished status, so the server can refresh the fest-level grid it caches.
	OnFestChanged func(festID, gameID, revision int64)

	mu      sync.Mutex
	pending map[int64]*editBatch
}

// NewBatcher constructs a Batcher bound to the given engine and metrics
// recorder.
func NewBatcher(eng *core.Engine, rec *metrics.Recorder) *Batcher {
	return &Batcher{
		Eng:     eng,
		Rec:     rec,
		pending: make(map[int64]*editBatch),
	}
}

// SubmitEdit queues a flat game's state PATCH and blocks until its window
// commits (or the requesting context is canceled).
func (b *Batcher) SubmitEdit(reqCtx context.Context, scope core.FestScope, req edit.PatchRequest, payload string, sample *metrics.Sample) ([]byte, int64, error) {
	res := b.submit(reqCtx, &editJob{kind: kindGamePatch, scope: scope, req: req, payload: payload, sample: sample})
	return res.next, res.revision, res.err
}

// SubmitMatchEdit queues a per-match Protocol state PATCH. It returns the
// committed MatchView (with the seq its broadcast was assigned) so the editor
// can keep its optimistic copy in lockstep with the delta it will also receive.
func (b *Batcher) SubmitMatchEdit(reqCtx context.Context, scope core.FestScope, matchID int64, code string, req edit.PatchRequest, payload string, sample *metrics.Sample) ([]byte, uint64, error) {
	if sample != nil {
		sample.Fest, sample.Game, sample.Ops = scope.FestID, scope.GameID, len(req.Ops)
	}
	res := b.submit(reqCtx, &editJob{
		kind: kindMatchPatch, scope: scope, matchID: matchID, code: code,
		req: req, payload: payload, sample: sample,
	})
	return res.next, res.seq, res.err
}

// SubmitMatchFinish queues a match's finished/active transition.
func (b *Batcher) SubmitMatchFinish(reqCtx context.Context, scope core.FestScope, matchID int64, code string, finished bool) ([]byte, uint64, error) {
	res := b.submit(reqCtx, &editJob{
		kind: kindMatchFinish, scope: scope, matchID: matchID, code: code,
		finished: finished, payload: util.MustJSON(map[string]any{"finished": finished}),
	})
	return res.next, res.seq, res.err
}

// SubmitMatchVenue queues a match's venue assignment.
func (b *Batcher) SubmitMatchVenue(reqCtx context.Context, scope core.FestScope, matchID int64, code string, number int) ([]byte, uint64, error) {
	if number <= 0 {
		return nil, 0, errors.New("bad venue number")
	}
	res := b.submit(reqCtx, &editJob{
		kind: kindMatchVenue, scope: scope, matchID: matchID, code: code,
		venue: number, payload: util.MustJSON(map[string]any{"venue": number}),
	})
	return res.next, res.seq, res.err
}

// submit enqueues a job and blocks until its window commits. The job is applied
// regardless of whether the caller keeps waiting — once queued it is a durable
// intent — so a client disconnect never drops an edit, it only stops us
// reporting the result.
func (b *Batcher) submit(reqCtx context.Context, job *editJob) editResult {
	// Detached, attribution-carrying context so the write stays attributed to the
	// acting user/request and is bounded by festwrite.WriteTxTimeout, and is NOT
	// aborted by this client's disconnect (it may be carrying co-editors' edits
	// too). Owned by the flusher, which cancels it once the batch has been applied.
	job.ctx, job.cancel = festwrite.AuditDetachedContext(reqCtx, job.scope.FestID)
	job.done = make(chan editResult, 1)
	if b.Rec.On {
		job.submitAt = time.Now()
	}
	b.enqueueEdit(job)

	select {
	case res := <-job.done:
		return res
	case <-reqCtx.Done():
		return editResult{err: reqCtx.Err()}
	}
}

// enqueueEdit appends a job to its game's open batch, starting the flush timer
// when it opens a fresh window.
func (b *Batcher) enqueueEdit(job *editJob) {
	b.mu.Lock()
	if b.pending == nil {
		b.pending = make(map[int64]*editBatch)
	}
	batch := b.pending[job.scope.GameID]
	if batch == nil {
		batch = &editBatch{scope: job.scope}
		b.pending[job.scope.GameID] = batch
		gid := job.scope.GameID
		batch.timer = time.AfterFunc(editBatchWindow, func() {
			b.flushEditBatch(gid)
		})
	}
	batch.jobs = append(batch.jobs, job)
	b.mu.Unlock()
}

// flushEditBatch detaches the open batch for a game and applies it. Runs in the
// timer's detached goroutine.
func (b *Batcher) flushEditBatch(gameID int64) {
	b.mu.Lock()
	batch := b.pending[gameID]
	delete(b.pending, gameID)
	b.mu.Unlock()
	if batch == nil {
		return
	}
	if batch.timer != nil {
		batch.timer.Stop()
	}
	b.applyEditBatch(batch)
}

// touched tracks the per-match work a window accumulated: the pre-edit view to
// diff against, whether a finish transition needs computed places, and the
// revision the match ended up at.
type touched struct {
	matchID  int64
	code     string
	oldData  []byte
	changed  bool // at least one job on this match committed
	finishTo *bool
	venue    int
	revision int64
	data     []byte
	view     store.MatchView
}

// applyEditBatch applies a window's edits under a single lock acquisition in one
// transaction, then resolves every job and broadcasts once per touched scope. It
// must resolve every job's done channel even on panic, so no editor request can
// hang forever.
func (b *Batcher) applyEditBatch(batch *editBatch) {
	scope := batch.scope
	jobs := batch.jobs
	defer func() {
		if r := recover(); r != nil {
			log.Printf("recovered panic in edit batch flush for game %d: %v", scope.GameID, r)
			b.failBatch(jobs, fmt.Errorf("internal error applying edit batch"))
		}
	}()

	metricsOn := b.Rec.On

	// Acquire the dedicated connection OFF the global lock (pool wait bounded by
	// festwrite.WriteTxTimeout), exactly as the hardened match-write path does, so a starved
	// pool can never pin b.Eng.Mu. See the 2026-06-13 freeze note in audit.go.
	txCtx, cancelTx := context.WithTimeout(context.Background(), festwrite.WriteTxTimeout)
	defer cancelTx()
	conn, err := b.Eng.AcquireWriteConn(txCtx, "edit-batch")
	if err != nil {
		b.failBatch(jobs, err)
		return
	}
	defer conn.Close()

	if metricsOn {
		b.Rec.WriteWaiters.Add(int64(len(jobs)))
	}
	unlock := b.Eng.LockWrite("edit-batch")
	acquired := time.Now()
	if metricsOn {
		b.Rec.WriteWaiters.Add(-int64(len(jobs)))
	}

	// Pre-images of every match this window will touch, captured under our
	// exclusive lock before the mutation commits, so each broadcast can be a
	// minimal delta. Best-effort: an empty pre-image just means a full snapshot.
	order, byMatch := b.preImages(txCtx, scope, jobs)

	tx, err := b.Eng.BeginWriteTxConn(txCtx, conn)
	if err != nil {
		unlock()
		b.failBatch(jobs, err)
		return
	}
	// Safety net: a no-op after Commit, but guarantees the tx is finalized on any
	// early return below before the conn is returned to the pool.
	defer tx.Rollback()

	results := make([]editResult, len(jobs))
	var mergedOps [][]byte
	var finalNext []byte
	var lastRevision int64
	committedAny := false
	needSnapshot := false

	for i, job := range jobs {
		// Re-seed the per-transaction audit context to THIS edit's actor so the
		// row-op triggers (journal_*_update etc.) attribute it correctly, then
		// replay the edit inside its own savepoint: a per-match job writes as it
		// goes, so a failure halfway must undo only its own partial work.
		_ = festwrite.SeedAuditCtx(job.ctx, tx)
		res, opsJSON, err := b.applyOneJob(txCtx, tx, i, job, byMatch)
		if err != nil {
			results[i] = editResult{err: err}
			continue
		}
		results[i] = res
		committedAny = true
		if job.kind != kindGamePatch {
			byMatch[job.matchID].changed = true
			continue
		}
		finalNext = res.next
		lastRevision = res.revision
		if opsJSON != nil {
			mergedOps = append(mergedOps, opsJSON)
		} else {
			// Ops failed to re-marshal (practically never): fall back to a full
			// snapshot so viewers still converge on the committed state.
			needSnapshot = true
		}
	}

	if !committedAny {
		// Every edit in the window was invalid: nothing to commit (the deferred
		// rollback finalizes the tx). Hand each its own error.
		unlock()
		for i, job := range jobs {
			b.deliver(job, results[i], metricsOn, acquired, 0)
		}
		return
	}

	// A match every one of whose jobs failed was never actually touched: drop it
	// so it is not rescored, re-revisioned and rebroadcast for nothing.
	order = slices.DeleteFunc(order, func(matchID int64) bool { return !byMatch[matchID].changed })

	// Once per window per touched match: score it into match_results, then let
	// the resolver advance any bracket slot those results settled.
	cascadedIDs, err := b.recomputeTouched(txCtx, tx, scope, order, byMatch)
	if err != nil {
		unlock()
		b.failBatch(jobs, err)
		return
	}

	if err := tx.Commit(); err != nil {
		unlock()
		// Commit failed: the whole window failed atomically. Even the edits that
		// applied in memory did not persist, so report the failure to all.
		b.failBatch(jobs, err)
		return
	}

	// Post-images, still under the lock (as the per-edit path did), so the views
	// we broadcast are exactly the ones this window committed.
	cascaded := b.postImages(txCtx, scope, order, byMatch, cascadedIDs)
	hold := time.Since(acquired)
	unlock()

	// Broadcast every touched match before delivering, so the seq each editor
	// gets back is the one its own broadcast was assigned.
	finished, festRevision := b.broadcastTouched(scope, order, byMatch)

	for i, job := range jobs {
		if job.kind != kindGamePatch && results[i].err == nil {
			t := byMatch[job.matchID]
			if t == nil || len(t.data) == 0 {
				// The edit committed but its view would not load: report it rather
				// than hand the editor an empty body it cannot parse.
				results[i] = editResult{err: errors.New("edit committed but its match view could not be loaded")}
			} else {
				results[i] = editResult{next: t.data, revision: t.revision, seq: t.view.Seq}
			}
		}
		b.deliver(job, results[i], metricsOn, acquired, hold)
	}

	for _, cv := range cascaded {
		if data, err := json.Marshal(cv); err == nil {
			b.Eng.BroadcastState(scope.FestID, MatchScopeKey(scope.GameID, cv.Code), cv.Revision, data)
		}
	}
	if finished && b.OnFestChanged != nil {
		b.OnFestChanged(scope.FestID, scope.GameID, festRevision)
	}

	if len(mergedOps) == 0 && !needSnapshot {
		return
	}
	// One merged broadcast for the flat game's whole window — the batching already
	// provided the coalescing window, so editors and viewers get it immediately.
	scopeKey := fmt.Sprintf("game-state:%d", scope.GameID)
	if needSnapshot {
		b.Eng.BroadcastState(scope.FestID, scopeKey, lastRevision, finalNext)
	} else {
		b.Eng.BroadcastBatchedDelta(scope.FestID, scopeKey, lastRevision, realtime.MergeOpsArrays(mergedOps))
	}
}

// broadcastTouched fans out each touched match — a minimal delta against its
// pre-image when that is cheaper, the full view otherwise — and stamps the
// assigned seq back onto the view the editors will receive as their response.
// It reports whether any match changed its finished status, and the highest
// fest revision the window reached.
func (b *Batcher) broadcastTouched(scope core.FestScope, order []int64, byMatch map[int64]*touched) (bool, int64) {
	finished := false
	var festRevision int64
	for _, matchID := range order {
		t := byMatch[matchID]
		if t.finishTo != nil {
			finished = true
		}
		festRevision = util.MaxInt64(festRevision, t.revision)
		scopeKey := MatchScopeKey(scope.GameID, t.code)
		if deltaOps, _ := realtime.MatchDeltaOps(t.oldData, t.data); len(deltaOps) > 0 {
			t.view.Seq = b.Eng.BroadcastStateDelta(scope.FestID, scopeKey, t.revision, deltaOps)
		} else {
			t.view.Seq = b.Eng.BroadcastState(scope.FestID, scopeKey, t.revision, t.data)
		}
		if stamped, err := json.Marshal(t.view); err == nil {
			t.data = stamped
		}
	}
	return finished, festRevision
}

// MatchScopeKey names a match's SSE scope. The batcher owns every match
// broadcast, so it owns the key the server's read paths must agree with.
func MatchScopeKey(gameID int64, code string) string {
	return fmt.Sprintf("match:%d:%s", gameID, code)
}

// preImages registers every match the window touches and reads its committed
// view, in first-touch order.
func (b *Batcher) preImages(ctx context.Context, scope core.FestScope, jobs []*editJob) ([]int64, map[int64]*touched) {
	var order []int64
	byMatch := map[int64]*touched{}
	for _, job := range jobs {
		if job.kind == kindGamePatch || byMatch[job.matchID] != nil {
			continue
		}
		t := &touched{matchID: job.matchID, code: job.code}
		if view, err := b.loadMatchView(ctx, scope, job.matchID); err == nil {
			t.oldData, _ = json.Marshal(view)
		}
		byMatch[job.matchID] = t
		order = append(order, job.matchID)
	}
	return order, byMatch
}

// postImages reloads the committed view of every touched match and of the
// downstream matches the resolver advanced, skipping the touched ones (already
// reloaded). Failures are non-fatal: the edit is committed, and a missed
// cascade broadcast only costs a reload.
func (b *Batcher) postImages(ctx context.Context, scope core.FestScope, order []int64, byMatch map[int64]*touched, cascadedIDs []int64) []store.MatchView {
	for _, matchID := range order {
		t := byMatch[matchID]
		view, err := b.loadMatchView(ctx, scope, matchID)
		if err != nil {
			continue
		}
		view.Revision = util.MaxInt64(view.Revision, t.revision)
		t.view = view
		t.data, _ = json.Marshal(view)
	}
	var cascaded []store.MatchView
	for _, matchID := range cascadedIDs {
		if byMatch[matchID] != nil {
			continue
		}
		view, err := b.loadMatchView(ctx, scope, matchID)
		if err != nil || view.Code == "" {
			continue
		}
		cascaded = append(cascaded, view)
	}
	return cascaded
}

func (b *Batcher) loadMatchView(ctx context.Context, scope core.FestScope, matchID int64) (store.MatchView, error) {
	match, err := store.LoadDBMatchStateWhere(ctx, b.Eng.DB,
		`m.id = ? and m.fest_id = ? and m.game_id = ?`, matchID, scope.FestID, scope.GameID)
	if err != nil {
		return store.MatchView{}, err
	}
	return store.MatchViewFrom(match), nil
}

// applyOneJob runs one job inside its own savepoint, so a job that fails after
// a partial write leaves the rest of the window untouched.
func (b *Batcher) applyOneJob(ctx context.Context, tx *sql.Tx, index int, job *editJob, byMatch map[int64]*touched) (editResult, []byte, error) {
	name := "job" + strconv.Itoa(index)
	if _, err := tx.ExecContext(ctx, "savepoint "+name); err != nil {
		return editResult{}, nil, err
	}
	res, opsJSON, err := b.runJob(ctx, tx, job, byMatch)
	if err != nil {
		if _, rerr := tx.ExecContext(ctx, "rollback to savepoint "+name); rerr != nil {
			return editResult{}, nil, rerr
		}
	}
	if _, rerr := tx.ExecContext(ctx, "release savepoint "+name); rerr != nil {
		return editResult{}, nil, rerr
	}
	return res, opsJSON, err
}

func (b *Batcher) runJob(ctx context.Context, tx *sql.Tx, job *editJob, byMatch map[int64]*touched) (editResult, []byte, error) {
	switch job.kind {
	case kindGamePatch:
		next, revision, opsJSON, err := b.applyGamePatchTx(job.ctx, tx, job.scope, job.req, job.payload, job.sample)
		return editResult{next: next, revision: revision}, opsJSON, err
	case kindMatchPatch:
		return editResult{}, nil, b.applyMatchPatchTx(job.ctx, tx, job)
	case kindMatchFinish:
		return editResult{}, nil, b.applyMatchFinishTx(job.ctx, tx, job, byMatch[job.matchID])
	case kindMatchVenue:
		if err := b.applyMatchVenueTx(job.ctx, tx, job); err != nil {
			return editResult{}, nil, err
		}
		byMatch[job.matchID].venue = job.venue
		return editResult{}, nil, nil
	}
	return editResult{}, nil, fmt.Errorf("unknown edit job kind %d", job.kind)
}

// applyMatchPatchTx applies one editor's ops to a match's Protocol state blob.
// The ops address blob paths; matchops turns each into the typed mutation for
// that path, and the recorded BlobOps become the journal's semantic record.
func (b *Batcher) applyMatchPatchTx(ctx context.Context, tx *sql.Tx, job *editJob) error {
	match, err := b.loadMatchTx(ctx, tx, job)
	if err != nil {
		return err
	}
	if match.State.Finished {
		return errors.New("match is finished")
	}
	ops, err := store.MutateMatchBlobTx(ctx, tx, job.matchID, func(blob *store.MatchBlob) error {
		return matchops.Apply(blob, match, job.req.Ops)
	})
	if err != nil {
		return err
	}
	return festwrite.JournalMatchPatchTx(ctx, tx, job.matchID, ops)
}

func (b *Batcher) applyMatchFinishTx(ctx context.Context, tx *sql.Tx, job *editJob, t *touched) error {
	status := "active"
	if job.finished {
		status = "finished"
	}
	if _, err := tx.ExecContext(ctx, `update matches set status = ? where id = ?`, status, job.matchID); err != nil {
		return err
	}
	value := job.finished
	t.finishTo = &value
	return nil
}

func (b *Batcher) applyMatchVenueTx(ctx context.Context, tx *sql.Tx, job *editJob) error {
	var venueID int64
	if err := tx.QueryRowContext(ctx, `
select id from venues where fest_id = ? and number = ?`, job.scope.FestID, job.venue).Scan(&venueID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("unknown venue")
		}
		return err
	}
	_, err := tx.ExecContext(ctx, `update matches set venue_id = ? where id = ?`, venueID, job.matchID)
	return err
}

func (b *Batcher) loadMatchTx(ctx context.Context, tx *sql.Tx, job *editJob) (store.DBMatchState, error) {
	return store.LoadDBMatchStateWhere(ctx, tx,
		`m.id = ? and m.fest_id = ? and m.game_id = ?`, job.matchID, job.scope.FestID, job.scope.GameID)
}

// recomputeTouched scores every match the window changed and then resolves the
// game's slots once, returning the matches the cascade moved.
func (b *Batcher) recomputeTouched(ctx context.Context, tx *sql.Tx, scope core.FestScope, order []int64, byMatch map[int64]*touched) ([]int64, error) {
	if len(order) == 0 {
		return nil, nil
	}
	for _, matchID := range order {
		t := byMatch[matchID]
		match, err := store.LoadDBMatchStateWhere(ctx, tx,
			`m.id = ? and m.fest_id = ? and m.game_id = ?`, matchID, scope.FestID, scope.GameID)
		if err != nil {
			return nil, err
		}
		// Finishing a match turns the live grid into a result: places are computed
		// from the scores, then the scorer lets any pin override them again.
		if t.finishTo != nil && *t.finishTo {
			store.AssignComputedPlaces(&match.State)
		}
		if err := scoring.RecalculateMatchResultsTx(ctx, tx, match); err != nil {
			return nil, err
		}
		eventType, payload := journalEventFor(t)
		revision, err := bumpMatchRevisionTx(ctx, tx, scope.FestID, matchID, eventType, payload)
		if err != nil {
			return nil, err
		}
		t.revision = revision
	}
	return resolver.ResolveGameSlotsTx(ctx, tx, scope.GameID)
}

// journalEventFor names the coarse live event for a window's effect on one
// match. A window that also flipped a Structure transition records that, since
// the Protocol ops are already journaled per edit as OpMatchPatch records.
func journalEventFor(t *touched) (string, string) {
	switch {
	case t.finishTo != nil:
		return "match:update", util.MustJSON(map[string]any{"code": t.code, "finished": *t.finishTo})
	case t.venue != 0:
		return "match:venue", util.MustJSON(map[string]any{"code": t.code, "venue": t.venue})
	}
	return "game:state-patch", util.MustJSON(map[string]any{"match": t.code})
}

// bumpMatchRevisionTx advances the match's and the fest's revision once for the
// whole window and records the semantic journal event for it.
func bumpMatchRevisionTx(ctx context.Context, tx *sql.Tx, festID, matchID int64, eventType, payload string) (int64, error) {
	if _, err := tx.ExecContext(ctx, `update matches set revision = revision + 1 where id = ?`, matchID); err != nil {
		return 0, err
	}
	return festwrite.BumpFestRevisionTx(ctx, tx, festID, eventType, payload)
}

// deliver hands a job its result, records its metric sample, and releases its
// detached context.
func (b *Batcher) deliver(job *editJob, res editResult, metricsOn bool, acquired time.Time, hold time.Duration) {
	if metricsOn && job.sample != nil {
		if !job.submitAt.IsZero() {
			job.sample.Wait = acquired.Sub(job.submitAt)
		}
		job.sample.Hold = hold
	}
	job.finish(res)
	job.cancel()
}

// failBatch fails every job with err and releases their contexts.
func (b *Batcher) failBatch(jobs []*editJob, err error) {
	for _, job := range jobs {
		job.finish(editResult{err: err})
		job.cancel()
	}
}

// applyGamePatchTx applies one flat game's state PATCH within an existing
// transaction: read the current state, apply the ops, write it back and bump the
// fest revision (which appends the journal event). It performs no locking, no
// begin/commit — the batcher owns those — so several edits share one tx. It
// returns the new state, the assigned revision and the marshaled ops (for the
// merged broadcast). Ops are validated and applied before any write, so a
// returned error means nothing was written for this edit.
func (b *Batcher) applyGamePatchTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, req edit.PatchRequest, payload string, sample *metrics.Sample) ([]byte, int64, []byte, error) {
	if len(req.Ops) == 0 {
		return nil, 0, nil, errors.New("missing patch ops")
	}
	metricsOn := b.Rec.On && sample != nil
	if metricsOn {
		sample.Fest, sample.Game = scope.FestID, scope.GameID
		sample.Ops = len(req.Ops)
	}

	// Flat games keep their state on the 'main' match; a game without one (EK)
	// PATCHes its game-level auxiliary blob instead.
	var gameType, stateJSON string
	var matchID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
select g.game_type, m.id, coalesce(m.state_json, coalesce(g.state_json, '{}'))
from games g left join matches m on m.game_id = g.id and m.code = 'main'
where g.fest_id = ? and g.id = ?`,
		scope.FestID, scope.GameID).Scan(&gameType, &matchID, &stateJSON); err != nil {
		return nil, 0, nil, err
	}
	if stateJSON == "" {
		stateJSON = "{}"
	}

	var root any
	tUnmarshal := metrics.NowIf(metricsOn)
	if err := json.Unmarshal([]byte(stateJSON), &root); err != nil {
		return nil, 0, nil, fmt.Errorf("stored game state is invalid json: %w", err)
	}
	if metricsOn {
		sample.Unmarshal = time.Since(tUnmarshal)
	}
	if root == nil {
		root = map[string]any{}
	}

	blobOps := make([]store.BlobOp, 0, len(req.Ops))
	for _, op := range req.Ops {
		if op.Op != "" && op.Op != "set" {
			return nil, 0, nil, fmt.Errorf("unsupported patch op %q", op.Op)
		}
		path, err := edit.ParseJSONPatchPath(op.Path)
		if err != nil {
			return nil, 0, nil, err
		}
		if edit.PatchPathTouchesRatingRoster(gameType, path) {
			return nil, 0, nil, edit.ErrRatingRosterImmutable
		}
		value, err := edit.DecodePatchValue(op.Value)
		if err != nil {
			return nil, 0, nil, err
		}
		root, err = edit.ApplyJSONSet(root, path, value)
		if err != nil {
			return nil, 0, nil, err
		}
		blobOps = append(blobOps, store.BlobOp{Kind: "set", Path: pointerFromSegments(path), Value: value, Parts: op.Path})
	}

	tMarshal := metrics.NowIf(metricsOn)
	next, err := json.Marshal(root)
	if err != nil {
		return nil, 0, nil, err
	}
	tDB := metrics.NowIf(metricsOn)
	if metricsOn {
		sample.Marshal = tDB.Sub(tMarshal)
	}
	if matchID.Valid {
		if _, err := tx.ExecContext(ctx, `
update matches set state_json = ? where id = ?`, string(next), matchID.Int64); err != nil {
			return nil, 0, nil, err
		}
		if _, err := tx.ExecContext(ctx, `
update games set updated_at = ? where fest_id = ? and id = ?`,
			util.UtcNow(), scope.FestID, scope.GameID); err != nil {
			return nil, 0, nil, err
		}
		if err := festwrite.JournalMatchPatchTx(ctx, tx, matchID.Int64, blobOps); err != nil {
			return nil, 0, nil, err
		}
	} else {
		result, err := tx.ExecContext(ctx, `
update games set state_json = ?, updated_at = ? where fest_id = ? and id = ?`,
			string(next), util.UtcNow(), scope.FestID, scope.GameID)
		if err != nil {
			return nil, 0, nil, err
		}
		if n, err := result.RowsAffected(); err != nil {
			return nil, 0, nil, err
		} else if n == 0 {
			return nil, 0, nil, sql.ErrNoRows
		}
	}
	revision, err := festwrite.BumpFestRevisionTx(ctx, tx, scope.FestID, "game:state-patch", payload)
	if err != nil {
		return nil, 0, nil, err
	}
	if metricsOn {
		sample.DB = time.Since(tDB)
		sample.Bytes = len(next)
	}
	// req.Ops came from valid request JSON, so this marshal effectively never
	// fails; on the off chance it does, signal a snapshot fallback with nil ops.
	opsJSON, mErr := json.Marshal(req.Ops)
	if mErr != nil {
		opsJSON = nil
	}
	return next, revision, opsJSON, nil
}
