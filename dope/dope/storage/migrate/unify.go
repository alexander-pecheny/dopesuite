package migrate

import (
	"context"
	"database/sql"

	"dope/dope/storage/journal"
	"dope/dope/storage/store"
)

// Unified-model data conversion (docs/unified-model.md, ADR-0004): legacy
// relational EK state (themes/answers rows) becomes the per-match state blob.
// Idempotent — a match whose legacy rows are gone, or that never had any, is
// left untouched, so the converter can run at every startup until the legacy
// tables are finally dropped.

// RunUnifyConversion performs the unified-model data conversion on a legacy
// database. No-op when nothing is left to convert. Journal triggers are
// dropped first so conversion churn is never journaled as edits; the caller's
// bootstrap reinstalls them right after (fingerprint cleared by the drop).
func RunUnifyConversion(db *sql.DB) error {
	ctx := context.Background()
	var pending int
	if err := db.QueryRowContext(ctx, `select count(*) from themes`).Scan(&pending); err != nil {
		return err
	}
	if pending == 0 {
		return nil
	}
	if err := journal.DropTriggers(db); err != nil {
		return err
	}
	return ConvertEKMatchBlobs(ctx, db)
}

// ConvertEKMatchBlobs rewrites every match that still has themes rows into a
// matches.state_json blob built from those rows and their answers.
func ConvertEKMatchBlobs(ctx context.Context, db *sql.DB) error {
	matchIDs, err := store.CollectRows(ctx, db,
		`select distinct match_id from themes order by match_id`, nil,
		func(rows *sql.Rows) (int64, error) {
			var id int64
			return id, rows.Scan(&id)
		})
	if err != nil {
		return err
	}
	for _, matchID := range matchIDs {
		if err := convertOneMatch(ctx, db, matchID); err != nil {
			return err
		}
	}
	return nil
}

func convertOneMatch(ctx context.Context, db *sql.DB, matchID int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	type themeRow struct {
		id     int64
		teamID int64
		kind   string
		index  int
		player int64
	}
	themes, err := store.CollectRows(ctx, tx,
		`select id, team_id, kind, theme_index, coalesce(player_id, 0) from themes where match_id = ?`,
		[]any{matchID}, func(rows *sql.Rows) (themeRow, error) {
			var r themeRow
			return r, rows.Scan(&r.id, &r.teamID, &r.kind, &r.index, &r.player)
		})
	if err != nil {
		return err
	}
	blob := store.MatchBlob{}
	for _, theme := range themes {
		if theme.player != 0 {
			blob.SetPlayer(theme.teamID, theme.kind, theme.index, theme.player)
		}
		type answerRow struct {
			index int
			mark  string
		}
		answers, err := store.CollectRows(ctx, tx,
			`select answer_index, mark from answers where theme_id = ?`,
			[]any{theme.id}, func(rows *sql.Rows) (answerRow, error) {
				var r answerRow
				return r, rows.Scan(&r.index, &r.mark)
			})
		if err != nil {
			return err
		}
		blob.EnsureTheme(theme.teamID, theme.kind, theme.index)
		for _, a := range answers {
			blob.SetAnswer(theme.teamID, theme.kind, theme.index, a.index, a.mark)
		}
	}
	encoded, err := blob.JSON()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `update matches set state_json = ? where id = ?`, encoded, matchID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from answers where theme_id in (select id from themes where match_id = ?)`, matchID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from themes where match_id = ?`, matchID); err != nil {
		return err
	}
	return tx.Commit()
}
