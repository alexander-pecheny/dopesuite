package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"testing"

	"dope/dope/domain/entrants"
	"dope/dope/domain/festops"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	"dope/dope/domain/sites"
	"dope/dope/storage/store"
)

// A synch fest's import puts each team on the площадка the rating site has it
// play at. A Game on a площадка seats that площадка's teams alone, a Game on
// none seats everyone, and a team the site moves to another площадка moves
// between the Games with it.
func TestASynchFestSeatsEachSiteApart(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	res, err := db.Exec(`insert into fests(title, revision, created_at, updated_at, kind) values('Синхрон', 1, '2026-10-16', '2026-10-16', ?)`, sites.Synch)
	if err != nil {
		t.Fatal(err)
	}
	festID, _ := res.LastInsertId()
	minsk, brest := sites.Venue{ID: 10, Title: "Минск"}, sites.Venue{ID: 20, Title: "Брест"}
	team := func(id int64, name string, venue sites.Venue) roster.FestRosterImportTeam {
		return roster.FestRosterImportTeam{RatingID: id, Name: name, Venue: venue,
			Players: []roster.FestRosterImportPlayer{{RatingID: id * 10, FirstName: name, LastName: "Капитан"}}}
	}
	load := func(alfa sites.Venue) {
		t.Helper()
		teams := []roster.FestRosterImportTeam{team(1, "Альфа", alfa), team(2, "Бета", minsk), team(3, "Гамма", brest)}
		if _, err := entrants.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, teams, imports.RosterChoice{}); err != nil {
			t.Fatalf("import: %v", err)
		}
	}
	load(minsk)

	siteOf := func(venue sites.Venue) int64 {
		var id int64
		if err := db.QueryRow(`select id from fest_sites where fest_id = ? and rating_venue_id = ?`, festID, venue.ID).Scan(&id); err != nil {
			t.Fatalf("площадка %s: %v", venue.Title, err)
		}
		return id
	}
	create := func(siteID int64) int64 {
		var gameID int64
		withTx(t, db, func(ctx context.Context, tx *sql.Tx) (err error) {
			gameID, _, err = festops.CreateGameTx(ctx, tx, gamebuild.Spec{FestID: festID, Type: games.OD, Label: "ОД", TourComp: []int{3}, SiteID: siteID})
			return err
		})
		return gameID
	}
	seated := func(gameID int64) []string {
		t.Helper()
		doc, err := store.LoadGameDoc(t.Context(), db, festID, gameID)
		if err != nil {
			t.Fatal(err)
		}
		var state games.ODState
		if err := json.Unmarshal([]byte(doc.State), &state); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, team := range state.Teams {
			names = append(names, team.Name)
		}
		slices.Sort(names)
		return names
	}
	minskGame, brestGame, everyone := create(siteOf(minsk)), create(siteOf(brest)), create(0)
	if got := seated(minskGame); !slices.Equal(got, []string{"Альфа", "Бета"}) {
		t.Fatalf("Минск seats %v", got)
	}
	if got := seated(brestGame); !slices.Equal(got, []string{"Гамма"}) {
		t.Fatalf("Брест seats %v", got)
	}
	if got := seated(everyone); len(got) != 3 {
		t.Fatalf("the Game on no площадка seats %v", got)
	}

	load(brest)
	if got := seated(minskGame); !slices.Equal(got, []string{"Бета"}) {
		t.Fatalf("after the move Минск seats %v", got)
	}
	if got := seated(brestGame); !slices.Equal(got, []string{"Альфа", "Гамма"}) {
		t.Fatalf("after the move Брест seats %v", got)
	}
}
