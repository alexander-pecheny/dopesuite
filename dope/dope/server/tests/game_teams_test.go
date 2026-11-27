package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"dope/dope/domain/imports"
	"dope/dope/domain/roster"

	"code.pecheny.me/pecheny/dopesuite/dopecore/session"
)

// gameTeamsView is a flat Game's entrants tab as the API answers it.
type gameTeamsView struct {
	Own   bool             `json:"own"`
	Teams []roster.OwnTeam `json:"teams"`
}

func ownNames(teams []roster.OwnTeam) []string {
	out := make([]string, len(teams))
	for i, team := range teams {
		out[i] = team.Name
	}
	return out
}

// A flat Game plays the fest's teams until the host switches it to a
// separate list on its Участники tab: a copy of those teams, then the Game's
// own. The host renumbers, renames, adds and removes teams there; a team the
// Game holds answers for stays, and its answers follow it to a new number.
// The fest's new teams reach only the Games that play the fest's teams, and
// the list survives a switch back and forth.
func TestAFlatGameKeepsASeparateTeamList(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "own-teams", "Фест", systemUserID(t, db))
	seedFestTeams(t, db, festID, 3)
	// Teams made by hand, as the Команды page makes them: a roster write keeps
	// their rows (an unlinked row nobody made is written anew each time).
	if _, err := db.Exec(`update fest_teams set hand = 1 where fest_id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	api := func(method, path string, body any) (int, string) {
		t.Helper()
		resp := scopedAPIRequest(t, srv, method, fmt.Sprintf("/api/fest/%d%s", festID, path), body, token)
		return resp.Code, resp.Body.String()
	}
	create := func() int64 {
		t.Helper()
		code, body := api(http.MethodPost, "/games", map[string]any{"game_type": "od", "od_tours": 1, "od_questions": 3})
		var game struct {
			ID int64 `json:"id"`
		}
		if code != http.StatusOK || json.Unmarshal([]byte(body), &game) != nil {
			t.Fatalf("create: %d %s", code, body)
		}
		return game.ID
	}
	teamsOf := func(method string, gameID int64, path string, body any) gameTeamsView {
		t.Helper()
		code, out := api(method, fmt.Sprintf("/games/%d/teams%s", gameID, path), body)
		var got gameTeamsView
		if code != http.StatusOK || json.Unmarshal([]byte(out), &got) != nil {
			t.Fatalf("%s teams%s: %d %s", method, path, code, out)
		}
		return got
	}
	type seat struct {
		Name   string `json:"name"`
		Number int64  `json:"number"`
	}
	state := func(gameID int64) (seats []seat, entries [][]int64) {
		t.Helper()
		_, body := api(http.MethodGet, fmt.Sprintf("/games/%d/state", gameID), nil)
		var doc struct {
			Teams   []seat    `json:"teams"`
			Entries [][]int64 `json:"entries"`
		}
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("state: %s", body)
		}
		return doc.Teams, doc.Entries
	}
	seated := func(gameID int64) []string {
		t.Helper()
		seats, _ := state(gameID)
		out := make([]string, len(seats))
		for i, s := range seats {
			out[i] = s.Name
		}
		return out
	}
	all := []string{"Участник 1", "Участник 2", "Участник 3"}

	game, follower := create(), create()
	if got := teamsOf(http.MethodGet, game, "", nil); got.Own || !slices.Equal(ownNames(got.Teams), all) {
		t.Fatalf("a new game: %+v", got)
	}
	// Team 1 answers question 1.
	if code, body := api(http.MethodPatch, fmt.Sprintf("/games/%d/state", game), map[string]any{"ops": []map[string]any{
		{"op": "set", "path": []any{"entries", 0, 0}, "value": 1},
		{"op": "set", "path": []any{"completed", 0}, "value": true},
	}}); code != http.StatusOK {
		t.Fatalf("answer: %d %s", code, body)
	}
	// A list to save: what the tab sends, each team with the number it had.
	saved := func(got gameTeamsView) []roster.OwnTeam {
		out := slices.Clone(got.Teams)
		for i := range out {
			out[i].Prev = out[i].Number
		}
		return out
	}

	got := teamsOf(http.MethodPut, game, "", map[string]any{"own": true})
	if !got.Own || !slices.Equal(ownNames(got.Teams), all) || !got.Teams[0].Entered {
		t.Fatalf("separate list: %+v", got)
	}
	// Team 1 becomes number 7, team 2 is renamed, team 3 goes, and a team the
	// fest does not have comes in.
	list := saved(got)
	list[0].Number = 7
	list[1].Name = "Чёрный ЧГК"
	list = append(list[:2], roster.OwnTeam{Number: 9, Name: "Гости", City: "Самара", Players: []roster.OwnPlayer{{Name: "Иван Петров"}}})
	got = teamsOf(http.MethodPut, game, "/list", map[string]any{"teams": list})
	if want := []string{"Участник 1", "Чёрный ЧГК", "Гости"}; !slices.Equal(ownNames(got.Teams), want) || !got.Teams[0].Entered {
		t.Fatalf("after the edits: %+v", got)
	}
	seats, entries := state(game)
	if len(seats) != 3 || seats[0].Number != 7 || entries[0][0] != 7 {
		t.Fatalf("team 1's answer did not follow it to 7: %+v %v", seats, entries)
	}
	if s := seated(follower); !slices.Equal(s, all) {
		t.Fatalf("the other game seats %v", s)
	}

	// A team with answers cannot leave, a number cannot be given twice, and a
	// team needs a name.
	for name, teams := range map[string][]roster.OwnTeam{
		"answers":  saved(got)[1:],
		"twice":    append(saved(got), roster.OwnTeam{Number: 9, Name: "Ещё"}),
		"nameless": append(saved(got), roster.OwnTeam{Number: 10}),
	} {
		if code, body := api(http.MethodPut, fmt.Sprintf("/games/%d/teams/list", game), map[string]any{"teams": teams}); code == http.StatusOK {
			t.Fatalf("%s: saved %s", name, body)
		}
	}

	// The Составы tab shows the list's people.
	_, body := api(http.MethodGet, fmt.Sprintf("/games/%d/roster", game), nil)
	var rosterTab struct {
		Teams []roster.FestRosterTeamView `json:"teams"`
	}
	if err := json.Unmarshal([]byte(body), &rosterTab); err != nil || len(rosterTab.Teams) != 3 ||
		len(rosterTab.Teams[2].Players) != 1 || rosterTab.Teams[2].Players[0].Name != "Иван Петров" {
		t.Fatalf("roster tab: %s", body)
	}

	// A team the fest gains joins the Game that plays the fest's teams alone.
	if code, body := api(http.MethodPost, "/teams", map[string]any{"name": "Новички", "players": []any{}}); code != http.StatusOK {
		t.Fatalf("new fest team: %d %s", code, body)
	}
	if s := seated(follower); !slices.Contains(s, "Новички") {
		t.Fatalf("the follower missed the new team: %v", s)
	}
	if s := seated(game); slices.Contains(s, "Новички") {
		t.Fatalf("the separate list took the new team: %v", s)
	}

	// Back to the fest's teams and to the list again: the list is as it was.
	got = teamsOf(http.MethodPut, game, "", map[string]any{"own": false})
	if got.Own || !slices.Contains(ownNames(got.Teams), "Новички") {
		t.Fatalf("back to the fest's teams: %+v", got)
	}
	got = teamsOf(http.MethodPut, game, "", map[string]any{"own": true})
	if want := []string{"Участник 1", "Чёрный ЧГК", "Гости"}; !got.Own || !slices.Equal(ownNames(got.Teams), want) {
		t.Fatalf("the list again: %+v", got)
	}
}

// A separate list loads from a roster sheet: the sample the tab offers is
// the list itself, and a sheet's teams become the list, a team with no
// number taking the next one free.
func TestAFlatGameLoadsItsSeparateListFromASheet(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	festID := newFest(t, db, "own-sheet", "Фест", systemUserID(t, db))
	seedFestTeams(t, db, festID, 2)
	resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games", festID),
		map[string]any{"game_type": "od", "od_tours": 1, "od_questions": 3}, token)
	var game struct {
		ID int64 `json:"id"`
	}
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &game) != nil {
		t.Fatalf("create: %d %s", resp.Code, resp.Body.String())
	}
	base := fmt.Sprintf("/api/fest/%d/games/%d/teams", festID, game.ID)
	if resp := scopedAPIRequest(t, srv, http.MethodPut, base, map[string]any{"own": true}, token); resp.Code != http.StatusOK {
		t.Fatalf("own: %d %s", resp.Code, resp.Body.String())
	}
	if resp := scopedAPIRequest(t, srv, http.MethodGet, base+"/sample.xlsx", nil, token); resp.Code != http.StatusOK || resp.Body.Len() == 0 {
		t.Fatalf("sample: %d", resp.Code)
	}

	book, err := imports.RosterSheetXLSX([]roster.FestRosterImportTeam{
		{Number: 5, Name: "Тмин", City: "Самара", Players: []roster.FestRosterImportPlayer{{FirstName: "Анна", LastName: "Иванова"}}},
		{Name: "Перец"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "teams.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(book)
	form.Close()
	req := httptest.NewRequest(http.MethodPost, base+"/xlsx", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	rec := httptest.NewRecorder()
	srv.HandleScopedAPI(rec, req)
	var got gameTeamsView
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("xlsx: %d %s", rec.Code, rec.Body.String())
	}
	if len(got.Teams) != 2 || got.Teams[0].Number != 5 || got.Teams[1].Number != 6 ||
		len(got.Teams[0].Players) != 1 || got.Teams[0].Players[0].Name != "Анна Иванова" {
		t.Fatalf("loaded: %+v", got.Teams)
	}
}
