package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"dope/dope/storage/journal"
)

// Cold-segment conversion: segments archive hot payload bytes verbatim, so the
// legacy row-deltas rewritten in the hot journal also sit inside every zstd
// segment — JSON-encoded when the archiver folded them, varint-encoded when the
// audit converter wrote them. Rewriting both keeps the whole log, hot and cold,
// on one schema dialect. Event records (op >= 64) pass through untouched.

// convertSegments rewrites legacy row-op records inside every cold segment:
// reseed_entries renames to stage_standings (team_id -> participant_id), and a
// flat game's games.state_json delta redirects onto its 'main' match row.
func convertSegments(ctx context.Context, db *sql.DB) error {
	if err := journal.CreateTables(db); err != nil {
		return err
	}
	matchOf, err := flatMatchIDs(ctx, db)
	if err != nil {
		return err
	}
	names, err := journal.LoadDict(ctx, db)
	if err != nil {
		return err
	}
	dict, err := journal.LoadWritableDict(db)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `select id, blob from journal_segment`)
	if err != nil {
		return err
	}
	type seg struct {
		id   int64
		blob []byte
	}
	var segs []seg
	for rows.Next() {
		var s seg
		if err := rows.Scan(&s.id, &s.blob); err != nil {
			rows.Close()
			return err
		}
		segs = append(segs, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range segs {
		raw, err := journal.Decompress(s.blob)
		if err != nil {
			return fmt.Errorf("segment %d: %w", s.id, err)
		}
		recs, err := journal.DecodeSegment(raw)
		if err != nil {
			return fmt.Errorf("segment %d: %w", s.id, err)
		}
		changed := false
		for i := range recs {
			if rewriteSegmentRecord(&recs[i], matchOf, names, dict) {
				changed = true
			}
		}
		if !changed {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := dict.PersistTx(tx); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `update journal_segment set blob = ? where id = ?`,
			journal.Compress(journal.EncodeSegment(recs)), s.id); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// flatMatchIDs maps every ЧГК-family game to its single 'main' match — the
// redirect target for its legacy games.state_json deltas.
func flatMatchIDs(ctx context.Context, db *sql.DB) (map[int64]int64, error) {
	rows, err := db.QueryContext(ctx, `
select g.id, m.id from games g
join matches m on m.game_id = g.id and m.code = 'main'
where g.game_type in ('od', 'ksi', 'si')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var gameID, matchID int64
		if err := rows.Scan(&gameID, &matchID); err != nil {
			return nil, err
		}
		out[gameID] = matchID
	}
	return out, rows.Err()
}

func rewriteSegmentRecord(rec *journal.Record, matchOf map[int64]int64, names map[uint64]string, dict *journal.Dict) bool {
	switch rec.Op {
	case journal.OpRowIns, journal.OpRowSet, journal.OpRowDel:
	default:
		return false
	}
	if len(rec.Args) > 0 && rec.Args[0] == '{' {
		return rewriteJSONArgs(rec, matchOf)
	}
	return rewriteVarintArgs(rec, matchOf, names, dict)
}

func rewriteJSONArgs(rec *journal.Record, matchOf map[int64]int64) bool {
	var decoded struct {
		Table string         `json:"t"`
		Row   map[string]any `json:"r"`
	}
	if err := json.Unmarshal(rec.Args, &decoded); err != nil {
		return false
	}
	switch decoded.Table {
	case "reseed_entries":
		if teamID, ok := decoded.Row["team_id"]; ok {
			decoded.Row["participant_id"] = teamID
			delete(decoded.Row, "team_id")
		}
		return marshalArgs(rec, "stage_standings", decoded.Row)
	case "games":
		state, isString := decoded.Row["state_json"].(string)
		if _, present := decoded.Row["state_json"]; !present {
			return false
		}
		gameID, ok := asInt(decoded.Row["id"])
		if !ok {
			return false
		}
		matchID, flat := matchOf[gameID]
		if !flat {
			return false
		}
		if isString && state != "" {
			return marshalArgs(rec, "matches", map[string]any{"id": matchID, "state_json": state})
		}
		delete(decoded.Row, "state_json")
		return marshalArgs(rec, "games", decoded.Row)
	}
	return false
}

func marshalArgs(rec *journal.Record, table string, row map[string]any) bool {
	out, err := json.Marshal(map[string]any{"t": table, "r": row})
	if err != nil {
		return false
	}
	rec.Args = out
	return true
}

func rewriteVarintArgs(rec *journal.Record, matchOf map[int64]int64, names map[uint64]string, dict *journal.Dict) bool {
	a, err := journal.DecodeRowArgs(rec.Args)
	if err != nil {
		return false
	}
	switch names[a.TableID] {
	case "reseed_entries":
		a.TableID = dict.Intern("stage_standings")
		for i, c := range a.Cols {
			if names[c.NameID] == "team_id" {
				a.Cols[i].NameID = dict.Intern("participant_id")
			}
		}
		rec.Args = journal.EncodeRowArgs(a)
		return true
	case "games":
		var gameID int64
		var state string
		stateAt := -1
		for i, c := range a.Cols {
			switch names[c.NameID] {
			case "id":
				gameID, _ = asInt(c.Val)
			case "state_json":
				stateAt = i
				switch v := c.Val.(type) {
				case string:
					state = v
				case []byte:
					state = string(v)
				}
			}
		}
		if stateAt < 0 {
			return false
		}
		matchID, flat := matchOf[gameID]
		if !flat {
			return false
		}
		if state != "" {
			a = journal.RowArgs{TableID: dict.Intern("matches"), Cols: []journal.ColVal{
				{NameID: dict.Intern("id"), Val: matchID},
				{NameID: dict.Intern("state_json"), Val: state},
			}}
		} else {
			a.Cols = append(a.Cols[:stateAt], a.Cols[stateAt+1:]...)
		}
		rec.Args = journal.EncodeRowArgs(a)
		return true
	}
	return false
}
