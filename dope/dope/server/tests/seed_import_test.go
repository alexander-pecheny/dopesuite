package tests

import (
	"context"
	"database/sql"
	"dope/dope/domain/core"
	"dope/dope/domain/imports"
	"dope/dope/platform/realtime"
	"dope/dope/platform/util"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
	"dope/dope/web/hostpages"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSeedImportFromKSIResolvesGenericSeedsAndDeclines(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, ekGameID := createSeedImportFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	scope := dopeserver.FestScope{FestID: festID, GameID: ekGameID}

	view, _, _, err := imports.ImportSeedsFromKSI(srv.Eng(), t.Context(), scope)
	if err != nil {
		t.Fatalf("import seeds: %v", err)
	}
	if view.DrawSize != 4 || view.ActiveCount != 5 || len(view.Rows) != 5 {
		t.Fatalf("view = %#v, want draw size 4 / 5 active rows", view)
	}
	if got := seedImportRowNames(view); !sameStrings(got, []string{"B", "A", "C", "D", "E"}) {
		t.Fatalf("seed rows = %#v, want KSI ranking", got)
	}
	if !view.Rows[4].Waitlist || view.Rows[4].SeedNumber != 5 {
		t.Fatalf("row 5 = %#v, want waitlist seed 5", view.Rows[4])
	}
	if got := seedImportSlotNames(t, db, ekGameID); !sameStrings(got, []string{"B", "A", "C", "D"}) {
		t.Fatalf("slot names = %#v, want first 4 seeds", got)
	}
	if got := seedImportRegularThemeCount(t, db, ekGameID); got != 4*store.ThemeCount {
		t.Fatalf("regular themes = %d, want %d", got, 4*store.ThemeCount)
	}

	view, _, _, err = imports.SetSeedImportDeclined(srv.Eng(), t.Context(), scope, imports.SeedDeclineRequest{
		TeamID:   view.Rows[0].TeamID,
		Declined: true,
	})
	if err != nil {
		t.Fatalf("decline seed: %v", err)
	}
	if view.Rows[0].SeedNumber != 0 || !view.Rows[0].Declined {
		t.Fatalf("declined row = %#v, want no seed number and declined=true", view.Rows[0])
	}
	if got := seedImportSeedNumbers(view); !sameInts(got, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("seed numbers after decline = %#v", got)
	}
	if got := seedImportSlotNames(t, db, ekGameID); !sameStrings(got, []string{"A", "C", "D", "E"}) {
		t.Fatalf("slot names after decline = %#v, want waitlist team promoted", got)
	}
	if got := seedImportRegularThemeCount(t, db, ekGameID); got != 4*store.ThemeCount {
		t.Fatalf("regular themes after decline = %d, want %d", got, 4*store.ThemeCount)
	}
	if got := seedImportExtraThemeCount(t, db, ekGameID); got != 0 {
		t.Fatalf("extra themes after decline = %d, want 0", got)
	}
}

func TestSeedImportFromKSIPropagatesDeclines(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, ekGameID := createSeedImportFixture(t, db)
	// Mark team "B" (the KSI top seed) as refused-to-play. The fixture uses legacy
	// number-less participants, so the identity key is "s"+lowercased name.
	declineKSIParticipant(t, db, festID, "sb")

	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	scope := dopeserver.FestScope{FestID: festID, GameID: ekGameID}

	view, _, _, err := imports.ImportSeedsFromKSI(srv.Eng(), t.Context(), scope)
	if err != nil {
		t.Fatalf("import seeds: %v", err)
	}

	// B is still imported (visible on the EK page) but lands pre-declined: no seed.
	bRow, ok := seedImportRowByName(view, "B")
	if !ok || !bRow.Declined || bRow.SeedNumber != 0 {
		t.Fatalf("B row = %#v (found=%v), want declined with no seed number", bRow, ok)
	}
	// Seeding skips B and fills the bracket with the teams that actually played.
	if got := seedImportSlotNames(t, db, ekGameID); !sameStrings(got, []string{"A", "C", "D", "E"}) {
		t.Fatalf("slot names = %#v, want B excluded", got)
	}
}

func declineKSIParticipant(t *testing.T, db *sql.DB, festID int64, key string) {
	t.Helper()
	var gameID int64
	var raw string
	if err := db.QueryRowContext(context.Background(), `
select id, coalesce(state_json, '{}') from games where fest_id = ? and game_type = 'ksi'`, festID).Scan(&gameID, &raw); err != nil {
		t.Fatalf("load ksi game: %v", err)
	}
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		t.Fatalf("decode ksi state: %v", err)
	}
	obj["declined"] = json.RawMessage(`{"` + key + `":true}`)
	next, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("encode ksi state: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `update games set state_json = ? where id = ?`, string(next), gameID); err != nil {
		t.Fatalf("update ksi state: %v", err)
	}
}

func seedImportRowByName(view imports.SeedImportView, name string) (imports.SeedImportViewRow, bool) {
	for _, row := range view.Rows {
		if row.Name == name {
			return row, true
		}
	}
	return imports.SeedImportViewRow{}, false
}

func TestSeedLabelsShownBeforeImport(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, ekGameID := createSeedImportFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	scope, err := srv.VerifyMatchInScope(t.Context(), dopeserver.FestScope{FestID: festID, GameID: ekGameID}, "A")
	if err != nil {
		t.Fatalf("match scope: %v", err)
	}
	view, err := srv.LoadScopedMatchViewLocked(scope)
	if err != nil {
		t.Fatalf("load match: %v", err)
	}
	if got := matchTeamNames(view); !sameStrings(got, []string{"Посев-1", "Посев-2", "Посев-3", "Посев-4"}) {
		t.Fatalf("team names = %#v, want seed labels", got)
	}
}

func TestFinishAssignsPlaces(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, ekGameID := createSeedImportFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	scopeBase := dopeserver.FestScope{FestID: festID, GameID: ekGameID}
	if _, _, _, err := imports.ImportSeedsFromKSI(srv.Eng(), t.Context(), scopeBase); err != nil {
		t.Fatalf("import seeds: %v", err)
	}
	scope, err := srv.VerifyMatchInScope(t.Context(), scopeBase, "A")
	if err != nil {
		t.Fatalf("match scope: %v", err)
	}
	finished := true
	view, _, _, _, err := srv.ApplyScopedMatchUpdate(t.Context(), scope, []dopeserver.UpdateRequest{{Finished: &finished}})
	if err != nil {
		t.Fatalf("finish match: %v", err)
	}
	if got := matchTeamPlaces(view); !sameFloats(got, []float64{1, 2, 3, 4}) {
		t.Fatalf("places = %#v, want 1..4", got)
	}
}

func createSeedImportFixture(t *testing.T, db *sql.DB) (int64, int64) {
	t.Helper()
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	now := util.UtcNow()
	systemID, err := dopeserver.EnsureSystemUser(ctx, tx)
	if err != nil {
		t.Fatalf("system user: %v", err)
	}
	festID, err := store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(null, 'Seed fixture', '', null, ?, 1, ?, ?, 1)`, systemID, now, now)
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
		{"right", "", "", "", ""},
	}
	if _, err := insertJSONGameFixture(ctx, tx, festID, "ksi", "КСИ", "ksi", 1,
		map[string]any{
			"schemaVersion": 2,
			"title":         "КСИ",
			"gameType":      "ksi",
			"participants":  []string{"A", "B", "C", "D", "E"},
			"themes":        1,
		},
		map[string]any{
			"participants": []string{"A", "B", "C", "D", "E"},
			"themes":       []map[string]any{{"answers": answers}},
			"finished":     true,
		}); err != nil {
		t.Fatalf("insert ksi: %v", err)
	}

	var scheme store.FestScheme
	rawScheme := `{
	  "schemaVersion": 2,
	  "slug": "seed-ek",
	  "title": "Seed EK",
	  "gameType": "ek",
	  "venues": [{"number": 1, "title": "Зал"}],
	  "stages": [{
	    "code": "r1",
	    "title": "Раунд",
	    "stage_type": "matches",
	    "matches": [{
	      "code": "A",
	      "title": "Бой A",
	      "venue": 1,
	      "participantCount": 4,
	      "slots": ["seed-1", "seed-2", "seed-3", "seed-4"]
	    }]
	  }]
	}`
	if err := json.Unmarshal([]byte(rawScheme), &scheme); err != nil {
		t.Fatalf("decode ek scheme: %v", err)
	}
	ekGameID, err := hostpages.CreateEKGameTx(ctx, tx, festID, scheme)
	if err != nil {
		t.Fatalf("create ek: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return festID, ekGameID
}

func seedImportSlotNames(t *testing.T, db *sql.DB, gameID int64) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
select coalesce(t.name, '')
from match_slots ms
join matches m on m.id = ms.match_id
left join teams t on t.id = ms.team_id
where m.game_id = ? and m.code = 'A'
order by ms.slot_index`, gameID)
	if err != nil {
		t.Fatalf("query slots: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan slot: %v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("slot rows: %v", err)
	}
	return out
}

func seedImportRegularThemeCount(t *testing.T, db *sql.DB, gameID int64) int {
	t.Helper()
	match, err := store.LoadDBMatchStateWhere(context.Background(), db, `m.game_id = ? and m.code = 'A'`, gameID)
	if err != nil {
		t.Fatalf("load match state: %v", err)
	}
	count := 0
	for i, id := range match.TeamIDs {
		if id != 0 {
			count += len(match.State.Teams[i].Themes)
		}
	}
	return count
}

// seedImportExtraThemeCount counts state-blob team sections that no longer
// have a seat in the match — the pruning invariant after a decline reshuffle.
func seedImportExtraThemeCount(t *testing.T, db *sql.DB, gameID int64) int {
	t.Helper()
	match, err := store.LoadDBMatchStateWhere(context.Background(), db, `m.game_id = ? and m.code = 'A'`, gameID)
	if err != nil {
		t.Fatalf("load match state: %v", err)
	}
	seated := map[string]bool{}
	for _, id := range match.TeamIDs {
		if id != 0 {
			seated[strconv.FormatInt(id, 10)] = true
		}
	}
	count := 0
	for key := range match.Blob.Teams {
		if !seated[key] {
			count++
		}
	}
	return count
}

func seedImportRowNames(view imports.SeedImportView) []string {
	out := make([]string, len(view.Rows))
	for i, row := range view.Rows {
		out[i] = row.Name
	}
	return out
}

func matchTeamNames(view store.MatchView) []string {
	out := make([]string, len(view.Teams))
	for i, team := range view.Teams {
		out[i] = team.Name
	}
	return out
}

func matchTeamPlaces(view store.MatchView) []float64 {
	out := make([]float64, len(view.Teams))
	for i, team := range view.Teams {
		out[i] = team.Place
	}
	return out
}

func seedImportSeedNumbers(view imports.SeedImportView) []int {
	out := make([]int, len(view.Rows))
	for i, row := range view.Rows {
		out[i] = row.SeedNumber
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
