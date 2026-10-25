package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"dope/dope/domain/entrants"
	"dope/dope/domain/festops"
	"dope/dope/domain/flatgame"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/multifest"
	"dope/dope/domain/roster"
	"dope/dope/platform/roles"
	"dope/dope/storage/store"
)

// A synch fest's import puts each team on the площадка the rating site has it
// play at, and each площадка numbers its own teams from 1. A Game on a
// площадка seats that площадка's teams alone under those numbers, a Game on
// none seats everyone under the fest's, and a team the site moves to another
// площадка moves between the Games with it and takes the next number there.
func TestASynchFestSeatsEachVenueApart(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	res, err := db.Exec(`insert into fests(title, revision, created_at, updated_at, kind) values('Синхрон', 1, '2026-10-16', '2026-10-16', ?)`, multifest.Synch)
	if err != nil {
		t.Fatal(err)
	}
	festID, _ := res.LastInsertId()
	minsk, brest := multifest.RatingVenue{ID: 10, Title: "Минск"}, multifest.RatingVenue{ID: 20, Title: "Брест"}
	team := func(id int64, name string, venue multifest.RatingVenue) roster.FestRosterImportTeam {
		return roster.FestRosterImportTeam{RatingID: id, Name: name, Venue: venue,
			Players: []roster.FestRosterImportPlayer{{RatingID: id * 10, FirstName: name, LastName: "Капитан"}}}
	}
	load := func(alfa multifest.RatingVenue) {
		t.Helper()
		teams := []roster.FestRosterImportTeam{team(1, "Альфа", alfa), team(2, "Бета", minsk), team(3, "Гамма", brest)}
		if _, err := entrants.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, teams, imports.RosterChoice{}); err != nil {
			t.Fatalf("import: %v", err)
		}
	}
	load(minsk)

	venueOf := func(venue multifest.RatingVenue) int64 {
		var id int64
		if err := db.QueryRow(`select id from multifest_venues where fest_id = ? and rating_venue_id = ?`, festID, venue.ID).Scan(&id); err != nil {
			t.Fatalf("площадка %s: %v", venue.Title, err)
		}
		return id
	}
	create := func(multiFestVenueID int64) int64 {
		var gameID int64
		withTx(t, db, func(ctx context.Context, tx *sql.Tx) (err error) {
			gameID, _, err = festops.CreateGameTx(ctx, tx, gamebuild.Spec{FestID: festID, Type: games.OD, Label: "ОД", TourComp: []int{3}, MultiFestVenueID: multiFestVenueID})
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
	minskGame, brestGame, everyone := create(venueOf(minsk)), create(venueOf(brest)), create(0)
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

	if err := srv.PageServer().AutoFestNumbers(t.Context(), festID, venueOf(minsk)); err != nil {
		t.Fatal(err)
	}
	if got := seated(minskGame); !slices.Equal(got, []string{"1 Бета"}) {
		t.Fatalf("after numbering Минск anew it seats %v", got)
	}
	if got := seated(brestGame); !slices.Equal(got, []string{"1 Гамма", "2 Альфа"}) {
		t.Fatalf("numbering Минск anew changed Брест to %v", got)
	}
}

// A host who follows a link to a venue runs that venue's Game alone, and edits
// the teams and numbers of that venue alone: a team they add plays there, and
// a team or a player of another venue is not theirs to touch.
func TestAVenueHostRunsAndEditsTheirVenueAlone(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	res, err := db.Exec(`insert into fests(title, revision, created_at, updated_at, kind) values('Синхрон', 1, '2026-10-19', '2026-10-19', ?)`, multifest.Synch)
	if err != nil {
		t.Fatal(err)
	}
	festID, _ := res.LastInsertId()
	minsk, brest := multifest.RatingVenue{ID: 10, Title: "Минск"}, multifest.RatingVenue{ID: 20, Title: "Брест"}
	team := func(id int64, name string, venue multifest.RatingVenue) roster.FestRosterImportTeam {
		return roster.FestRosterImportTeam{RatingID: id, Name: name, Venue: venue,
			Players: []roster.FestRosterImportPlayer{{RatingID: id * 10, FirstName: name, LastName: "Капитан"}}}
	}
	teams := []roster.FestRosterImportTeam{team(1, "Альфа", minsk), team(2, "Бета", minsk), team(3, "Гамма", brest)}
	if _, err := entrants.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, teams, imports.RosterChoice{}); err != nil {
		t.Fatalf("import: %v", err)
	}
	venueOf := func(venue multifest.RatingVenue) int64 {
		var id int64
		db.QueryRow(`select id from multifest_venues where fest_id = ? and rating_venue_id = ?`, festID, venue.ID).Scan(&id)
		return id
	}
	minskID, brestID := venueOf(minsk), venueOf(brest)
	create := func(venueID int64) int64 {
		var gameID int64
		withTx(t, db, func(ctx context.Context, tx *sql.Tx) (err error) {
			gameID, _, err = festops.CreateGameTx(ctx, tx, gamebuild.Spec{FestID: festID, Type: games.OD, Label: "ОД", TourComp: []int{3}, MultiFestVenueID: venueID})
			return err
		})
		return gameID
	}
	minskGame, brestGame := create(minskID), create(brestID)
	adminID, adminToken := createAPITestSession(t, srv, "boss")
	addAPITestRole(t, srv, festID, adminID, roles.Admin)
	_, hostToken := createAPITestSession(t, srv, "minsk")
	api := fmt.Sprintf("/api/fest/%d", festID)
	call := func(method, path string, body any, token string) *httptest.ResponseRecorder {
		t.Helper()
		return scopedAPIRequest(t, srv, method, api+path, body, token)
	}

	resp := call(http.MethodPost, fmt.Sprintf("/multifest-venues/%d/invites", minskID), map[string]any{"label": "Минск"}, adminToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("mint: %d %s", resp.Code, resp.Body.String())
	}
	links := decodeInto[[]struct {
		Code  string `json:"code"`
		Venue int64  `json:"multifest_venue_id"`
	}](t, resp)
	if len(links) != 1 || links[0].Venue != minskID {
		t.Fatalf("links: %+v", links)
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPost, "/api/join/"+links[0].Code, nil, hostToken); resp.Code != http.StatusOK {
		t.Fatalf("join: %d %s", resp.Code, resp.Body.String())
	}

	write := func(gameID int64) int {
		return call(http.MethodPatch, fmt.Sprintf("/games/%d/state", gameID),
			map[string]any{"ops": []map[string]any{{"path": []any{"completed"}, "value": []bool{true}}}}, hostToken).Code
	}
	if a, b := write(minskGame), write(brestGame); a != http.StatusOK || b != http.StatusForbidden {
		t.Fatalf("the venue host writes %d to their Game and %d to the other; want 200, 403", a, b)
	}

	type apiTeam struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Venue int64  `json:"multifest_venue_id"`
	}
	names := func() []string {
		t.Helper()
		var out []string
		for _, team := range decodeInto[[]apiTeam](t, call(http.MethodGet, "/teams", nil, hostToken)) {
			out = append(out, team.Name)
		}
		slices.Sort(out)
		return out
	}
	if got := names(); !slices.Equal(got, []string{"Альфа", "Бета"}) {
		t.Fatalf("the venue host sees the teams %v", got)
	}
	var gamma int64
	db.QueryRow(`select id from fest_teams where fest_id = ? and name = 'Гамма'`, festID).Scan(&gamma)
	if resp := call(http.MethodPut, fmt.Sprintf("/teams/%d", gamma), map[string]any{"name": "Гамма"}, hostToken); resp.Code != http.StatusForbidden {
		t.Fatalf("the venue host edited another venue's team: %d", resp.Code)
	}
	if resp := call(http.MethodPost, "/teams", map[string]any{"name": "Дельта"}, hostToken); resp.Code != http.StatusOK {
		t.Fatalf("add: %d %s", resp.Code, resp.Body.String())
	}
	var venue, number int64
	db.QueryRow(`select multifest_venue_id, venue_number from fest_teams where fest_id = ? and name = 'Дельта'`, festID).Scan(&venue, &number)
	if venue != minskID || number != 3 {
		t.Fatalf("the new team is on venue %d under %d; want %d, 3", venue, number, minskID)
	}
	gammaPlayer := map[string]any{"rating_id": 30, "first_name": "Гамма", "last_name": "Капитан"}
	if resp := call(http.MethodPost, "/teams", map[string]any{"name": "Эпсилон", "players": []any{gammaPlayer}}, hostToken); resp.Code != http.StatusBadRequest {
		t.Fatalf("the venue host took a player from another venue: %d", resp.Code)
	}

	if resp := call(http.MethodGet, fmt.Sprintf("/numbers?venue=%d", brestID), nil, hostToken); resp.Code != http.StatusForbidden {
		t.Fatalf("the venue host read another venue's numbers: %d", resp.Code)
	}
	numbered := decodeInto[[]struct {
		Number int `json:"number"`
	}](t, call(http.MethodGet, fmt.Sprintf("/numbers?venue=%d", minskID), nil, hostToken))
	if len(numbered) != 3 {
		t.Fatalf("the venue host's numbers: %+v", numbered)
	}
	if resp := call(http.MethodGet, "/teams/export.xlsx", nil, hostToken); resp.Code != http.StatusForbidden {
		t.Fatalf("the venue host exported the whole roster: %d", resp.Code)
	}

	members := decodeInto[[]struct {
		Nickname string  `json:"nickname"`
		Venues   []int64 `json:"multifest_venues"`
	}](t, call(http.MethodGet, "/access", nil, adminToken))
	for _, m := range members {
		if m.Nickname == "minsk" && !slices.Equal(m.Venues, []int64{minskID}) {
			t.Fatalf("the access list limits the venue host to %v", m.Venues)
		}
	}
}

// A fest with Games changes its type only when the host agrees to lose them,
// and leaving a synch fest takes its площадки and the teams' places on them.
func TestAFestChangesItsTypeByStartingItsGamesAfresh(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	host := srv.HostPageServer()
	res, err := db.Exec(`insert into fests(title, revision, created_at, updated_at, kind) values('Синхрон', 1, '2026-10-25', '2026-10-25', ?)`, multifest.Synch)
	if err != nil {
		t.Fatal(err)
	}
	festID, _ := res.LastInsertId()
	team := roster.FestRosterImportTeam{RatingID: 1, Name: "Альфа", Venue: multifest.RatingVenue{ID: 10, Title: "Минск"},
		Players: []roster.FestRosterImportPlayer{{RatingID: 10, FirstName: "Альфа", LastName: "Капитан"}}}
	if _, err := entrants.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, []roster.FestRosterImportTeam{team}, imports.RosterChoice{}); err != nil {
		t.Fatalf("import: %v", err)
	}
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := festops.CreateGameTx(ctx, tx, gamebuild.Spec{FestID: festID, Type: games.OD, Label: "ОД", TourComp: []int{3}})
		return err
	})
	count := func(q string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q, festID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	settings, err := host.LoadFestSettings(t.Context(), festID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Kind = multifest.Regular
	if err := host.UpdateFest(t.Context(), festID, settings); err == nil {
		t.Fatal("the type changed without agreeing to delete the Games")
	}
	if n := count(`select count(*) from games where fest_id = ?`); n != 1 {
		t.Fatalf("a refused change left %d Games", n)
	}
	settings.ReplaceGames = true
	if err := host.UpdateFest(t.Context(), festID, settings); err != nil {
		t.Fatal(err)
	}
	if kind, _ := multifest.FestKind(t.Context(), db, festID); kind != multifest.Regular {
		t.Fatalf("the fest is %s", kind)
	}
	if n := count(`select count(*) from games where fest_id = ?`); n != 0 {
		t.Fatalf("%d Games outlived the change", n)
	}
	if n := count(`select count(*) from multifest_venues where fest_id = ?`); n != 0 {
		t.Fatalf("%d площадки outlived the change", n)
	}
	if n := count(`select count(*) from fest_teams where fest_id = ? and deleted = 0 and multifest_venue_id is null and venue_number is null`); n != 1 {
		t.Fatalf("%d teams are off every площадка", n)
	}

	settings.Kind, settings.ReplaceGames = multifest.Synch, false
	if err := host.UpdateFest(t.Context(), festID, settings); err != nil {
		t.Fatalf("a fest without Games needs no agreement: %v", err)
	}
}
