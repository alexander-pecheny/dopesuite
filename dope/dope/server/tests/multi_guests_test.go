package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	dopeserver "dope/dope/server"

	"pecheny.me/dopecore/session"
)

// A Мультиигры game's guest teams are the host's: added by name on the game's
// page, played like any team, and kept only in that game's document. The
// fest roster never learns of them, so ОД never seats them, and a rating
// re-import or a clear of the game leaves them where they were.
func TestMultiGuestTeams(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	initial := []roster.FestRosterImportTeam{{RatingID: 2, Name: "Альфа"}, {RatingID: 3, Name: "Бета"}, {RatingID: 5, Name: "Гамма"}}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, initial, imports.RosterChoice{}); err != nil {
		t.Fatalf("initial import: %v", err)
	}
	multiID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "multi", "multi_games": "Песни: {0,1,2}x3"})
	odID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "od", "od_tours": "1", "od_questions": "3"})
	base := fmt.Sprintf("/api/fest/%d/games/%d", festID, multiID)

	state := func() (st struct {
		Participants []games.KSIParticipant `json:"participants"`
		Declined     map[string]bool        `json:"declined"`
		Games        []struct {
			Cells [][]int `json:"cells"`
		} `json:"games"`
	}) {
		t.Helper()
		resp := scopedAPIRequest(t, srv, http.MethodGet, base+"/state", nil, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("state: %d %s", resp.Code, resp.Body.String())
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	row := func(name string) int {
		t.Helper()
		for i, p := range state().Participants {
			if p.Name == name {
				return i
			}
		}
		t.Fatalf("no team %q in %+v", name, state().Participants)
		return -1
	}

	for _, name := range []string{"Жюри", "Гости из Пинска", "Лишние"} {
		if resp := scopedAPIRequest(t, srv, http.MethodPost, base+"/guests", map[string]any{"name": name}, token); resp.Code != http.StatusOK {
			t.Fatalf("add %s: %d %s", name, resp.Code, resp.Body.String())
		}
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPost, base+"/guests", map[string]any{"name": "бета"}, token); resp.Code != http.StatusBadRequest {
		t.Fatalf("a guest named like a fest team: %d %s", resp.Code, resp.Body.String())
	}
	// Жюри scores two, Гости refuse, Бета scores one.
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, base+"/state", map[string]any{"ops": []map[string]any{
		{"path": []any{"games", 0, "cells", row("Жюри"), 1}, "value": 2},
		{"path": []any{"games", 0, "cells", row("Бета"), 0}, "value": 1},
		{"path": []any{"declined", "n-2"}, "value": true},
	}}, token); resp.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", resp.Code, resp.Body.String())
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPut, base+"/guests/-1", map[string]any{"name": "Жюри турнира"}, token); resp.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", resp.Code, resp.Body.String())
	}
	if resp := scopedAPIRequest(t, srv, http.MethodDelete, base+"/guests/-1", nil, token); resp.Code != http.StatusBadRequest {
		t.Fatalf("removing a guest with points: %d %s", resp.Code, resp.Body.String())
	}
	if resp := scopedAPIRequest(t, srv, http.MethodDelete, base+"/guests/-3", nil, token); resp.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", resp.Code, resp.Body.String())
	}

	// The guests rank with the fest's teams, and no Participant is minted for them.
	var guestParticipants int
	if err := db.QueryRow(`select count(*) from participants where fest_id = ? and name in ('Жюри турнира', 'Гости из Пинска')`, festID).Scan(&guestParticipants); err != nil {
		t.Fatal(err)
	}
	if guestParticipants != 0 {
		t.Errorf("%d fest Participants were minted for guest teams", guestParticipants)
	}
	var odState string
	if err := db.QueryRow(`select state_json from matches where game_id = ?`, odID).Scan(&odState); err != nil {
		t.Fatal(err)
	}
	var od struct {
		Teams []struct {
			Name string `json:"name"`
		} `json:"teams"`
	}
	if err := json.Unmarshal([]byte(odState), &od); err != nil || len(od.Teams) != 3 {
		t.Fatalf("ОД teams = %s", odState)
	}
	for _, team := range od.Teams {
		if team.Name == "Жюри турнира" || team.Name == "Гости из Пинска" {
			t.Errorf("ОД seats the guest team %q", team.Name)
		}
	}
	ranked, err := games.ComputeMultiResults(multiDoc(t, srv, multiID))
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 4 || ranked[0].Total != 2 || state().Participants[ranked[0].Index].Name != "Жюри турнира" {
		t.Fatalf("ranked = %+v", ranked)
	}

	// Re-import: Альфа leaves, Дельта comes, the fest's teams reorder.
	next := []roster.FestRosterImportTeam{{RatingID: 3, Name: "Бета"}, {RatingID: 5, Name: "Гамма"}, {RatingID: 9, Name: "Дельта"}}
	if _, err := imports.ImportFestRoster(srv.Eng(), t.Context(), festID, 13533, next, imports.RosterChoice{}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	after := state()
	names := []string{}
	for _, p := range after.Participants {
		names = append(names, p.Name)
	}
	if fmt.Sprint(names) != "[Бета Гамма Дельта Жюри турнира Гости из Пинска]" {
		t.Fatalf("teams after re-import = %v", names)
	}
	if after.Games[0].Cells[row("Жюри турнира")][1] != 2 || after.Games[0].Cells[row("Бета")][0] != 1 {
		t.Fatalf("cells after re-import = %v", after.Games[0].Cells)
	}
	if !games.KSIParticipantDeclined(after.Declined, after.Participants[row("Гости из Пинска")]) {
		t.Fatalf("the guests' Отказ was lost: %v", after.Declined)
	}

	// A clear wipes the points and keeps the guest teams.
	clearReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/%d/clear", festID, multiID), nil)
	clearReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	clearResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(clearResp, clearReq)
	if clearResp.Code != http.StatusSeeOther {
		t.Fatalf("clear: %d %s", clearResp.Code, clearResp.Body.String())
	}
	cleared := state()
	if len(cleared.Participants) != 5 || cleared.Participants[3].Name != "Жюри турнира" || cleared.Games[0].Cells[3][1] != 0 {
		t.Fatalf("after clear = %+v", cleared)
	}
}

// multiDoc is a flat game's scheme and the state its one Match holds.
func multiDoc(t *testing.T, srv *dopeserver.Server, gameID int64) (string, string) {
	t.Helper()
	var scheme, state string
	if err := srv.Eng().DB.QueryRow(`
select g.scheme_json, m.state_json from games g join matches m on m.game_id = g.id where g.id = ?`, gameID).Scan(&scheme, &state); err != nil {
		t.Fatal(err)
	}
	return scheme, state
}
