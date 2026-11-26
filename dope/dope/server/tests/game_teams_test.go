package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
)

// A flat Game seats the fest's teams until the host gives it a list of its
// own on its Участники tab: the teams it seats at that moment, which then
// change only there. A team the Game holds answers for cannot leave it, and
// a team the fest gains later joins only the Games that follow the fest.
func TestAFlatGameKeepsATeamListOfItsOwn(t *testing.T) {
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
	type team struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Entered bool   `json:"entered"`
	}
	type view struct {
		Own        bool   `json:"own"`
		Teams      []team `json:"teams"`
		Candidates []team `json:"candidates"`
	}
	names := func(teams []team) []string {
		out := make([]string, len(teams))
		for i, t := range teams {
			out[i] = t.Name
		}
		return out
	}
	teamsOf := func(method string, gameID int64, path string, body any) view {
		t.Helper()
		code, out := api(method, fmt.Sprintf("/games/%d/teams%s", gameID, path), body)
		var got view
		if code != http.StatusOK || json.Unmarshal([]byte(out), &got) != nil {
			t.Fatalf("%s teams%s: %d %s", method, path, code, out)
		}
		return got
	}
	seated := func(gameID int64) []string {
		t.Helper()
		_, body := api(http.MethodGet, fmt.Sprintf("/games/%d/state", gameID), nil)
		var doc struct {
			Teams []struct {
				Name string `json:"name"`
			} `json:"teams"`
		}
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("state: %s", body)
		}
		out := make([]string, len(doc.Teams))
		for i, t := range doc.Teams {
			out[i] = t.Name
		}
		return out
	}
	all := []string{"Участник 1", "Участник 2", "Участник 3"}

	game, follower := create(), create()
	if got := teamsOf(http.MethodGet, game, "", nil); got.Own || !slices.Equal(names(got.Teams), all) {
		t.Fatalf("a new game: %+v", got)
	}
	// Team 1 answers question 1.
	if code, body := api(http.MethodPatch, fmt.Sprintf("/games/%d/state", game), map[string]any{"ops": []map[string]any{
		{"op": "set", "path": []any{"entries", 0, 0}, "value": 1},
		{"op": "set", "path": []any{"completed", 0}, "value": true},
	}}); code != http.StatusOK {
		t.Fatalf("answer: %d %s", code, body)
	}

	got := teamsOf(http.MethodPut, game, "", map[string]any{"own": true})
	if !got.Own || !slices.Equal(names(got.Teams), all) || len(got.Candidates) != 0 || !got.Teams[0].Entered {
		t.Fatalf("own list: %+v", got)
	}
	if code, body := api(http.MethodDelete, fmt.Sprintf("/games/%d/teams/%d", game, got.Teams[0].ID), nil); code == http.StatusOK {
		t.Fatalf("a team with answers left the game: %s", body)
	}
	third := got.Teams[2].ID
	got = teamsOf(http.MethodDelete, game, fmt.Sprintf("/%d", third), nil)
	if !slices.Equal(names(got.Teams), all[:2]) || !slices.Equal(names(got.Candidates), all[2:]) {
		t.Fatalf("after removing the third: %+v", got)
	}
	if s := seated(game); !slices.Equal(s, all[:2]) {
		t.Fatalf("the game seats %v", s)
	}
	if s := seated(follower); !slices.Equal(s, all) {
		t.Fatalf("the other game seats %v", s)
	}

	// A team the fest gains joins the Game that follows the fest alone.
	if code, body := api(http.MethodPost, "/teams", map[string]any{"name": "Новички", "players": []any{}}); code != http.StatusOK {
		t.Fatalf("new fest team: %d %s", code, body)
	}
	if s := seated(follower); !slices.Contains(s, "Новички") {
		t.Fatalf("the follower missed the new team: %v", s)
	}
	if s := seated(game); !slices.Equal(s, all[:2]) {
		t.Fatalf("the own list took the new team: %v", s)
	}

	got = teamsOf(http.MethodPost, game, "", map[string]any{"team": third})
	if !slices.Equal(names(got.Teams), all) {
		t.Fatalf("after adding the third back: %+v", got)
	}
	got = teamsOf(http.MethodPut, game, "", map[string]any{"own": false})
	if got.Own || !slices.Contains(names(got.Teams), "Новички") {
		t.Fatalf("back to the fest's list: %+v", got)
	}
	if s := seated(game); len(s) != 4 {
		t.Fatalf("back to the fest's list, the game seats %v", s)
	}
}
