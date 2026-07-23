package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"dope/dope/storage/journal"
	"dope/dope/storage/store"
)

// Unified-model data conversion (docs/unified-model.md, ADR-0004): legacy
// relational EK state (themes/answers rows) becomes the per-match state blob.
// Runs once, as schema version 18 (server/db.go); each sub-step still checks
// its legacy shape so the one run is safe on any database state.
//
// Journal scans gate json_extract behind the row-op check via iif (guaranteed
// evaluation order, unlike AND terms): only row ops (1-3) carry the {"t","r"}
// JSON deltas — event records (op >= 64) may be non-JSON (OpEvGeneric
// length-prefixes the event type), and json_extract on one aborts the scan.

// RunUnifyConversion performs the unified-model data conversion on a legacy
// database: EK relational state and checkpoints become match blobs, the
// themes/answers tables are dropped, and legacy records are rewritten in the
// hot journal, the checkpoints and the cold segments. Journal triggers are
// dropped first so conversion churn is never journaled as edits; the caller's
// bootstrap reinstalls them right after (fingerprint cleared by the drop).
func RunUnifyConversion(db *sql.DB) error {
	ctx := context.Background()
	var hasThemes, flatPending, hasReseedEntries int
	if err := db.QueryRowContext(ctx,
		`select count(*) from sqlite_master where type = 'table' and name = 'themes'`).Scan(&hasThemes); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `
select count(*) from games g
where g.game_type in ('od', 'ksi', 'si')
  and not exists (select 1 from matches m where m.game_id = g.id and m.code = 'main')`).Scan(&flatPending); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx,
		`select count(*) from sqlite_master where type = 'table' and name = 'reseed_entries'`).Scan(&hasReseedEntries); err != nil {
		return err
	}
	if hasThemes == 0 && flatPending == 0 && hasReseedEntries == 0 {
		return convertSegments(ctx, db)
	}
	if err := journal.DropTriggers(db); err != nil {
		return err
	}
	if hasThemes != 0 {
		if err := guardNoReplayableLegacyRecords(ctx, db); err != nil {
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
	}
	if flatPending != 0 {
		if err := convertFlatGames(ctx, db); err != nil {
			return err
		}
	}
	if hasReseedEntries != 0 {
		if err := convertStageStandings(ctx, db); err != nil {
			return err
		}
	}
	return convertSegments(ctx, db)
}

// convertStageStandings folds the legacy reseed_entries table into
// stage_standings (its generalisation: every ranking stage writes here, keyed
// by participant instead of team). Journal records and checkpoints rename
// mechanically — same rows, new table and column names.
func convertStageStandings(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
insert or replace into stage_standings(stage_id, rank, participant_id, metrics_json)
select stage_id, rank, team_id, metrics_json from reseed_entries`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `drop table reseed_entries`); err != nil {
		return err
	}
	type rec struct {
		id      int64
		payload string
	}
	records, err := store.CollectRows(ctx, db, `
select id, payload from journal
where iif(op in (1, 2, 3), json_extract(payload, '$.t'), null) = 'reseed_entries'`,
		nil, func(rows *sql.Rows) (rec, error) {
			var r rec
			return r, rows.Scan(&r.id, &r.payload)
		})
	if err != nil {
		return err
	}
	for _, r := range records {
		var decoded struct {
			Table string         `json:"t"`
			Row   map[string]any `json:"r"`
		}
		if err := json.Unmarshal([]byte(r.payload), &decoded); err != nil {
			continue
		}
		if teamID, ok := decoded.Row["team_id"]; ok {
			decoded.Row["participant_id"] = teamID
			delete(decoded.Row, "team_id")
		}
		out, err := json.Marshal(map[string]any{"t": "stage_standings", "r": decoded.Row})
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `update journal set payload = ? where id = ?`, string(out), r.id); err != nil {
			return err
		}
	}
	return rewriteCheckpointStandings(ctx, db)
}

// rewriteCheckpointStandings renames the reseed_entries table dump inside every
// stored checkpoint to the stage_standings shape.
func rewriteCheckpointStandings(ctx context.Context, db *sql.DB) error {
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
		entries, ok := cp.Tables["reseed_entries"]
		if !ok {
			continue
		}
		for _, entry := range entries {
			if teamID, exists := entry["team_id"]; exists {
				entry["participant_id"] = teamID
				delete(entry, "team_id")
			}
		}
		cp.Tables["stage_standings"] = entries
		delete(cp.Tables, "reseed_entries")
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

// convertFlatGames gives every ЧГК-family game (od/ksi/si) its unified shape:
// one stage (kind 'matches', code 'main') holding one match whose state_json
// carries what used to live on games.state_json. Journal records touching a
// flat game's state redirect onto the match row; checkpoints fold their
// StateJSON into the injected match row. games.state_json itself survives as
// the game-level auxiliary blob (EK seed-import staging) and is blanked for
// converted flat games.
func convertFlatGames(ctx context.Context, db *sql.DB) error {
	type flatGame struct {
		id     int64
		festID int64
		title  string
		state  string
		status string
	}
	games, err := store.CollectRows(ctx, db, `
select id, fest_id, title, coalesce(state_json, '{}'), status from games
where game_type in ('od', 'ksi', 'si')`, nil,
		func(rows *sql.Rows) (flatGame, error) {
			var g flatGame
			return g, rows.Scan(&g.id, &g.festID, &g.title, &g.state, &g.status)
		})
	if err != nil {
		return err
	}
	matchOf := map[int64]int64{}
	stageOf := map[int64]int64{}
	for _, g := range games {
		stageID, matchID, err := ensureFlatMatch(ctx, db, g.id, g.festID, g.title, g.status)
		if err != nil {
			return fmt.Errorf("flat match for game %d: %w", g.id, err)
		}
		matchOf[g.id] = matchID
		stageOf[g.id] = stageID
		if _, err := db.ExecContext(ctx,
			`update matches set state_json = ? where id = ?`, g.state, matchID); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx,
			`update games set state_json = '{}' where id = ?`, g.id); err != nil {
			return err
		}
	}
	if err := rewriteGameStateRecords(ctx, db, matchOf); err != nil {
		return err
	}
	return rewriteFlatCheckpoints(ctx, db, stageOf, matchOf)
}

// ensureFlatMatch creates (or finds) the single stage+match of a flat game.
func ensureFlatMatch(ctx context.Context, db *sql.DB, gameID, festID int64, title, status string) (int64, int64, error) {
	var stageID int64
	err := db.QueryRowContext(ctx,
		`select id from stages where game_id = ? and code = 'main'`, gameID).Scan(&stageID)
	if err == sql.ErrNoRows {
		res, insErr := db.ExecContext(ctx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json)
values(?, ?, 'main', '', 'matches', 'matches', 1, 'active', '{}')`, festID, gameID)
		if insErr != nil {
			return 0, 0, insErr
		}
		stageID, insErr = res.LastInsertId()
		if insErr != nil {
			return 0, 0, insErr
		}
	} else if err != nil {
		return 0, 0, err
	}
	var matchID int64
	matchStatus := "active"
	if status == "finished" {
		matchStatus = "finished"
	}
	err = db.QueryRowContext(ctx,
		`select id from matches where game_id = ? and code = 'main'`, gameID).Scan(&matchID)
	if err == sql.ErrNoRows {
		res, insErr := db.ExecContext(ctx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, status, revision, state_json)
values(?, ?, ?, 'main', ?, 1, 0, ?, 0, '{}')`, festID, gameID, stageID, title, matchStatus)
		if insErr != nil {
			return 0, 0, insErr
		}
		matchID, insErr = res.LastInsertId()
		if insErr != nil {
			return 0, 0, insErr
		}
	} else if err != nil {
		return 0, 0, err
	}
	return stageID, matchID, nil
}

// rewriteGameStateRecords redirects every flat game's state_json row-op in the
// hot journal onto its match row (EK games keep theirs — the column remains
// their seed-import staging).
func rewriteGameStateRecords(ctx context.Context, db *sql.DB, matchOf map[int64]int64) error {
	type rec struct {
		id      int64
		payload string
	}
	records, err := store.CollectRows(ctx, db, `
select id, payload from journal
where iif(op in (1, 2, 3), json_extract(payload, '$.t'), null) = 'games'
  and iif(op in (1, 2, 3), json_extract(payload, '$.r.state_json'), null) is not null`,
		nil, func(rows *sql.Rows) (rec, error) {
			var r rec
			return r, rows.Scan(&r.id, &r.payload)
		})
	if err != nil {
		return err
	}
	for _, r := range records {
		var decoded struct {
			Table string         `json:"t"`
			Row   map[string]any `json:"r"`
		}
		if err := json.Unmarshal([]byte(r.payload), &decoded); err != nil {
			continue
		}
		gameID, ok := asInt(decoded.Row["id"])
		if !ok {
			continue
		}
		matchID, flat := matchOf[gameID]
		if !flat {
			continue
		}
		state, _ := decoded.Row["state_json"].(string)
		delete(decoded.Row, "state_json")
		var out []byte
		if state != "" {
			out, err = json.Marshal(map[string]any{
				"t": "matches",
				"r": map[string]any{"id": matchID, "state_json": state},
			})
		} else {
			out, err = json.Marshal(map[string]any{"t": decoded.Table, "r": decoded.Row})
		}
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `update journal set payload = ? where id = ?`, string(out), r.id); err != nil {
			return err
		}
	}
	return nil
}

// rewriteFlatCheckpoints folds each flat game's checkpoint StateJSON into an
// injected stage+match row, so restoring an old checkpoint recreates the
// unified shape instead of resurrecting games.state_json.
func rewriteFlatCheckpoints(ctx context.Context, db *sql.DB, stageOf, matchOf map[int64]int64) error {
	for gameID, matchID := range matchOf {
		type cpRow struct {
			seq  int64
			blob []byte
		}
		cpRows, err := store.CollectRows(ctx, db, `
select seq, state_blob from journal_checkpoint where game_id = ?`, []any{gameID},
			func(rows *sql.Rows) (cpRow, error) {
				var r cpRow
				return r, rows.Scan(&r.seq, &r.blob)
			})
		if err != nil {
			return err
		}
		for _, row := range cpRows {
			if err := rewriteOneFlatCheckpoint(ctx, db, gameID, matchID, stageOf[gameID], row.seq, row.blob); err != nil {
				return err
			}
		}
	}
	return nil
}

func rewriteOneFlatCheckpoint(ctx context.Context, db *sql.DB, gameID, matchID, stageID, seq int64, blob []byte) error {
	{
		cp, err := journal.DecodeGameCheckpoint(blob)
		if err != nil {
			return fmt.Errorf("checkpoint game %d: %w", gameID, err)
		}
		var festID int64
		if err := db.QueryRowContext(ctx, `select fest_id from games where id = ?`, gameID).Scan(&festID); err != nil {
			return err
		}
		var matchTitle, matchStatus string
		_ = db.QueryRowContext(ctx, `select title, status from matches where id = ?`, matchID).Scan(&matchTitle, &matchStatus)
		if len(cp.Tables["stages"]) == 0 {
			cp.Tables["stages"] = []map[string]any{{
				"id": stageID, "fest_id": festID, "game_id": gameID, "code": "main", "title": "",
				"stage_type": "matches", "kind": "matches", "position": int64(1), "status": "active", "config_json": "{}",
			}}
		}
		if len(cp.Tables["matches"]) == 0 {
			cp.Tables["matches"] = []map[string]any{{
				"id": matchID, "fest_id": festID, "game_id": gameID, "stage_id": stageID,
				"code": "main", "title": matchTitle, "position": int64(1), "participant_count": int64(0),
				"status": matchStatus, "revision": int64(0), "state_json": cp.StateJSON,
			}}
		} else {
			for _, row := range cp.Tables["matches"] {
				if id, ok := asInt(row["id"]); ok && id == matchID {
					row["state_json"] = cp.StateJSON
				}
			}
		}
		cp.StateJSON = ""
		encoded, err := journal.EncodeGameCheckpoint(cp)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx,
			`update journal_checkpoint set state_blob = ? where game_id = ? and seq = ?`, encoded, gameID, seq); err != nil {
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
where iif(j.op in (1, 2, 3), json_extract(j.payload, '$.t'), null) in ('themes', 'answers')
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
