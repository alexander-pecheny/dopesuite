package migrate

import (
	"context"
	"database/sql"
	"fmt"

	"dope/dope/storage/journal"
	"dope/dope/storage/store"
)

// Unified-model data conversion (docs/unified-model.md, ADR-0004): legacy
// relational EK state (themes/answers rows) becomes the per-match state blob.
// Idempotent — a match whose legacy rows are gone, or that never had any, is
// left untouched, so the converter can run at every startup until the legacy
// tables are finally dropped.

// RunUnifyConversion performs the unified-model data conversion on a legacy
// database: EK relational state and checkpoints become match blobs, and the
// themes/answers tables are dropped. No-op once the tables are gone. Journal
// triggers are dropped first so conversion churn is never journaled as edits;
// the caller's bootstrap reinstalls them right after (fingerprint cleared by
// the drop).
func RunUnifyConversion(db *sql.DB) error {
	ctx := context.Background()
	var hasThemes int
	if err := db.QueryRowContext(ctx,
		`select count(*) from sqlite_master where type = 'table' and name = 'themes'`).Scan(&hasThemes); err != nil {
		return err
	}
	if hasThemes == 0 {
		return nil
	}
	if err := guardNoReplayableLegacyRecords(ctx, db); err != nil {
		return err
	}
	if err := journal.DropTriggers(db); err != nil {
		return err
	}
	if err := rewriteCheckpoints(ctx, db); err != nil {
		return err
	}
	if err := ConvertEKMatchBlobs(ctx, db); err != nil {
		return err
	}
	for _, stmt := range []string{`drop table if exists answers`, `drop table if exists themes`} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// guardNoReplayableLegacyRecords refuses to convert while any hot journal
// record above its game's earliest checkpoint still references themes/answers:
// such a record would be replayed by revert, and this converter deliberately
// carries no relational-replay simulation (prod has zero such records —
// verified against the 2026-07-23 snapshot). Records below every checkpoint
// are display-only and stay as they are.
func guardNoReplayableLegacyRecords(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx, `
select count(*) from journal j
where (json_extract(j.payload, '$.t') in ('themes', 'answers'))
  and j.game_id is not null
  and j.id > coalesce((select min(seq) from journal_checkpoint c where c.game_id = j.game_id), j.id)`).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("unify: %d replayable journal records still reference themes/answers; convert them first", count)
	}
	return nil
}

// rewriteCheckpoints folds the themes/answers rows inside every stored game
// checkpoint into their matches rows' state_json, so restoring an old
// checkpoint reproduces the unified shape.
func rewriteCheckpoints(ctx context.Context, db *sql.DB) error {
	type cpRow struct {
		gameID int64
		seq    int64
		blob   []byte
	}
	rows, err := store.CollectRows(ctx, db,
		`select game_id, seq, state_blob from journal_checkpoint`, nil,
		func(rows *sql.Rows) (cpRow, error) {
			var r cpRow
			return r, rows.Scan(&r.gameID, &r.seq, &r.blob)
		})
	if err != nil {
		return err
	}
	for _, row := range rows {
		cp, err := journal.DecodeGameCheckpoint(row.blob)
		if err != nil {
			return fmt.Errorf("checkpoint game %d seq %d: %w", row.gameID, row.seq, err)
		}
		if !foldCheckpointThemes(cp) {
			continue
		}
		encoded, err := journal.EncodeGameCheckpoint(cp)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx,
			`update journal_checkpoint set state_blob = ? where game_id = ? and seq = ?`,
			encoded, row.gameID, row.seq); err != nil {
			return err
		}
	}
	return nil
}

// foldCheckpointThemes converts a decoded checkpoint in place; reports whether
// it contained legacy rows.
func foldCheckpointThemes(cp *journal.GameCheckpoint) bool {
	themes := cp.Tables["themes"]
	answers := cp.Tables["answers"]
	if themes == nil && answers == nil {
		return false
	}
	answersByTheme := map[int64][]map[string]any{}
	for _, a := range answers {
		if id, ok := asInt(a["theme_id"]); ok {
			answersByTheme[id] = append(answersByTheme[id], a)
		}
	}
	blobs := map[int64]*store.MatchBlob{}
	for _, th := range themes {
		matchID, ok1 := asInt(th["match_id"])
		teamID, ok2 := asInt(th["team_id"])
		themeIndex, ok3 := asInt(th["theme_index"])
		kind, _ := th["kind"].(string)
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		blob := blobs[matchID]
		if blob == nil {
			blob = &store.MatchBlob{}
			blobs[matchID] = blob
		}
		blob.EnsureTheme(teamID, kind, int(themeIndex))
		if player, ok := asInt(th["player_id"]); ok && player != 0 {
			blob.SetPlayer(teamID, kind, int(themeIndex), player)
		}
		if themeID, ok := asInt(th["id"]); ok {
			for _, a := range answersByTheme[themeID] {
				index, okI := asInt(a["answer_index"])
				mark, _ := a["mark"].(string)
				if okI {
					blob.SetAnswer(teamID, kind, int(themeIndex), int(index), mark)
				}
			}
		}
	}
	for _, matchRow := range cp.Tables["matches"] {
		id, ok := asInt(matchRow["id"])
		if !ok {
			continue
		}
		if blob := blobs[id]; blob != nil {
			if encoded, err := blob.JSON(); err == nil {
				matchRow["state_json"] = encoded
			}
		} else if _, exists := matchRow["state_json"]; !exists {
			matchRow["state_json"] = "{}"
		}
	}
	delete(cp.Tables, "themes")
	delete(cp.Tables, "answers")
	return true
}

func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	}
	return 0, false
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
