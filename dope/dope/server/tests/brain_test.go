package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dope/dope/storage/store"

	"pecheny.me/dopecore/session"
)

// The whole brain rr-group loop through the real handlers: creating the game
// schedules the canonical KINSBF бои over the numbered fest teams with both
// slots seated; a per-бой PATCH lands in the raw protocol state; finishing the
// бой scores it into match_results and the rr stage into stage_standings.
func TestBrainGroupCreateEditFinish(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))

	// Names sort alphabetically OPPOSITE to their numbers, pinning the
	// entrant order to the seeding numbers, not the roster view's sort.
	for i, name := range []string{"Яблоко", "Ель", "Берёза", "Астра"} {
		if _, err := srv.Eng().DB.Exec(`
insert into fest_teams(fest_id, name, city, position, number) values(?, ?, '', ?, ?)`,
			festID, name, i+1, i+1); err != nil {
			t.Fatalf("insert fest team: %v", err)
		}
	}

	form := url.Values{"game_type": {"brain"}, "brain_dsl": {"[defaults]\nquestions: 5\n\n[scheme]\ntype: roundrobin\nteams_in_group: 4\n"}}
	createReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/new", festID), strings.NewReader(form.Encode()))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createReq.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	createResp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(createResp, createReq)
	if createResp.Code != http.StatusSeeOther {
		t.Fatalf("create brain game status = %d, body %s", createResp.Code, createResp.Body.String())
	}
	var gameID int64
	if err := srv.Eng().DB.QueryRow(`select id from games where fest_id = ? and game_type = 'brain'`, festID).Scan(&gameID); err != nil {
		t.Fatalf("brain game id: %v", err)
	}

	var kind string
	if err := srv.Eng().DB.QueryRow(`select kind from stages where game_id = ?`, gameID).Scan(&kind); err != nil || kind != "rr" {
		t.Fatalf("stage kind = %q, err %v; want rr", kind, err)
	}
	var boutCount, seatedSlots int
	if err := srv.Eng().DB.QueryRow(`select count(*) from matches where game_id = ?`, gameID).Scan(&boutCount); err != nil || boutCount != 6 {
		t.Fatalf("bout count = %d, err %v; want 6 (4-team KINSBF rounds)", boutCount, err)
	}
	if err := srv.Eng().DB.QueryRow(`
select count(*) from match_slots ms join matches m on m.id = ms.match_id
where m.game_id = ? and ms.participant_id is not null`, gameID).Scan(&seatedSlots); err != nil || seatedSlots != 12 {
		t.Fatalf("seated slots = %d, err %v; want 12", seatedSlots, err)
	}

	// Бой s1-g1-1 is Яблоко vs Ель (round 1 pairs numbers 1-2). Яблоко takes q1,q2.
	patch := map[string]any{"ops": []map[string]any{
		{"path": []any{"teams", 0, "rows", 0, "player"}, "value": "Игрок 1"},
		{"path": []any{"teams", 0, "rows", 0, "mark"}, "value": "right"},
		{"path": []any{"teams", 0, "rows", 1, "mark"}, "value": "right"},
		{"path": []any{"teams", 1, "rows", 2, "mark"}, "value": "wrong"},
	}}
	patchResp := scopedAPIRequest(t, srv, http.MethodPatch,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/s1-g1-1/state", festID, gameID), patch, token)
	if patchResp.Code != http.StatusOK {
		t.Fatalf("match patch status = %d, body %s", patchResp.Code, patchResp.Body.String())
	}
	view := decodeJSON[store.MatchView](t, patchResp)
	if len(view.Participants) != 2 || view.Participants[0].Name != "Яблоко" || view.Participants[1].Name != "Ель" {
		t.Fatalf("match view teams = %+v, want Яблоко vs Ель", view.Participants)
	}
	var state struct {
		Teams []struct {
			Rows []struct {
				Player string `json:"player"`
				Mark   string `json:"mark"`
			} `json:"rows"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(view.State, &state); err != nil {
		t.Fatalf("view state: %v (%s)", err, view.State)
	}
	if state.Teams[0].Rows[0].Mark != "right" || state.Teams[0].Rows[0].Player != "Игрок 1" || state.Teams[1].Rows[2].Mark != "wrong" {
		t.Fatalf("patched state = %s", view.State)
	}

	finishResp := scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/s1-g1-1/finish", festID, gameID),
		map[string]any{"finished": true}, token)
	if finishResp.Code != http.StatusOK {
		t.Fatalf("finish status = %d, body %s", finishResp.Code, finishResp.Body.String())
	}

	var placeA, placeB float64
	var takenA float64
	if err := srv.Eng().DB.QueryRow(`
select r.place, r.metrics_json ->> '$.taken' from match_results r
join participants tm on tm.id = r.participant_id join matches m on m.id = r.match_id
where m.game_id = ? and m.code = 's1-g1-1' and tm.name = 'Яблоко'`, gameID).Scan(&placeA, &takenA); err != nil {
		t.Fatalf("result Яблоко: %v", err)
	}
	if err := srv.Eng().DB.QueryRow(`
select r.place from match_results r
join participants tm on tm.id = r.participant_id join matches m on m.id = r.match_id
where m.game_id = ? and m.code = 's1-g1-1' and tm.name = 'Ель'`, gameID).Scan(&placeB); err != nil {
		t.Fatalf("result Ель: %v", err)
	}
	if placeA != 1 || placeB != 2 || takenA != 2 {
		t.Fatalf("s1-g1-1 places/taken = %v/%v taken %v, want 1/2 taken 2", placeA, placeB, takenA)
	}

	var points float64
	if err := srv.Eng().DB.QueryRow(`
select ss.metrics_json ->> '$.points' from stage_standings ss
join participants tm on tm.id = ss.participant_id join stages s on s.id = ss.stage_id
where s.game_id = ? and tm.name = 'Яблоко'`, gameID).Scan(&points); err != nil {
		t.Fatalf("standings Яблоко: %v", err)
	}
	if points != 2 {
		t.Fatalf("Яблоко group points = %v, want 2", points)
	}
}
