package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"dope/dope/domain/entrants"
	"dope/dope/domain/festops"
	"dope/dope/domain/flatgame"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	"dope/dope/domain/sites"
	"dope/dope/storage/store"
)

// A synch fest's import puts each team on the площадка the rating site has it
// play at, and each площадка numbers its own teams from 1. A Game on a
// площадка seats that площадка's teams alone under those numbers, a Game on
// none seats everyone under the fest's, and a team the site moves to another
// площадка moves between the Games with it and takes the next number there.
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
			names = append(names, fmt.Sprintf("%d %s", team.Number, team.Name))
		}
		slices.Sort(names)
		return names
	}
	minskGame, brestGame, everyone := create(siteOf(minsk)), create(siteOf(brest)), create(0)
	if got := seated(minskGame); !slices.Equal(got, []string{"1 Альфа", "2 Бета"}) {
		t.Fatalf("Минск seats %v", got)
	}
	if got := seated(brestGame); !slices.Equal(got, []string{"1 Гамма"}) {
		t.Fatalf("Брест seats %v", got)
	}
	if got := seated(everyone); !slices.Equal(got, []string{"1 Альфа", "2 Бета", "3 Гамма"}) {
		t.Fatalf("the Game on no площадка seats %v", got)
	}
	var participants []string
	for _, game := range []int64{minskGame, brestGame} {
		withTx(t, db, func(ctx context.Context, tx *sql.Tx) error { return flatgame.SettleTx(ctx, tx, festID, game) })
	}
	rows, err := db.Query(`
select distinct p.number || ' ' || p.name from game_participants gp join participants p on p.id = gp.participant_id
where gp.game_id in (?, ?) order by 1`, minskGame, brestGame)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		participants = append(participants, row)
	}
	if !slices.Equal(participants, []string{"1 Альфа", "2 Бета", "3 Гамма"}) {
		t.Fatalf("the площадки' Games seat the fest's Participants %v", participants)
	}

	load(brest)
	if got := seated(minskGame); !slices.Equal(got, []string{"2 Бета"}) {
		t.Fatalf("after the move Минск seats %v", got)
	}
	if got := seated(brestGame); !slices.Equal(got, []string{"1 Гамма", "2 Альфа"}) {
		t.Fatalf("after the move Брест seats %v", got)
	}

	if err := srv.PageServer().AutoFestNumbers(t.Context(), festID, siteOf(minsk)); err != nil {
		t.Fatal(err)
	}
	if got := seated(minskGame); !slices.Equal(got, []string{"1 Бета"}) {
		t.Fatalf("after numbering Минск anew it seats %v", got)
	}
	if got := seated(brestGame); !slices.Equal(got, []string{"1 Гамма", "2 Альфа"}) {
		t.Fatalf("numbering Минск anew changed Брест to %v", got)
	}
}
