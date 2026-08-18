package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"pecheny.me/dopecore/session"
)

// The declared-seed loop: a brain scheme with [init] seed: {od-code} compiles
// to Посев placeholders; «Import seed» snapshots the ОД game's CURRENT
// standings into the ladder and seats every seed slot; a decline moves
// everyone below up the ladder.
func TestBrainSeedImportFromOD(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	token := createTestSession(t, srv, systemUserID(t, srv.Eng().DB))

	names := []string{"Астра", "Берёза", "Вяз", "Гинкго"}
	for i, name := range names {
		if _, err := srv.Eng().DB.Exec(`
insert into fest_teams(fest_id, name, city, position, number) values(?, ?, '', ?, ?)`,
			festID, name, i+1, i+1); err != nil {
			t.Fatalf("insert fest team: %v", err)
		}
	}

	createGame := func(form url.Values) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/new", festID), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
		resp := httptest.NewRecorder()
		srv.HostPageServer().HandleHostRouter(resp, req)
		if resp.Code != http.StatusSeeOther {
			t.Fatalf("create game status = %d, body %s", resp.Code, resp.Body.String())
		}
	}
	createGame(url.Values{"game_type": {"od"}, "od_tours": {"1"}, "od_questions": {"3"}})

	var odGameID int64
	var odCode string
	if err := srv.Eng().DB.QueryRow(`select id, code from games where fest_id = ? and game_type = 'od'`, festID).Scan(&odGameID, &odCode); err != nil {
		t.Fatalf("od game: %v", err)
	}
	// Partial ОД standings: Гинкго 3, Берёза 2, Астра 1, Вяз 0 — entered the
	// way the page enters them, so the game is scored and ranked.
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odGameID),
		map[string]any{"ops": []map[string]any{
			{"path": []any{"entries"}, "value": [][]int{{4, 2, 1}, {4, 2}, {4}}},
			{"path": []any{"completed"}, "value": []bool{true, true, true}},
		}}, token); resp.Code != http.StatusOK {
		t.Fatalf("seed od state: %d %s", resp.Code, resp.Body.String())
	}

	dsl := fmt.Sprintf("[defaults]\nquestions: 5\n\n[init]\nseed: %s\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\n", odCode)
	createGame(url.Values{"game_type": {"brain"}, "brain_dsl": {dsl}})
	var brainID int64
	if err := srv.Eng().DB.QueryRow(`select id from games where fest_id = ? and game_type = 'brain'`, festID).Scan(&brainID); err != nil {
		t.Fatalf("brain game: %v", err)
	}
	var seated int
	if err := srv.Eng().DB.QueryRow(`
select count(*) from match_slots ms join matches m on m.id = ms.match_id
where m.game_id = ? and ms.participant_id is not null`, brainID).Scan(&seated); err != nil || seated != 0 {
		t.Fatalf("pre-import seated = %d, err %v; want 0 (Посев placeholders)", seated, err)
	}

	runResp := scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/seed-import/run", festID, brainID), nil, token)
	if runResp.Code != http.StatusOK {
		t.Fatalf("seed-import/run status = %d, body %s", runResp.Code, runResp.Body.String())
	}

	teamAtSeat := func(matchCode string, slot int) string {
		t.Helper()
		var name string
		if err := srv.Eng().DB.QueryRow(`
select tm.name from match_slots ms
join matches m on m.id = ms.match_id
join participants tm on tm.id = ms.participant_id
where m.game_id = ? and m.code = ? and ms.slot_index = ?`, brainID, matchCode, slot).Scan(&name); err != nil {
			t.Fatalf("seat %s/%d: %v", matchCode, slot, err)
		}
		return name
	}
	// Round one pairs seeds 1-2 and 3-4 of the ОД standings.
	if a, b := teamAtSeat("s1-g1-1", 0), teamAtSeat("s1-g1-1", 1); a != "Гинкго" || b != "Берёза" {
		t.Fatalf("бой 1 = %s vs %s, want Гинкго vs Берёза", a, b)
	}
	if a, b := teamAtSeat("s1-g1-2", 0), teamAtSeat("s1-g1-2", 1); a != "Астра" || b != "Вяз" {
		t.Fatalf("бой 2 = %s vs %s, want Астра vs Вяз", a, b)
	}

	// Гинкго declines: everyone moves up the ladder, seed 1 is now Берёза.
	var ginkgoID int64
	if err := srv.Eng().DB.QueryRow(`select participant_id from game_assignments where game_id = ? and number = 1`, brainID).Scan(&ginkgoID); err != nil {
		t.Fatalf("assignment 1: %v", err)
	}
	declineResp := scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/seed-import/decline", festID, brainID),
		map[string]any{"teamID": ginkgoID, "declined": true}, token)
	if declineResp.Code != http.StatusOK {
		t.Fatalf("decline status = %d, body %s", declineResp.Code, declineResp.Body.String())
	}
	if a, b := teamAtSeat("s1-g1-1", 0), teamAtSeat("s1-g1-1", 1); a != "Берёза" || b != "Астра" {
		t.Fatalf("после отказа бой 1 = %s vs %s, want Берёза vs Астра", a, b)
	}

	// Бой 1 gets played; un-declining Гинкго shifts the ladder back, but a
	// finished бой holds its participants — the vacancy logic only moves
	// through unplayed matches.
	patch := map[string]any{"ops": []map[string]any{
		{"path": []any{"teams", 0, "rows", 0, "mark"}, "value": "right"},
	}}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/s1-g1-1/state", festID, brainID), patch, token); resp.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body %s", resp.Code, resp.Body.String())
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/matches/s1-g1-1/finish", festID, brainID),
		map[string]any{"finished": true}, token); resp.Code != http.StatusOK {
		t.Fatalf("finish status = %d, body %s", resp.Code, resp.Body.String())
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/seed-import/decline", festID, brainID),
		map[string]any{"teamID": ginkgoID, "declined": false}, token); resp.Code != http.StatusOK {
		t.Fatalf("un-decline status = %d, body %s", resp.Code, resp.Body.String())
	}
	if a, b := teamAtSeat("s1-g1-1", 0), teamAtSeat("s1-g1-1", 1); a != "Берёза" || b != "Астра" {
		t.Fatalf("сыгранный бой пересажен: %s vs %s, want Берёза vs Астра held", a, b)
	}
	var result string
	if err := srv.Eng().DB.QueryRow(`
select count(*) from match_results r join matches m on m.id = r.match_id
where m.game_id = ? and m.code = 's1-g1-1'`, brainID).Scan(&result); err != nil || result != "2" {
		t.Fatalf("finished бой results = %s err %v, want 2 kept", result, err)
	}
}
