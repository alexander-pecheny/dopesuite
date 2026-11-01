package tests

import (
	"database/sql"
	"path/filepath"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	"dope/dope/platform/realtime"
	dopeserver "dope/dope/server"
)

// An import sets the base to each team's roster and зачёты as the site gave
// them, and an import that changes nothing in the fest still fills a base
// that lacks them, as on a fest imported before the base existed.
func TestAnImportSetsTheBase(t *testing.T) {
	t.Parallel()
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	festID, _, _ := createRosterPropagationFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	teams := []roster.FestRosterImportTeam{
		{RatingID: 2, Name: "Бета", Players: []roster.FestRosterImportPlayer{
			{RatingID: 30, FirstName: "Вера", LastName: "Б"}, {RatingID: 20, FirstName: "Анна", LastName: "А"}, {FirstName: "Без", LastName: "Ид"}}},
		{RatingID: 3, Name: "Гамма", Flags: []roster.FestRosterFlag{{RatingID: 59, Short: "ЧР", Full: "Выводящий зачёт ЧР"}, {Short: "Ш", Full: "Ш"}}},
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, teams, imports.RosterChoice{}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"2 roster": "[20,30]", "2 flags": "[]",
		"3 roster": "[]", "3 flags": `["ЧР","Ш"]`,
	}
	assertBase(t, db, festID, want)

	if _, err := db.Exec(`delete from rating_sync_base where fest_id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	again, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, teams, imports.RosterChoice{})
	if err != nil || !again.Unchanged {
		t.Fatalf("the same import again: %+v, %v", again, err)
	}
	assertBase(t, db, festID, want)
}

func assertBase(t *testing.T, db *sql.DB, festID int64, want map[string]string) {
	t.Helper()
	rows, err := db.Query(`select rating_team || ' ' || fact, value from rating_sync_base where fest_id = ?`, festID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatal(err)
		}
		got[key] = value
	}
	if len(got) != len(want) {
		t.Fatalf("base %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("base %v, want %v", got, want)
		}
	}
}
