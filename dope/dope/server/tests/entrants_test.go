package tests

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/entrants"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	"dope/dope/storage/store"

	"github.com/xuri/excelize/v2"
	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/session"
)

// The Участники tab (CONTEXT.md, Entrant list): the host imports a list, then
// edits it by hand. Each rule the server keeps is exercised here: an entrant
// with results can be neither removed, renamed nor moved; only a one-off is
// renamed in a Game; a decline hands the seat on only in бои nobody started.

func entrantsView(t *testing.T, db *sql.DB, festID, gameID int64) entrants.View {
	t.Helper()
	view, err := entrants.Load(t.Context(), db, core.FestScope{FestID: festID, GameID: gameID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return view
}

// mustEntrants takes a write's two results: mustEntrants(t)(entrants.Add(…)).
func mustEntrants(t *testing.T) func(entrants.Result, error) entrants.View {
	return func(result entrants.Result, err error) entrants.View {
		t.Helper()
		if err != nil {
			t.Fatalf("write to the list: %v", err)
		}
		return result.View
	}
}

// refused checks a write is turned down with a message a host reads, naming
// what it should.
func refused(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("accepted, want a refusal mentioning %q", want)
	}
	message, ok := corei18n.AsUser(err)
	if !ok {
		t.Fatalf("refusal is not for a person to read: %v", err)
	}
	if !strings.Contains(message, want) {
		t.Fatalf("refusal %q does not mention %q", message, want)
	}
}

func rowByName(t *testing.T, view entrants.View, name string) imports.SeedImportViewRow {
	t.Helper()
	for _, row := range view.Rows {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("%q is not in the list: %+v", name, view.Rows)
	return imports.SeedImportViewRow{}
}

func rowNames(view entrants.View) []string {
	var out []string
	for _, row := range view.Rows {
		out = append(out, row.Name)
	}
	return out
}

// seatOf is who holds a seed number of a Game.
func seatOf(t *testing.T, db *sql.DB, gameID int64, number int) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`
select participant_id from game_assignments where game_id = ? and basket = 1 and number = ?`, gameID, number).Scan(&id)
	if err == sql.ErrNoRows {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// matchOfSeed is the бой whose slot takes a seed number, and who sits there.
func matchOfSeed(t *testing.T, db *sql.DB, gameID int64, number int) (int64, int64) {
	t.Helper()
	var matchID int64
	var participant sql.NullInt64
	if err := db.QueryRow(`
select ms.match_id, ms.participant_id from match_slots ms join matches m on m.id = ms.match_id
where m.game_id = ? and ms.source_type = 'seed' and json_extract(ms.source_ref_json, '$.number') = ?
order by m.position, m.id limit 1`, gameID, number).Scan(&matchID, &participant); err != nil {
		t.Fatalf("бой of seed %d: %v", number, err)
	}
	return matchID, participant.Int64
}

func TestEntrantListImportsAndEditsByHand(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 6)
	// A брейн of four seats seeded by lot: the list fills four, two wait.
	gameID := createSchemeGame(t, db, festID, "brain", "Брейн",
		"[defaults]\nquestions: 3\n\n[init]\nseed: random\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n")
	scope := core.FestScope{FestID: festID, GameID: gameID}
	eng := srv.Eng()
	ctx := t.Context()

	view := mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceRandom}, nil))
	if len(view.Rows) != 6 || view.DrawSize != 4 || view.Edited {
		t.Fatalf("after the lot: %d rows, %d seats, edited %v", len(view.Rows), view.DrawSize, view.Edited)
	}
	waiting := 0
	for _, row := range view.Rows {
		if row.Waitlist {
			waiting++
		}
	}
	if waiting != 2 {
		t.Fatalf("waiting list = %d, want 2", waiting)
	}

	// The fifth moves to the top and takes seed 1.
	fifth := view.Rows[4]
	view = mustEntrants(t)(entrants.Move(eng, ctx, scope, fifth.TeamID, 1))
	if view.Rows[0].TeamID != fifth.TeamID || view.Rows[0].SeedNumber != 1 || !view.Edited {
		t.Fatalf("after the move: top %+v, edited %v", view.Rows[0], view.Edited)
	}
	if seatOf(t, db, gameID, 1) != fifth.TeamID {
		t.Fatal("seed 1 is not the moved entrant")
	}

	// Seed 1's бой begins: that entrant has results now.
	matchID, sitting := matchOfSeed(t, db, gameID, 1)
	if sitting != fifth.TeamID {
		t.Fatalf("seed 1's бой seats %d, want %d", sitting, fifth.TeamID)
	}
	if _, err := db.Exec(`update matches set status = 'finished' where id = ?`, matchID); err != nil {
		t.Fatal(err)
	}
	_, err := entrants.Remove(eng, ctx, scope, fifth.TeamID)
	refused(t, err, fifth.Name)
	_, err = entrants.Move(eng, ctx, scope, fifth.TeamID, 3)
	refused(t, err, fifth.Name)
	_, err = entrants.Rename(eng, ctx, scope, fifth.TeamID, "Другое имя")
	refused(t, err, "команд")

	// The second declines: the first on the waiting list moves up, and the
	// entrant whose бой has begun keeps its seed whatever moves around it.
	view = entrantsView(t, db, festID, gameID)
	second, firstWaiting := view.Rows[1], view.Rows[4]
	if !firstWaiting.Waitlist {
		t.Fatalf("row 5 is not waiting: %+v", firstWaiting)
	}
	view = mustEntrants(t)(entrants.Decline(eng, ctx, scope, second.TeamID, true))
	if seatOf(t, db, gameID, 1) != fifth.TeamID {
		t.Fatal("the entrant with results lost its seat")
	}
	if got := rowByName(t, view, firstWaiting.Name); got.Waitlist || got.SeedNumber == 0 {
		t.Fatalf("the first waiting did not move up: %+v", got)
	}
	if got := rowByName(t, view, second.Name); got.SeedNumber != 0 {
		t.Fatalf("the declined one still holds a seat: %+v", got)
	}
	// Moving another entrant above the one with results leaves its seat alone.
	view = mustEntrants(t)(entrants.Move(eng, ctx, scope, view.Rows[3].TeamID, 1))
	if seatOf(t, db, gameID, 1) != fifth.TeamID {
		t.Fatal("a move shifted the seat of an entrant with results")
	}

	// A one-off: added by name, renamed, refused under a name taken, removed
	// with its Participant.
	view = mustEntrants(t)(entrants.Add(eng, ctx, scope, entrants.AddRequest{Name: "Гости"}))
	guest := rowByName(t, view, "Гости")
	if !guest.OneOff {
		t.Fatalf("the typed entrant is not a one-off: %+v", guest)
	}
	view = mustEntrants(t)(entrants.Rename(eng, ctx, scope, guest.TeamID, "Гости издалека"))
	rowByName(t, view, "Гости издалека")
	_, err = entrants.Add(eng, ctx, scope, entrants.AddRequest{Name: "гости издалека"})
	refused(t, err, "гости издалека")
	_, err = entrants.Rename(eng, ctx, scope, guest.TeamID, view.Rows[0].Name)
	refused(t, err, view.Rows[0].Name)
	view = mustEntrants(t)(entrants.Remove(eng, ctx, scope, guest.TeamID))
	if slices.Contains(rowNames(view), "Гости издалека") {
		t.Fatal("the one-off stayed in the list")
	}
	var left int
	if err := db.QueryRow(`select count(*) from participants where id = ?`, guest.TeamID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("the one-off's Participant stayed: %d, %v", left, err)
	}

	// A fest team already in the list is refused; the candidates leave it out.
	var inList int64
	if err := db.QueryRow(`select id from fest_teams where fest_id = ? and name = ?`, festID, view.Rows[0].Name).Scan(&inList); err != nil {
		t.Fatal(err)
	}
	_, err = entrants.Add(eng, ctx, scope, entrants.AddRequest{Key: fmt.Sprintf("team:%d", inList)})
	refused(t, err, view.Rows[0].Name)
	for _, candidate := range view.Candidates {
		if candidate.Key == fmt.Sprintf("team:%d", inList) {
			t.Fatal("a team in the list is offered to add")
		}
	}
	// A troika cannot be typed in a team format's key, nor a one-off troika at all.
	_, err = entrants.Add(eng, ctx, scope, entrants.AddRequest{Key: "troika:1"})
	refused(t, err, "")

	// A re-import from the source applies the hand edits again (ADR-0025):
	// the entrant moved to the top is at the top, declines are kept. The
	// one-off added and removed again left nothing to replay.
	top := view.Rows[0]
	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceRandom}, nil))
	if !view.Edited || view.Rows[0].TeamID != top.TeamID {
		t.Fatalf("a re-import lost the hand edits: edited %v, top %+v, want %+v", view.Edited, view.Rows[0], top)
	}
	if !rowByName(t, view, second.Name).Declined {
		t.Fatal("the import forgot a decline")
	}
	// A fresh import takes the source's list alone.
	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceRandom, Fresh: true}, nil))
	if view.Edited {
		t.Fatal("a fresh import left the list marked as edited")
	}
	if !rowByName(t, view, second.Name).Declined {
		t.Fatal("the fresh import forgot a decline")
	}
}

// A one-off entrant belongs to its Game: the fest roster never shows it, no
// other Game offers it, a fest-wide lookup by its name makes another, and a
// rating import leaves it where it is.
func TestOneOffEntrantStaysInItsGame(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 4)
	// A брейн of the fest's teams, all against all in one group of four: its
	// Structure is built for its entrants, but a group of four takes four.
	gameID := createSchemeGame(t, db, festID, "brain", "Брейн", "[defaults]\nquestions: 3\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n")
	scope := core.FestScope{FestID: festID, GameID: gameID}

	// A fifth does not fit: the group stays, and the one-off waits.
	result, err := entrants.Add(srv.Eng(), t.Context(), scope, entrants.AddRequest{Name: "Сборная звёзд"})
	view := mustEntrants(t)(result, err)
	if result.Rebuilt || !view.Resizes || !strings.Contains(view.Kept, "5") {
		t.Fatalf("a fifth in a group of four: rebuilt %v, resizes %v, kept %q", result.Rebuilt, view.Resizes, view.Kept)
	}
	guest := rowByName(t, view, "Сборная звёзд")
	if !guest.Waitlist {
		t.Fatalf("the one-off does not wait: %+v", guest)
	}
	// A team declines: the group is built again with the one-off in it.
	result, err = entrants.Decline(srv.Eng(), t.Context(), scope, view.Rows[3].TeamID, true)
	view = mustEntrants(t)(result, err)
	if !result.Rebuilt || view.Kept != "" {
		t.Fatalf("a decline did not rebuild the group: rebuilt %v, kept %q", result.Rebuilt, view.Kept)
	}
	if got := gameEntrants(t, db, gameID); len(got) != 4 || !slices.Contains(got, guest.TeamID) {
		t.Fatalf("the group seats %v, want the one-off among four", got)
	}

	var inRoster int
	if err := db.QueryRow(`select count(*) from fest_teams where fest_id = ? and name = 'Сборная звёзд'`, festID).Scan(&inRoster); err != nil || inRoster != 0 {
		t.Fatalf("the one-off reached the fest roster: %d, %v", inRoster, err)
	}
	// The game form's entrant picker does not offer it.
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/host/fest/%d/game/new", festID), nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	resp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("game form: %d", resp.Code)
	}
	if strings.Contains(resp.Body.String(), "Сборная звёзд") {
		t.Fatal("the game form offers another Game's one-off")
	}
	// A fest-wide lookup by its name does not find it: it makes a Participant
	// of its own.
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		other, _, err := roster.EnsureSeedTeam(ctx, tx, festID, "Сборная звёзд", "", nil)
		if err == nil && other == guest.TeamID {
			t.Fatal("a fest-wide lookup by name found the one-off")
		}
		return err
	})

	// A rating import rewrites the fest roster; the one-off stays seated.
	var teams []roster.FestRosterImportTeam
	for i := 1; i <= 4; i++ {
		teams = append(teams, roster.FestRosterImportTeam{RatingID: int64(100 + i), Number: int64(i), Name: fmt.Sprintf("Участник %d", i)})
	}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 1, teams, imports.RosterChoice{}); err != nil {
		t.Fatalf("rating import: %v", err)
	}
	if !slices.Contains(gameEntrants(t, db, gameID), guest.TeamID) {
		t.Fatal("the rating import dropped the one-off")
	}
	rowByName(t, entrantsView(t, db, festID, gameID), "Сборная звёзд")
}

func countMatches(t *testing.T, db *sql.DB, gameID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`select count(*) from matches where game_id = ?`, gameID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A Тройка that takes a зачёт follows the troikas page until the host edits its
// list; an import from the зачёт makes it follow again; once the отбор has
// results a new troika waits on the list for a seat.
func TestTroikaListFollowsTheDivisionUntilEdited(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	eng := srv.Eng()
	ctx := t.Context()
	students := festTeamWithPlayers(t, db, festID, "Альфа", [][2]string{{"А", "Один"}, {"А", "Два"}, {"А", "Три"}, {"А", "Четыре"}, {"А", "Пять"}, {"А", "Шесть"}, {"А", "Семь"}, {"А", "Восемь"}})
	festTeamFlag(t, db, students, "Студ")

	add := func(name string, players ...string) int64 {
		var id int64
		withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
			written, err := entrants.AddTroikasTx(ctx, tx, festID, []roster.AssembledInput{{Name: name, Players: players}})
			if err == nil {
				id = written.Added[0]
			}
			return err
		})
		return id
	}
	s1 := add("С1", "А Один", "А Два")
	s2 := add("С2", "А Три", "А Четыре")
	gameID := createSchemeGameFor(t, db, festID, "troika", "Тройка — студенты",
		"[init]\ndivision: Студ\n\n[scheme]\nkind: flat\nwritten: true\nthemes: 1\nletters: false\n", nil)
	scope := core.FestScope{FestID: festID, GameID: gameID}
	if got := gameEntrants(t, db, gameID); !slices.Equal(got, []int64{s1, s2}) {
		t.Fatalf("entrants = %v", got)
	}
	view := entrantsView(t, db, festID, gameID)
	if view.Preselect.Kind != entrants.SourceTroikas || view.Preselect.Division != "Студ" || !view.Resizes {
		t.Fatalf("preselect %+v, resizes %v", view.Preselect, view.Resizes)
	}

	// A new troika joins while the list follows.
	s3 := add("С3", "А Пять", "А Шесть")
	if got := gameEntrants(t, db, gameID); !slices.Equal(got, []int64{s1, s2, s3}) {
		t.Fatalf("entrants after a new troika = %v", got)
	}
	if got := matchSeatIDs(t, db, gameID, "s1-m1"); !slices.Equal(got, []int64{s1, s2, s3}) {
		t.Fatalf("отбор seats = %v", got)
	}

	// A hand edit: the list stops following.
	mustEntrants(t)(entrants.Move(eng, ctx, scope, s3, 1))
	if got := gameEntrants(t, db, gameID); !slices.Equal(got, []int64{s3, s1, s2}) {
		t.Fatalf("entrants after the move = %v", got)
	}
	s4 := add("С4", "А Семь", "А Восемь")
	games, err := entrants.LoadDivisionGames(ctx, db, festID)
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || !games[0].Manual || games[0].Current {
		t.Fatalf("division game = %+v", games)
	}
	if slices.Contains(gameEntrants(t, db, gameID), s4) {
		t.Fatal("a troika joined a list the host had edited")
	}

	// An import from the зачёт applies the hand edit again, so the list
	// stays the host's; a fresh one follows again, and takes the new troika in.
	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceTroikas, Division: "Студ"}, nil))
	if !view.Edited || len(view.Rows) != 4 || view.Rows[0].Name != "С3" {
		t.Fatalf("after the import: edited %v, rows %v", view.Edited, rowNames(view))
	}
	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceTroikas, Division: "Студ", Fresh: true}, nil))
	if view.Edited || len(view.Rows) != 4 {
		t.Fatalf("after the import: edited %v, rows %v", view.Edited, rowNames(view))
	}

	// The отбор gets results, and a troika added by hand after that still
	// writes it: the written отбор grows a row for it.
	if _, err := db.Exec(`update matches set status = 'finished' where game_id = ?`, gameID); err != nil {
		t.Fatal(err)
	}
	withTx(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := roster.SaveAssembledTx(ctx, tx, festID, 0, roster.AssembledInput{Name: "С5", Players: []string{"А Один", "А Три"}})
		return err
	})
	var s5 int64
	if err := db.QueryRow(`select id from participants where fest_id = ? and name = 'С5'`, festID).Scan(&s5); err != nil {
		t.Fatal(err)
	}
	result, err := entrants.Add(eng, ctx, scope, entrants.AddRequest{Key: fmt.Sprintf("troika:%d", s5)})
	view = mustEntrants(t)(result, err)
	if !result.Rebuilt || view.Resizes || !view.Entered {
		t.Fatalf("the written отбор did not grow for a late troika: %v / %v / %v", result.Rebuilt, view.Resizes, view.Entered)
	}
	if got := rowByName(t, view, "С5"); got.Waitlist {
		t.Fatalf("the late troika waits instead of writing the отбор: %+v", got)
	}
	_, err = entrants.Add(eng, ctx, scope, entrants.AddRequest{Name: "Разовая тройка"})
	refused(t, err, "Тройки")
}

// The tab's routes: a host reads the list and edits it over HTTP, and each
// write answers the tab afresh.
func TestEntrantRoutes(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 5)
	gameID := createSchemeGame(t, db, festID, "brain", "Брейн",
		"[defaults]\nquestions: 3\n\n[init]\nseed: random\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n")
	base := fmt.Sprintf("/api/fest/%d/games/%d/entrants", festID, gameID)

	read := func(resp *httptest.ResponseRecorder) entrants.View {
		t.Helper()
		if resp.Code != http.StatusOK {
			t.Fatalf("%d %s", resp.Code, resp.Body.String())
		}
		return decodeJSON[entrants.View](t, resp)
	}
	view := read(scopedAPIRequest(t, srv, http.MethodGet, base, nil, token))
	if view.Kind != entrants.KindTeam || len(view.Sources) == 0 || len(view.Candidates) != 5 {
		t.Fatalf("empty tab: %+v", view)
	}
	view = read(scopedAPIRequest(t, srv, http.MethodPost, base+"/import", map[string]any{"kind": "fest"}, token))
	if len(view.Rows) != 5 {
		t.Fatalf("fest import rows = %d", len(view.Rows))
	}
	first := view.Rows[0].TeamID
	view = read(scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("%s/%d", base, first), map[string]any{"position": 5}, token))
	if view.Rows[4].TeamID != first {
		t.Fatal("the move did not land")
	}
	view = read(scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("%s/%d", base, first), map[string]any{"declined": true}, token))
	if !view.Rows[4].Declined {
		t.Fatal("the decline did not land")
	}
	view = read(scopedAPIRequest(t, srv, http.MethodPost, base, map[string]any{"name": "Гости"}, token))
	guest := rowByName(t, view, "Гости")
	read(scopedAPIRequest(t, srv, http.MethodDelete, fmt.Sprintf("%s/%d", base, guest.TeamID), nil, token))
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("%s/%d", base, first), map[string]any{"name": "Новое"}, token); resp.Code != http.StatusBadRequest {
		t.Fatalf("renaming a fest team answered %d", resp.Code)
	}
	// The old seed routes answer the same view.
	legacy := read(scopedAPIRequest(t, srv, http.MethodGet, fmt.Sprintf("/api/fest/%d/games/%d/seed-import", festID, gameID), nil, token))
	if len(legacy.Rows) != len(view.Rows)-1 {
		t.Fatalf("seed-import reads %d rows", len(legacy.Rows))
	}
}

// Личная СИ seats players: the list offers the fest's players, a one-off is a
// player typed by name, and an import from the fest drops a one-off it leaves
// out.
func TestPersonalSIListSeatsPlayers(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	eng := srv.Eng()
	ctx := t.Context()
	seedFestPlayers(t, db, festID, 4)
	gameID := createSchemeGame(t, db, festID, "si", "Личная СИ", "[scheme]\nkind: roundrobin\ngroup_size: 4\nthemes: 2\n")
	scope := core.FestScope{FestID: festID, GameID: gameID}
	matches := countMatches(t, db, gameID)
	if _, err := db.Exec(`insert into fest_players(fest_id, first_name, last_name) values(?, 'Игрок', 'Пятый')`, festID); err != nil {
		t.Fatal(err)
	}

	view := entrantsView(t, db, festID, gameID)
	if view.Kind != entrants.KindPlayer || view.Sources[0].Kind != entrants.SourceFest || !view.OneOffs || len(view.Rows) != 4 {
		t.Fatalf("личная СИ tab: kind %q, sources %+v, rows %v", view.Kind, view.Sources, rowNames(view))
	}
	if len(view.Candidates) != 1 || view.Candidates[0].Label != "Игрок Пятый" {
		t.Fatalf("candidates = %+v", view.Candidates)
	}
	// Five players from the fest into a table of four: the fifth waits.
	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceFest}, nil))
	if len(view.Rows) != 5 || !view.Rows[4].Waitlist || view.Kept == "" {
		t.Fatalf("after the import: %+v, kept %q", view.Rows, view.Kept)
	}
	if countMatches(t, db, gameID) != matches {
		t.Fatal("the Structure changed shape for a table of four")
	}
	// A player typed in is a one-off player.
	view = mustEntrants(t)(entrants.Add(eng, ctx, scope, entrants.AddRequest{Name: "Гость Издалека"}))
	guest := rowByName(t, view, "Гость Издалека")
	var kind string
	if err := db.QueryRow(`select roster from participants where id = ?`, guest.TeamID).Scan(&kind); err != nil || kind != "player" {
		t.Fatalf("the one-off is a %q, %v", kind, err)
	}
	// The fest's import brings the guest back, the host having added them;
	// a fresh one leaves the guest out, and its Participant goes.
	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceFest}, nil))
	if !slices.Contains(rowNames(view), "Гость Издалека") {
		t.Fatal("the import lost the one-off the host added")
	}
	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceFest, Fresh: true}, nil))
	if slices.Contains(rowNames(view), "Гость Издалека") {
		t.Fatal("a fresh import kept the one-off")
	}
	var left int
	if err := db.QueryRow(`select count(*) from participants where id = ?`, guest.TeamID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("the dropped one-off's Participant stayed: %d, %v", left, err)
	}
}

// A late entrant added to a written qualifier that is already being entered
// gets a seat, and the marks entered so far stay. Octobearfest 2026 lost the
// Своячок qualifier twice this way: an EK-shaped бой reports itself unstarted,
// so the rebuild for the new entrant wrote it a fresh, empty state.
func TestLateEntrantKeepsTheMarksOfAnUnfinishedBout(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	eng := srv.Eng()
	ctx := t.Context()
	seedFestPlayers(t, db, festID, 4)
	gameID := createSchemeGame(t, db, festID, "si", "Своячок", "[scheme]\nkind: flat\nthemes: 2\n")
	scope := core.FestScope{FestID: festID, GameID: gameID}
	mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceFest}, nil))

	var matchID, first int64
	if err := db.QueryRow(`
select ms.match_id, ms.participant_id from match_slots ms join matches m on m.id = ms.match_id
where m.game_id = ? and ms.participant_id is not null order by ms.slot_index limit 1`, gameID).Scan(&matchID, &first); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MutateMatchBlobTx(ctx, tx, matchID, func(blob *store.MatchBlob) error {
		blob.EnsureTheme(first, "regular", 0)
		blob.SetAnswer(first, "regular", 0, 1, "right")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	view := mustEntrants(t)(entrants.Add(eng, ctx, scope, entrants.AddRequest{Name: "Гость Издалека"}))
	if guest := rowByName(t, view, "Гость Издалека"); guest.Waitlist {
		t.Fatalf("the late entrant waits instead of taking a seat: %+v", view.Rows)
	}
	var seats int
	var state string
	if err := db.QueryRow(`select participant_count, state_json from matches where id = ?`, matchID).Scan(&seats, &state); err != nil {
		t.Fatalf("the бой is gone after the add: %v", err)
	}
	if seats != 5 {
		t.Fatalf("the бой seats %d, want 5", seats)
	}
	if !strings.Contains(state, `"right"`) {
		t.Fatalf("the marks entered before the add are lost: %s", state)
	}
}

// sheetOf is a seed sheet: the fest numbers in the order given.
func sheetOf(t *testing.T, numbers ...int) *bytes.Reader {
	t.Helper()
	book := excelize.NewFile()
	for i, n := range numbers {
		book.SetCellValue(book.GetSheetName(0), fmt.Sprintf("A%d", i+1), n)
	}
	var out bytes.Buffer
	if err := book.Write(&out); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(out.Bytes())
}

// The host seeds a Game from a source, fixes the list by hand, and the source
// changes (the ОД's results move on): a re-import takes the new order and
// applies the fixes to it again (ADR-0025), instead of losing them.
func TestReimportAppliesTheHandEditsToTheNewOrder(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 6)
	gameID := createSchemeGame(t, db, festID, "brain", "Брейн",
		"[defaults]\nquestions: 3\n\n[init]\nseed: xlsx\n\n[scheme]\nkind: roundrobin\ngroup_size: 6\n")
	scope := core.FestScope{FestID: festID, GameID: gameID}
	eng, ctx := srv.Eng(), t.Context()
	xlsx := entrants.Source{Kind: entrants.SourceXLSX}

	view := mustEntrants(t)(entrants.Import(eng, ctx, scope, xlsx, sheetOf(t, 1, 2, 3, 4, 5, 6)))
	two, five := rowByName(t, view, "Участник 2"), rowByName(t, view, "Участник 5")
	mustEntrants(t)(entrants.Remove(eng, ctx, scope, two.TeamID))
	view = mustEntrants(t)(entrants.Move(eng, ctx, scope, five.TeamID, 1))
	if view.Edits != 2 {
		t.Fatalf("edits = %d, want 2", view.Edits)
	}

	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, xlsx, sheetOf(t, 6, 5, 4, 3, 2, 1)))
	want := []string{"Участник 5", "Участник 6", "Участник 4", "Участник 3", "Участник 1"}
	if got := rowNames(view); !slices.Equal(got, want) {
		t.Fatalf("after the re-import: %v, want %v", got, want)
	}

	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceXLSX, Fresh: true}, sheetOf(t, 6, 5, 4, 3, 2, 1)))
	if got := rowNames(view); len(got) != 6 || got[0] != "Участник 6" || view.Edits != 0 {
		t.Fatalf("after a fresh import: %v, %d edits", got, view.Edits)
	}
}

// A move is a place in one source's order. An import from another source
// (the fest's table after a sheet, a Троечка's seeding after its troikas)
// keeps who plays but not the places: replaying them would undo the new
// order without a word. The view says how many moves it left behind.
func TestImportFromAnotherSourceDropsTheMoves(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 6)
	gameID := createSchemeGame(t, db, festID, "brain", "Брейн",
		"[defaults]\nquestions: 3\n\n[init]\nseed: xlsx\n\n[scheme]\nkind: roundrobin\ngroup_size: 6\n")
	scope := core.FestScope{FestID: festID, GameID: gameID}
	eng, ctx := srv.Eng(), t.Context()
	xlsx := entrants.Source{Kind: entrants.SourceXLSX}

	view := mustEntrants(t)(entrants.Import(eng, ctx, scope, xlsx, sheetOf(t, 6, 5, 4, 3, 2, 1)))
	two, five := rowByName(t, view, "Участник 2"), rowByName(t, view, "Участник 5")
	mustEntrants(t)(entrants.Remove(eng, ctx, scope, two.TeamID))
	mustEntrants(t)(entrants.Move(eng, ctx, scope, five.TeamID, 1))

	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceFest}, nil))
	want := []string{"Участник 1", "Участник 3", "Участник 4", "Участник 5", "Участник 6"}
	if got := rowNames(view); !slices.Equal(got, want) {
		t.Fatalf("after the fest import: %v, want %v (the removal kept, the move not)", got, want)
	}
	if view.MovesDropped != 1 || view.Edits != 1 {
		t.Fatalf("moves dropped %d, edits %d; want 1 and 1", view.MovesDropped, view.Edits)
	}
	// The tab is read back the moment the import lands (the fest broadcast
	// refreshes it): it still says so.
	read, err := entrants.Load(ctx, db, scope)
	if err != nil {
		t.Fatal(err)
	}
	if read.MovesDropped != 1 {
		t.Fatalf("read back: moves dropped %d, want 1", read.MovesDropped)
	}

	// The same source again: nothing to drop, the removal still applies.
	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, entrants.Source{Kind: entrants.SourceFest}, nil))
	if got := rowNames(view); !slices.Equal(got, want) || view.MovesDropped != 0 {
		t.Fatalf("after a second fest import: %v, %d dropped", got, view.MovesDropped)
	}
	if read, err := entrants.Load(ctx, db, scope); err != nil || read.MovesDropped != 0 {
		t.Fatalf("read back after the second import: %d dropped (%v), want 0", read.MovesDropped, err)
	}
}

// A team pulls out and another plays instead (the Октоберфест Троечка): the
// replacement takes the seed and the bouts of the one it replaces, and
// nobody else moves. A decline would move everybody below up a seat.
func TestReplaceKeepsEverybodyElseInTheirSeats(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 7)
	// Four seats, six in the list: two wait.
	gameID := createSchemeGame(t, db, festID, "brain", "Брейн",
		"[defaults]\nquestions: 3\n\n[init]\nseed: xlsx\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n")
	scope := core.FestScope{FestID: festID, GameID: gameID}
	eng, ctx := srv.Eng(), t.Context()
	xlsx := entrants.Source{Kind: entrants.SourceXLSX}

	view := mustEntrants(t)(entrants.Import(eng, ctx, scope, xlsx, sheetOf(t, 1, 2, 3, 4, 5, 6)))
	seats := func() []int64 {
		var out []int64
		for n := 1; n <= 4; n++ {
			out = append(out, seatOf(t, db, gameID, n))
		}
		return out
	}
	two, five := rowByName(t, view, "Участник 2"), rowByName(t, view, "Участник 5")
	if !five.Waitlist {
		t.Fatalf("Участник 5 is not waiting: %+v", five)
	}
	before := seats()

	// The host declined Участник 2 first, which moved 3 and 4 up; replacing
	// it puts them back where they were.
	mustEntrants(t)(entrants.Decline(eng, ctx, scope, two.TeamID, true))
	view = mustEntrants(t)(entrants.Replace(eng, ctx, scope, two.TeamID, entrants.AddRequest{Key: fmt.Sprintf("entrant:%d", five.TeamID)}))
	want := slices.Clone(before)
	want[1] = five.TeamID
	if got := seats(); !slices.Equal(got, want) {
		t.Fatalf("seats after the swap: %v, want %v", got, want)
	}
	if _, sitting := matchOfSeed(t, db, gameID, 2); sitting != five.TeamID {
		t.Fatalf("seed 2's бой seats %d, want %d", sitting, five.TeamID)
	}
	if got := rowNames(view); !slices.Equal(got, []string{"Участник 1", "Участник 5", "Участник 3", "Участник 4", "Участник 2", "Участник 6"}) {
		t.Fatalf("the list after the swap: %v", got)
	}

	// A fest team from outside the list takes the row, and the one replaced
	// leaves the list.
	var key string
	for _, candidate := range view.Candidates {
		if strings.HasPrefix(candidate.Label, "Участник 7") {
			key = candidate.Key
		}
	}
	if key == "" {
		t.Fatalf("Участник 7 is not a candidate: %+v", view.Candidates)
	}
	three := rowByName(t, view, "Участник 3")
	view = mustEntrants(t)(entrants.Replace(eng, ctx, scope, three.TeamID, entrants.AddRequest{Key: key}))
	seven := rowByName(t, view, "Участник 7")
	want[2] = seven.TeamID
	if got := seats(); !slices.Equal(got, want) {
		t.Fatalf("seats after the outside replacement: %v, want %v", got, want)
	}
	if slices.Contains(rowNames(view), "Участник 3") {
		t.Fatalf("Участник 3 is still in the list: %v", rowNames(view))
	}

	// A re-import from the same source applies the replacements again.
	view = mustEntrants(t)(entrants.Import(eng, ctx, scope, xlsx, sheetOf(t, 1, 2, 3, 4, 5, 6)))
	if got := seats(); !slices.Equal(got, want) {
		t.Fatalf("seats after the re-import: %v, want %v (list %v)", got, want, rowNames(view))
	}

	// Nobody replaces themselves, and nobody replaces an entrant whose бой has begun.
	_, err := entrants.Replace(eng, ctx, scope, seven.TeamID, entrants.AddRequest{Key: fmt.Sprintf("entrant:%d", seven.TeamID)})
	refused(t, err, "самим")
	matchID, _ := matchOfSeed(t, db, gameID, 1)
	if _, err := db.Exec(`update matches set status = 'finished' where id = ?`, matchID); err != nil {
		t.Fatal(err)
	}
	one := rowByName(t, view, "Участник 1")
	_, err = entrants.Replace(eng, ctx, scope, one.TeamID, entrants.AddRequest{Key: fmt.Sprintf("entrant:%d", two.TeamID)})
	refused(t, err, "Участник 1")
}
