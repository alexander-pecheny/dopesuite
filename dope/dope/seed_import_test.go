package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSeedImportFromKSIResolvesGenericSeedsAndDeclines(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, ekGameID := createSeedImportFixture(t, db)
	srv := &server{
		db:              db,
		subscribers:     make(map[chan event]struct{}),
		hostSubscribers: make(map[chan hostPresenceEvent]struct{}),
	}
	scope := festScope{FestID: festID, GameID: ekGameID}

	view, _, _, err := srv.importSeedsFromKSI(t.Context(), scope)
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

	view, _, _, err = srv.setSeedImportDeclined(t.Context(), scope, seedDeclineRequest{
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
}

func createSeedImportFixture(t *testing.T, db *sql.DB) (int64, int64) {
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

	var scheme festScheme
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
	ekGameID, err := createEKGameTx(ctx, tx, festID, scheme)
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

func seedImportRowNames(view seedImportView) []string {
	out := make([]string, len(view.Rows))
	for i, row := range view.Rows {
		out[i] = row.Name
	}
	return out
}

func seedImportSeedNumbers(view seedImportView) []int {
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
