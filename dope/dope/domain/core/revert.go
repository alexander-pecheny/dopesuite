package core

import (
	"context"
	"database/sql"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/journal"
	"dope/dope/storage/store"
	"fmt"
)

// Per-game revert is DERIVED from the forward journal rather than from stored
// before-images: to put a game back to the state it had at journal point
// targetID, restore the newest checkpoint at or before that point and replay the
// game's forward row-ops up to it. Edits after targetID are simply not replayed,
// which reverts them. The revert itself is recorded as a new forward journal
// entry (so it is part of history and itself undoable).
//
// This needs the game's fine row-ops (captured by the journal row-op triggers)
// plus periodic checkpoints. Bulk/structural ops don't emit row-ops; instead a
// checkpoint is written right after them, so replay never has to cross one.

// gameRowOp is a decoded forward row-op for replay.
type gameRowOp struct {
	id    int64
	op    journal.Op
	table string
	row   map[string]any
}

// loadGameRowOpsBetween returns a game's row-ops with id in (afterID, throughID],
// ordered, decoded from the hot journal. (Cold segments are folded back by the
// archiver only for settled history; a checkpoint always sits at or after the
// last archived point, so revert never needs to read segments.)
func loadGameRowOpsBetween(ctx context.Context, q store.Queryer, gameID, afterID, throughID int64) ([]gameRowOp, error) {
	rows, err := q.QueryContext(ctx, `
select id, op, payload from journal
where game_id = ? and id > ? and id <= ? and op in (?, ?, ?)
order by id`, gameID, afterID, throughID, int(journal.OpRowIns), int(journal.OpRowSet), int(journal.OpRowDel))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []gameRowOp
	for rows.Next() {
		var (
			id      int64
			op      int
			payload []byte
		)
		if err := rows.Scan(&id, &op, &payload); err != nil {
			return nil, err
		}
		table, row, err := journal.DecodeRowOpJSON(payload)
		if err != nil {
			return nil, fmt.Errorf("decode row op %d: %w", id, err)
		}
		out = append(out, gameRowOp{id: id, op: journal.Op(op), table: table, row: row})
	}
	return out, rows.Err()
}

// nearestCheckpointAtOrBefore returns the newest checkpoint for a game with
// journal id <= throughID. The checkpoint's `seq` column stores the journal id
// at capture time. Returns (nil, ok=false) if none.
func nearestCheckpointAtOrBefore(ctx context.Context, q store.Queryer, gameID, throughID int64) (*journal.GameCheckpoint, int64, bool, error) {
	rows, err := q.QueryContext(ctx, `
select seq, state_blob from journal_checkpoint
where game_id = ? and seq <= ? order by seq desc limit 1`, gameID, throughID)
	if err != nil {
		return nil, 0, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, 0, false, rows.Err()
	}
	var seq int64
	var blob []byte
	if err := rows.Scan(&seq, &blob); err != nil {
		return nil, 0, false, err
	}
	cp, err := journal.DecodeGameCheckpoint(blob)
	if err != nil {
		return nil, 0, false, err
	}
	return cp, seq, true, nil
}

// ReconstructGameStateAt restores (within tx) the game to its state as of
// journal point throughID, by restoring the nearest checkpoint <= throughID and
// replaying the game's row-ops up to throughID. Used by both revert and
// historical inspection.
func ReconstructGameStateAt(ctx context.Context, tx *sql.Tx, gameID, throughID int64) error {
	cp, cpID, ok, err := nearestCheckpointAtOrBefore(ctx, tx, gameID, throughID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no checkpoint at or before %d for game %d (cannot reconstruct)", throughID, gameID)
	}
	if err := journal.RestoreGameCheckpoint(ctx, tx, gameID, cp); err != nil {
		return fmt.Errorf("restore checkpoint: %w", err)
	}
	ops, err := loadGameRowOpsBetween(ctx, tx, gameID, cpID, throughID)
	if err != nil {
		return err
	}
	rp := journal.NewReplayer(nil)
	for _, o := range ops {
		if err := rp.ApplyRowMap(ctx, tx, o.op, o.table, o.row); err != nil {
			return fmt.Errorf("replay row op %d (%s %s): %w", o.id, o.op, o.table, err)
		}
	}
	return nil
}

// revertGameToPoint reverts a game to journal point targetID. It reconstructs
// the game's state at targetID and records the revert as a new journal entry.
// Returns the new fest revision (seq).
func (e *Engine) RevertGameToPoint(reqCtx context.Context, festID, gameID, targetID int64) (int64, error) {
	var revision int64
	err := e.WithWriteTx(reqCtx, festID, "game-revert", func(ctx context.Context, tx *sql.Tx) error {
		if err := ReconstructGameStateAt(ctx, tx, gameID, targetID); err != nil {
			return err
		}
		// Record the revert as a forward event and snapshot the reverted state so
		// future reverts have a nearby checkpoint and never re-cross this point.
		var err error
		revision, err = festwrite.BumpFestRevisionTx(ctx, tx, festID,
			"game:revert", util.MustJSON(map[string]any{"gameID": gameID, "target": targetID}))
		if err != nil {
			return err
		}
		return journal.WriteGameCheckpoint(ctx, tx, gameID, JournalIDForSeqTx(ctx, tx))
	})
	return revision, err
}

// JournalIDForSeqTx returns the id the next journal row will get (max id so the
// post-revert checkpoint is keyed just at/after the revert's own entries).
func JournalIDForSeqTx(ctx context.Context, tx *sql.Tx) int64 {
	var id sql.NullInt64
	_ = tx.QueryRowContext(ctx, `select max(id) from journal`).Scan(&id)
	if id.Valid {
		return id.Int64
	}
	return 0
}
