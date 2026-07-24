package migrate

import (
	"context"
	"database/sql"

	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
)

// pinBackfillPage bounds how many matches one pass loads. A prior migration
// OOM-killed prod by reading a whole table at once; this one holds at most one
// page of match ids and one match's blob in memory at a time.
const pinBackfillPage = 64

// RunPinBackfill moves host place pins out of match_results.place_override and
// into the per-match state blob, where Protocol state lives (ADR-0005). Each
// match converts in its own transaction, and clearing the column as it goes
// makes the pass self-terminating and safe to resume after an interruption.
func RunPinBackfill(db *sql.DB) error {
	ctx := context.Background()
	for {
		ids, err := pendingPinMatches(ctx, db)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, matchID := range ids {
			if err := backfillMatchPins(ctx, db, matchID); err != nil {
				return err
			}
		}
	}
}

func pendingPinMatches(ctx context.Context, db *sql.DB) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `
select distinct match_id from match_results
where place_override is not null
order by match_id limit ?`, pinBackfillPage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func backfillMatchPins(ctx context.Context, db *sql.DB, matchID int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	pins := map[int64]float64{}
	rows, err := tx.QueryContext(ctx,
		`select team_id, place_override from match_results where match_id = ? and place_override is not null`, matchID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var teamID int64
		var place float64
		if err := rows.Scan(&teamID, &place); err != nil {
			rows.Close()
			return err
		}
		pins[teamID] = place
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	ops, err := store.MutateMatchBlobTx(ctx, tx, matchID, func(blob *store.MatchBlob) error {
		for teamID, place := range pins {
			blob.SetPin(teamID, &place)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Journal the move so a replay of this fest rebuilds the pins too — without
	// it, history predating the migration reconstructs blobs with no pins.
	if err := festwrite.JournalMatchPatchTx(ctx, tx, matchID, ops); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`update match_results set place_override = null where match_id = ?`, matchID); err != nil {
		return err
	}
	return tx.Commit()
}
