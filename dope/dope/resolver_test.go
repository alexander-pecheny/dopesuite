package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestResolverPropagatesBracket exercises the full forward chain: finishing the
// 1/16 and 1/8 bouts must compute a reseed that sums both games and resolve the
// downstream reseed slots; un-finishing an upstream bout must roll it all back.
func TestResolverPropagatesBracket(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, gameID := createBracketFixture(t, db)
	srv := &server{
		db:              db,
		subscribers:     make(map[int64]map[chan event]struct{}),
		hostSubscribers: make(map[int64]map[chan hostPresenceEvent]struct{}),
	}
	scopeBase := festScope{FestID: festID, GameID: gameID}
	if _, _, _, err := srv.importSeedsFromKSI(t.Context(), scopeBase); err != nil {
		t.Fatalf("import seeds: %v", err)
	}

	finish := func(code string, finished bool) {
		t.Helper()
		scope, err := srv.verifyMatchInScope(t.Context(), scopeBase, code)
		if err != nil {
			t.Fatalf("scope %s: %v", code, err)
		}
		if _, _, err := srv.applyScopedMatchUpdate(scope, updateRequest{Finished: &finished}); err != nil {
			t.Fatalf("finish %s=%v: %v", code, finished, err)
		}
	}

	// Only the 1/16 bout is done — reseed cannot be computed and the 1/4 slot
	// stays unresolved.
	finish("A", true)
	if n := reseedEntryCount(t, db, gameID, "rs"); n != 0 {
		t.Fatalf("reseed entries before 1/8 finished = %d, want 0", n)
	}
	if teams := slotTeams(t, db, gameID, "C"); !allZero(teams) {
		t.Fatalf("1/4 slots resolved too early: %v", teams)
	}

	// Both games done — reseed sums place across A and B (1+1, 2+2, ...) and the
	// 1/4 bout's reseed slots resolve to the ranked teams.
	finish("B", true)
	entries := reseedEntries(t, db, gameID, "rs")
	if len(entries) != 4 {
		t.Fatalf("reseed entries = %d, want 4", len(entries))
	}
	if got := entries[0].num("place_sum"); got != 2 {
		t.Fatalf("rank 1 place_sum = %v, want 2 (place 1 in each of two games)", got)
	}
	cTeams := slotTeams(t, db, gameID, "C")
	if allZero(cTeams) {
		t.Fatalf("1/4 slots not resolved after both games finished: %v", cTeams)
	}
	if cTeams[0] != entries[0].teamID {
		t.Fatalf("1/4 first slot = team %d, want reseed rank 1 team %d", cTeams[0], entries[0].teamID)
	}
	if got := regularThemeCount(t, db, gameID, "C", cTeams[0]); got != themeCount {
		t.Fatalf("resolved 1/4 team has %d regular themes, want %d", got, themeCount)
	}

	// Reopening the 1/16 invalidates everything downstream again.
	finish("A", false)
	if n := reseedEntryCount(t, db, gameID, "rs"); n != 0 {
		t.Fatalf("reseed entries after reopening 1/16 = %d, want 0", n)
	}
	if teams := slotTeams(t, db, gameID, "C"); !allZero(teams) {
		t.Fatalf("1/4 slots still resolved after reopening 1/16: %v", teams)
	}
}

func allZero(ids []int64) bool {
	for _, id := range ids {
		if id != 0 {
			return false
		}
	}
	return true
}

type reseedRow struct {
	teamID  int64
	metrics map[string]any
}

func (r reseedRow) num(key string) float64 {
	value, _ := r.metrics[key].(float64)
	return value
}

func reseedEntries(t *testing.T, db *sql.DB, gameID int64, stageCode string) []reseedRow {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
select re.team_id, re.metrics_json
from reseed_entries re
join stages s on s.id = re.stage_id
where s.game_id = ? and s.code = ?
order by re.rank`, gameID, stageCode)
	if err != nil {
		t.Fatalf("query reseed: %v", err)
	}
	defer rows.Close()
	var out []reseedRow
	for rows.Next() {
		var r reseedRow
		var raw string
		if err := rows.Scan(&r.teamID, &raw); err != nil {
			t.Fatalf("scan reseed: %v", err)
		}
		if err := json.Unmarshal([]byte(raw), &r.metrics); err != nil {
			t.Fatalf("decode metrics: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func reseedEntryCount(t *testing.T, db *sql.DB, gameID int64, stageCode string) int {
	t.Helper()
	return len(reseedEntries(t, db, gameID, stageCode))
}

func slotTeams(t *testing.T, db *sql.DB, gameID int64, matchCode string) []int64 {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
select coalesce(ms.team_id, 0)
from match_slots ms
join matches m on m.id = ms.match_id
where m.game_id = ? and m.code = ?
order by ms.slot_index`, gameID, matchCode)
	if err != nil {
		t.Fatalf("query slots: %v", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan slot: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func regularThemeCount(t *testing.T, db *sql.DB, gameID int64, matchCode string, teamID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), `
select count(*)
from themes th
join matches m on m.id = th.match_id
where m.game_id = ? and m.code = ? and th.team_id = ? and th.kind = 'regular'`,
		gameID, matchCode, teamID).Scan(&count); err != nil {
		t.Fatalf("count themes: %v", err)
	}
	return count
}

// createBracketFixture builds a fest with a KSI game (for seeding) and an EK
// game whose bracket is 1/16 (A) -> 1/8 (B) -> reseed (rs) -> 1/4 (C).
func createBracketFixture(t *testing.T, db *sql.DB) (int64, int64) {
	t.Helper()
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	now := utcNow()
	systemID, err := ensureSystemUser(ctx, tx)
	if err != nil {
		t.Fatalf("system user: %v", err)
	}
	festID, err := insertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(null, 'Bracket fixture', '', null, ?, 1, ?, ?, 1)`, systemID, now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'creator', ?)`, festID, systemID, now); err != nil {
		t.Fatalf("insert organizer: %v", err)
	}

	answers := [][]string{
		{"", "", "", "right", ""},
		{"", "", "", "", "right"},
		{"", "", "right", "", ""},
		{"", "right", "", "", ""},
	}
	if _, err := insertJSONGameFixture(ctx, tx, festID, "ksi", "КСИ", "ksi", 1,
		map[string]any{
			"schemaVersion": 2, "title": "КСИ", "gameType": "ksi",
			"participants": []string{"A", "B", "C", "D"}, "themes": 1,
		},
		map[string]any{
			"participants": []string{"A", "B", "C", "D"},
			"themes":       []map[string]any{{"answers": answers}},
			"finished":     true,
		}); err != nil {
		t.Fatalf("insert ksi: %v", err)
	}

	rawScheme := `{
	  "schemaVersion": 2,
	  "slug": "bracket-ek",
	  "title": "Bracket EK",
	  "gameType": "ek",
	  "venues": [{"number": 1, "title": "Зал"}],
	  "stages": [
	    {"code": "r16", "title": "1/16", "stage_type": "matches", "position": 1,
	     "matches": [{"code": "A", "title": "Бой A", "venue": 1, "participantCount": 4,
	       "slots": ["seed-1", "seed-2", "seed-3", "seed-4"]}]},
	    {"code": "r8", "title": "1/8", "stage_type": "matches", "position": 2,
	     "matches": [{"code": "B", "title": "Бой B", "venue": 1, "participantCount": 4,
	       "slots": [
	         {"fromMatch": {"match": "A", "place": 1}},
	         {"fromMatch": {"match": "A", "place": 2}},
	         {"fromMatch": {"match": "A", "place": 3}},
	         {"fromMatch": {"match": "A", "place": 4}}]}]},
	    {"code": "rs", "title": "Пересев", "stage_type": "reseed", "position": 3,
	     "teams": [
	       {"fromMatch": {"match": "B", "place": 1}},
	       {"fromMatch": {"match": "B", "place": 2}},
	       {"fromMatch": {"match": "B", "place": 3}},
	       {"fromMatch": {"match": "B", "place": 4}}],
	     "sources": ["r16", "r8"],
	     "sort": [{"metric": "place_sum", "dir": "asc"}, {"metric": "total", "dir": "desc"}]},
	    {"code": "r4", "title": "1/4", "stage_type": "matches", "position": 4,
	     "matches": [{"code": "C", "title": "Бой C", "venue": 1, "participantCount": 4,
	       "slots": [
	         {"reseed": {"stage": "rs", "rank": 1}},
	         {"reseed": {"stage": "rs", "rank": 2}},
	         {"reseed": {"stage": "rs", "rank": 3}},
	         {"reseed": {"stage": "rs", "rank": 4}}]}]}
	  ]
	}`
	var scheme festScheme
	if err := json.Unmarshal([]byte(rawScheme), &scheme); err != nil {
		t.Fatalf("decode ek scheme: %v", err)
	}
	gameID, err := createEKGameTx(ctx, tx, festID, scheme)
	if err != nil {
		t.Fatalf("create ek: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return festID, gameID
}
