package migrate

import (
	"database/sql"
	"dope/dope/storage/journal"
	"dope/dope/storage/store"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
)

// RunConvertHistory rebuilds the full per-game edit history from a legacy
// audit_log into the new journal, so a migrated database keeps its whole
// history VISIBLE in the per-game history UI (not just forward edits). Unlike
// convert-audit (which produces opaque cold segments), this emits the same
// hot-journal entries the live triggers would have:
//   - EK games: each answers/match_results/… delta → a row-op (op + {t,r}),
//     attributed to its game (resolved via the DB).
//   - OD/KSI games: each games.state_json change → a synthetic game-state patch
//     (the before/after states are diffed into set-ops), so it renders per-cell.
//
// Run on a COPY that still has audit_log, BEFORE the normal migration drops it:
//
//	dope-server convert-history --db /tmp/fest-copy.db
//
// Then boot the server on that copy so the migration installs triggers and
// genesis checkpoints (keyed above the history so revert won't reach into it).
func RunConvertHistory(args []string) {
	fs := flag.NewFlagSet("convert-history", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to a COPY of the legacy database (must still have audit_log)")
	_ = fs.Parse(args)
	if *dbPath == "" {
		log.Fatal("convert-history: --db is required")
	}
	db, err := sql.Open("sqlite", store.BuildDSN(*dbPath))
	if err != nil {
		log.Fatalf("convert-history: open: %v", err)
	}
	defer db.Close()
	// Allow >1 connection: the insert transaction runs while the audit_log read
	// cursor is still open (WAL handles concurrent reader + writer).
	db.SetMaxOpenConns(4)

	rep, err := convertHistory(db)
	if err != nil {
		log.Fatalf("convert-history: %v", err)
	}
	log.Printf("convert-history: wrote %d journal entries (%d EK row-ops, %d OD/KSI patches), skipped %d",
		rep.written, rep.rowOps, rep.patches, rep.skipped)
}

type historyReport struct {
	written, rowOps, patches, skipped int64
}

var ekDetailTables = map[string]bool{
	"answers": true, "match_results": true, "matches": true, "themes": true,
	"match_slots": true, "reseed_entries": true, "game_assignments": true,
	"game_teams": true, "game_players": true, "game_team_players": true, "stages": true,
}

func convertHistory(db *sql.DB) (historyReport, error) {
	var rep historyReport

	if err := ensureJournalSchema(db); err != nil {
		return rep, err
	}

	gameType, err := loadIntStrMap(db, `select id, game_type from games`)
	if err != nil {
		return rep, fmt.Errorf("game types: %w", err)
	}
	matchGame, err := loadIntIntMap(db, `select id, game_id from matches`)
	if err != nil {
		return rep, err
	}
	stageGame, err := loadIntIntMap(db, `select id, game_id from stages`)
	if err != nil {
		return rep, err
	}
	themeMatch, err := loadIntIntMap(db, `select id, match_id from themes`)
	if err != nil {
		return rep, err
	}
	answerTheme, err := loadIntIntMap(db, `select id, theme_id from answers`)
	if err != nil {
		return rep, err
	}
	slotMatch, err := loadIntIntMap(db, `select id, match_id from match_slots`)
	if err != nil {
		return rep, err
	}

	resolveGame := func(table string, row map[string]any) int64 {
		switch table {
		case "games":
			return rowInt64(row, "id")
		case "stages":
			if g := rowInt64(row, "game_id"); g != 0 {
				return g
			}
			return stageGame[rowInt64(row, "id")]
		case "matches":
			if g := rowInt64(row, "game_id"); g != 0 {
				return g
			}
			return matchGame[rowInt64(row, "id")]
		case "match_results":
			return matchGame[rowInt64(row, "match_id")]
		case "reseed_entries":
			return stageGame[rowInt64(row, "stage_id")]
		case "match_slots":
			if m := rowInt64(row, "match_id"); m != 0 {
				return matchGame[m]
			}
			return matchGame[slotMatch[rowInt64(row, "id")]]
		case "themes":
			if m := rowInt64(row, "match_id"); m != 0 {
				return matchGame[m]
			}
			return matchGame[themeMatch[rowInt64(row, "id")]]
		case "answers":
			if t := rowInt64(row, "theme_id"); t != 0 {
				return matchGame[themeMatch[t]]
			}
			return matchGame[themeMatch[answerTheme[rowInt64(row, "id")]]]
		default:
			if rowInt64(row, "game_id") != 0 {
				return rowInt64(row, "game_id")
			}
			return 0
		}
	}

	rows, err := db.Query(`
select id, ts, table_name, op, dope_unz(before_json), dope_unz(after_json),
       coalesce(actor_user_id, 0), coalesce(request_id, ''), coalesce(fest_id, 0)
from audit_log order by id`)
	if err != nil {
		return rep, err
	}
	defer rows.Close()

	tx, err := db.Begin()
	if err != nil {
		return rep, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
insert into journal(id, fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return rep, err
	}
	defer stmt.Close()

	for rows.Next() {
		var (
			id        int64
			ts, table string
			op        string
			bj, aj    sql.NullString
			actor     int64
			reqID     string
			festID    int64
		)
		if err := rows.Scan(&id, &ts, &table, &op, &bj, &aj, &actor, &reqID, &festID); err != nil {
			return rep, err
		}
		fwd := pickRowJSON(op, bj, aj)
		row, _ := decodeRowJSONLoose(fwd)
		gid := resolveGame(table, row)
		if gid == 0 {
			// fall back to the *other* side for linking ids on UPDATEs
			if other, _ := decodeRowJSONLoose(otherRowJSON(op, bj, aj)); other != nil {
				gid = resolveGame(table, other)
			}
		}
		if gid == 0 {
			rep.skipped++
			continue
		}
		gt := gameType[gid]

		var (
			opcode  journal.Op
			payload []byte
		)
		switch {
		case table == "games" && (gt == "ksi" || gt == "od"):
			if op == "UPDATE" {
				ops := diffStateLeaves(jsonField(bj, "state_json"), jsonField(aj, "state_json"))
				if len(ops) == 0 {
					rep.skipped++
					continue
				}
				payload, _ = json.Marshal(map[string]any{"ops": ops})
				opcode = journal.OpEvGameStatePatch
			} else if op == "INSERT" {
				opcode = journal.OpEvGameState
				payload = []byte(jsonField(aj, "state_json"))
			} else {
				rep.skipped++
				continue
			}
			rep.patches++
		case ekDetailTables[table]:
			switch op {
			case "INSERT":
				opcode = journal.OpRowIns
			case "UPDATE":
				opcode = journal.OpRowSet
			case "DELETE":
				opcode = journal.OpRowDel
			}
			payload, _ = json.Marshal(map[string]any{"t": table, "r": row})
			rep.rowOps++
		default:
			rep.skipped++
			continue
		}

		var actorArg any
		if actor != 0 {
			actorArg = actor
		}
		var reqArg any
		if reqID != "" {
			reqArg = reqID
		}
		var festArg any
		if festID != 0 {
			festArg = festID
		}
		if _, err := stmt.Exec(id, festArg, gid, id, ts, actorArg, reqArg, int(opcode), payload, ts); err != nil {
			return rep, fmt.Errorf("insert journal for audit %d: %w", id, err)
		}
		rep.written++
	}
	if err := rows.Err(); err != nil {
		return rep, err
	}
	return rep, tx.Commit()
}

// ensureJournalSchema creates the journal tables on a legacy DB that predates
// them (so convert-history can populate them before the normal migration runs).
func ensureJournalSchema(db *sql.DB) error {
	_, err := db.Exec(`
create table if not exists journal(
  id integer primary key, fest_id integer, game_id integer, seq integer not null,
  ts text not null, actor_user_id integer, request_id text, op integer not null,
  payload blob not null default x'', created_at text not null);
create index if not exists journal_fest_seq on journal(fest_id, seq);
create index if not exists journal_game_seq on journal(game_id, seq);
create table if not exists journal_dict(id integer primary key, str text not null unique);`)
	return err
}

// --- helpers ----------------------------------------------------------------

func pickRowJSON(op string, bj, aj sql.NullString) string {
	if op == "DELETE" {
		if bj.Valid {
			return bj.String
		}
		return ""
	}
	if aj.Valid {
		return aj.String
	}
	return ""
}

func otherRowJSON(op string, bj, aj sql.NullString) string {
	if op == "DELETE" {
		if aj.Valid {
			return aj.String
		}
		return ""
	}
	if bj.Valid {
		return bj.String
	}
	return ""
}

func decodeRowJSONLoose(s string) (map[string]any, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	return DecodeRowJSON(s)
}

func jsonField(s sql.NullString, field string) string {
	if !s.Valid {
		return ""
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(s.String), &m) != nil {
		return ""
	}
	raw, ok := m[field]
	if !ok {
		return ""
	}
	// state_json is stored as a JSON string inside the row object → unquote.
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	return string(raw)
}

func loadIntStrMap(db *sql.DB, query string) (map[int64]string, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var k int64
		var v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func loadIntIntMap(db *sql.DB, query string) (map[int64]int64, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var k, v int64
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// diffStateLeaves diffs two game state_json documents into game-state set/remove
// patch ops on the changed leaves — the same shape the live PATCH path records,
// so the history UI renders OD/KSI edits per-cell.
func diffStateLeaves(beforeJSON, afterJSON string) []map[string]any {
	var before, after any
	dec := json.NewDecoder(strings.NewReader(emptyObjIfBlank(beforeJSON)))
	dec.UseNumber()
	_ = dec.Decode(&before)
	dec2 := json.NewDecoder(strings.NewReader(emptyObjIfBlank(afterJSON)))
	dec2.UseNumber()
	_ = dec2.Decode(&after)

	var ops []map[string]any
	var walk func(b, a any, path []any)
	walk = func(b, a any, path []any) {
		if len(ops) >= 200 { // guard against pathological diffs
			return
		}
		am, aIsMap := a.(map[string]any)
		bm, bIsMap := b.(map[string]any)
		if aIsMap {
			for k, av := range am {
				var bv any
				if bIsMap {
					bv = bm[k]
				}
				walk(bv, av, append(path, k))
			}
			return
		}
		as, aIsArr := a.([]any)
		bs, bIsArr := b.([]any)
		if aIsArr {
			for i, av := range as {
				var bv any
				if bIsArr && i < len(bs) {
					bv = bs[i]
				}
				walk(bv, av, append(path, i))
			}
			return
		}
		if !jsonEqual(b, a) {
			cp := make([]any, len(path))
			copy(cp, path)
			ops = append(ops, map[string]any{"op": "set", "path": cp, "value": a})
		}
	}
	walk(before, after, nil)
	return ops
}

func emptyObjIfBlank(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
