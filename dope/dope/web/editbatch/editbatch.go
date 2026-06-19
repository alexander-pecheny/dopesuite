// Package editbatch holds the editor-side write-batching runtime. Every
// game-state PATCH used to take the global write mutex, run its own transaction
// and fsync-commit on its own — so N concurrent editors hammering a grid
// produced N lock acquisitions and N commits per burst, the dominant source of
// write contention (and the shape of the 2026-06-13 freeze, where one stuck
// write pinned the lock for everyone).
//
// Batcher mirrors the viewer-side delta coalescing (deltaCoalesceWindow) on the
// WRITE side: edits to one game within editBatchWindow are collected and applied
// together under a SINGLE lock acquisition in ONE transaction with ONE
// commit/fsync, then broadcast once as a merged delta. That caps writes per
// actively-edited game at ~1000/editBatchWindow ≈ 6/sec regardless of how many
// editors are typing, sharply cutting lock churn and DB load.
//
// Crucially this changes only the TRANSACTION boundary, not the per-edit
// semantics: inside the shared transaction each edit is still replayed in
// arrival order with its own actor re-seeded into audit_ctx, its own row-op
// triggers firing, its own journal event and its own fest revision — so audit
// history, attribution, replay and undo/redo are byte-for-byte unchanged. An
// edit with invalid ops fails on its own (it errors before writing anything)
// without poisoning the others in the window.
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
	"sync"
	"time"

	"dope/dope/domain/core"
	"dope/dope/domain/edit"
	"dope/dope/platform/metrics"
	"dope/dope/platform/realtime"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
)

// editBatchWindow bounds how long game-state edits buffer before the batch is
// applied. Matches the viewer-side deltaCoalesceWindow so editor and viewer
// pacing are symmetric; 150ms is short enough not to feel laggy while long
// enough to fold a burst of co-editors into one write.
const editBatchWindow = 150 * time.Millisecond

// editResult is what a queued edit gets back once its window commits.
type editResult struct {
	next     []byte
	revision int64
	err      error
}

// editJob is one pending PATCH awaiting its window's flush.
type editJob struct {
	scope    core.FestScope
	req      edit.PatchRequest
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

// SubmitEdit queues a game-state PATCH for batched application and blocks until
// its window commits (or the requesting context is canceled). The edit is
// applied regardless of whether the caller keeps waiting — once queued it is a
// durable intent — so a client disconnect never drops an edit, it only stops us
// reporting the result.
func (b *Batcher) SubmitEdit(reqCtx context.Context, scope core.FestScope, req edit.PatchRequest, payload string, sample *metrics.Sample) ([]byte, int64, error) {
	// Detached, attribution-carrying context so the write stays attributed to the
	// acting user/request and is bounded by festwrite.WriteTxTimeout, and is NOT aborted by
	// this client's disconnect (it may be carrying co-editors' edits too). Owned
	// by the flusher, which cancels it once the batch has been applied.
	auditCtx, cancel := festwrite.AuditDetachedContext(reqCtx, scope.FestID)
	job := &editJob{
		scope:   scope,
		req:     req,
		payload: payload,
		ctx:     auditCtx,
		cancel:  cancel,
		sample:  sample,
		done:    make(chan editResult, 1),
	}
	if b.Rec.On {
		job.submitAt = time.Now()
	}
	b.enqueueEdit(job)

	select {
	case res := <-job.done:
		return res.next, res.revision, res.err
	case <-reqCtx.Done():
		return nil, 0, reqCtx.Err()
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

// applyEditBatch applies a window's edits under a single lock acquisition in one
// transaction, then resolves every job and broadcasts the merged delta once. It
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
		// replay the edit. A job whose ops are invalid errors before any write, so
		// it cannot corrupt the shared transaction — just skip it.
		_ = festwrite.SeedAuditCtx(job.ctx, tx)
		next, revision, opsJSON, err := b.applyOneEditTx(job.ctx, tx, scope, job.req, job.payload, job.sample)
		if err != nil {
			results[i] = editResult{err: err}
			continue
		}
		results[i] = editResult{next: next, revision: revision}
		finalNext = next
		lastRevision = revision
		committedAny = true
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

	if err := tx.Commit(); err != nil {
		unlock()
		// Commit failed: the whole window failed atomically. Even the edits that
		// applied in memory did not persist, so report the failure to all.
		b.failBatch(jobs, err)
		return
	}
	hold := time.Since(acquired)
	unlock()

	for i, job := range jobs {
		b.deliver(job, results[i], metricsOn, acquired, hold)
	}

	// One merged broadcast for the whole window — the batching already provided
	// the coalescing window, so editors and viewers both get it immediately.
	scopeKey := fmt.Sprintf("game-state:%d", scope.GameID)
	if needSnapshot {
		b.Eng.BroadcastState(scope.FestID, scopeKey, lastRevision, finalNext)
	} else {
		b.Eng.BroadcastBatchedDelta(scope.FestID, scopeKey, lastRevision, realtime.MergeOpsArrays(mergedOps))
	}
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

// applyOneEditTx applies one game-state PATCH within an existing transaction:
// read the current state, apply the ops, write it back and bump the fest
// revision (which appends the journal event). It performs no locking, no
// begin/commit — the batcher owns those — so several edits share one tx. It
// returns the new state, the assigned revision and the marshaled ops (for the
// merged broadcast). Ops are validated and applied before any write, so a
// returned error means nothing was written for this edit.
func (b *Batcher) applyOneEditTx(ctx context.Context, tx *sql.Tx, scope core.FestScope, req edit.PatchRequest, payload string, sample *metrics.Sample) ([]byte, int64, []byte, error) {
	if len(req.Ops) == 0 {
		return nil, 0, nil, errors.New("missing patch ops")
	}
	metricsOn := b.Rec.On && sample != nil
	if metricsOn {
		sample.Fest, sample.Game = scope.FestID, scope.GameID
		sample.Ops = len(req.Ops)
	}

	var gameType, stateJSON string
	if err := tx.QueryRowContext(ctx, `
select game_type, state_json from games where fest_id = ? and id = ?`,
		scope.FestID, scope.GameID).Scan(&gameType, &stateJSON); err != nil {
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
	result, err := tx.ExecContext(ctx, `
update games set state_json = ?, updated_at = ? where fest_id = ? and id = ?`,
		string(next), util.UtcNow(), scope.FestID, scope.GameID)
	if err != nil {
		return nil, 0, nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, 0, nil, err
	}
	if n == 0 {
		return nil, 0, nil, sql.ErrNoRows
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
