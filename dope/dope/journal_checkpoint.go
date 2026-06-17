package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Revert and replay are scoped per GAME, not per fest: games are independent
// units (the one cross-game link, EK seeding from a KSI ranking, is a one-time
// non-reactive import that lives as a single entry in EK's own log). A
// checkpoint is therefore a full snapshot of a single game's mutable state, so
// replay never has to start from the literal beginning of time and revert can
// reconstruct any past point by restoring the nearest checkpoint and replaying
// the game's forward edits up to the target.
//
// gameScopedTables lists the tables a game owns, in FK-safe INSERT order
// (reverse for delete). Each carries a predicate selecting that game's rows.
// The `games` row itself is handled separately (only its state_json is part of
// the snapshot — the rest of the row is identity/config).
var gameScopedTables = []struct {
	table string
	scope string // WHERE clause, single ? bound to game_id
}{
	{"stages", "game_id = ?"},
	{"matches", "game_id = ?"},
	{"match_slots", "match_id in (select id from matches where game_id = ?)"},
	{"themes", "match_id in (select id from matches where game_id = ?)"},
	{"answers", "theme_id in (select id from themes where match_id in (select id from matches where game_id = ?))"},
	{"match_results", "match_id in (select id from matches where game_id = ?)"},
	{"reseed_entries", "stage_id in (select id from stages where game_id = ?)"},
	{"game_assignments", "game_id = ?"},
	{"game_teams", "game_id = ?"},
	{"game_players", "game_id = ?"},
	{"game_team_players", "game_id = ?"},
	{"game_player_team_overrides", "game_id = ?"},
}

// gameCheckpoint is the decoded snapshot of one game's state.
type gameCheckpoint struct {
	StateJSON string                      `json:"state_json"`
	Tables    map[string][]map[string]any `json:"tables"`
}

type rowQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// captureGameCheckpoint snapshots a game's full mutable state.
func captureGameCheckpoint(ctx context.Context, q rowQuerier, gameID int64) (*gameCheckpoint, error) {
	cp := &gameCheckpoint{Tables: map[string][]map[string]any{}}

	// state_json (the only part of the games row that is mutable state).
	srow, err := q.QueryContext(ctx, `select coalesce(state_json, '{}') from games where id = ?`, gameID)
	if err != nil {
		return nil, err
	}
	if srow.Next() {
		if err := srow.Scan(&cp.StateJSON); err != nil {
			srow.Close()
			return nil, err
		}
	}
	srow.Close()

	for _, t := range gameScopedTables {
		rows, err := scanRowsAsMaps(ctx, q, t.table, t.scope, gameID)
		if err != nil {
			return nil, fmt.Errorf("capture %s: %w", t.table, err)
		}
		cp.Tables[t.table] = rows
	}
	return cp, nil
}

// scanRowsAsMaps selects every column of the scoped rows into ordered maps.
func scanRowsAsMaps(ctx context.Context, q rowQuerier, table, scope string, gameID int64) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`select * from %s where %s order by rowid`, table, scope), gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c] = normalizeSQLScan(vals[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// normalizeSQLScan coerces driver scan values to JSON-friendly, round-trippable
// types (int64/float64/string/nil; []byte -> base64-safe via string is avoided
// — blobs are rare in these tables, kept as []byte).
func normalizeSQLScan(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return x
	}
}

// encodeGameCheckpoint serializes + compresses a checkpoint for storage.
func encodeGameCheckpoint(cp *gameCheckpoint) ([]byte, error) {
	raw, err := json.Marshal(cp)
	if err != nil {
		return nil, err
	}
	return zstdCompress(raw), nil
}

func decodeGameCheckpoint(blob []byte) (*gameCheckpoint, error) {
	raw, err := zstdDecompress(blob)
	if err != nil {
		return nil, err
	}
	var cp gameCheckpoint
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&cp); err != nil {
		return nil, err
	}
	// Coerce json.Number back to int64/float64 for stable SQL binding.
	for _, rows := range cp.Tables {
		for _, m := range rows {
			for k, val := range m {
				m[k] = jsonToSQLValue(val)
			}
		}
	}
	return &cp, nil
}

// restoreGameCheckpoint resets a game to a captured snapshot: it deletes the
// game's current scoped rows (reverse FK order, deferred FK checks) and
// re-inserts the snapshot rows, then restores state_json.
func restoreGameCheckpoint(ctx context.Context, tx *sql.Tx, gameID int64, cp *gameCheckpoint) error {
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	// Delete in reverse FK order.
	for i := len(gameScopedTables) - 1; i >= 0; i-- {
		t := gameScopedTables[i]
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`delete from %s where %s`, t.table, t.scope), gameID); err != nil {
			return fmt.Errorf("clear %s: %w", t.table, err)
		}
	}
	// Re-insert in forward FK order.
	for _, t := range gameScopedTables {
		for _, row := range cp.Tables[t.table] {
			if err := insertRow(ctx, tx, t.table, row); err != nil {
				return fmt.Errorf("restore %s: %w", t.table, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `update games set state_json = ? where id = ?`, cp.StateJSON, gameID); err != nil {
		return err
	}
	return nil
}

// backfillGameCheckpoints writes a genesis checkpoint (at seq 0 = current state)
// for any game that has none yet, so per-game derived revert has an anchor for
// edits made from now on. Idempotent. Run at startup after triggers are
// installed.
func backfillGameCheckpoints(db *sql.DB) error {
	ctx := context.Background()
	// Genesis = current state at the current journal high-water mark. Keying the
	// checkpoint at max(journal.id) (not 0) means any converted pre-genesis
	// history has a lower seq, so revert-to-a-historical-point finds no
	// checkpoint at-or-before it and declines rather than replaying historical
	// row-ops onto the current state (which would corrupt it). New edits get
	// higher ids and revert normally.
	var hwm int64
	if err := db.QueryRowContext(ctx, `select coalesce(max(id), 0) from journal`).Scan(&hwm); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `
select id from games where id not in (select game_id from journal_checkpoint)`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := writeGameCheckpoint(ctx, tx, id, hwm); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// writeGameCheckpoint captures and persists a checkpoint for a game at seq.
func writeGameCheckpoint(ctx context.Context, tx *sql.Tx, gameID, seq int64) error {
	cp, err := captureGameCheckpoint(ctx, tx, gameID)
	if err != nil {
		return err
	}
	blob, err := encodeGameCheckpoint(cp)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
insert or replace into journal_checkpoint(game_id, seq, state_blob, dsl_version, created_at)
values(?, ?, ?, ?, ?)`, gameID, seq, blob, journalDSLVersion, utcNow())
	return err
}
