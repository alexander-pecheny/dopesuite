package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"pecheny.me/dopecore/session"
)

// A compiled reseed Edge must actually resolve: group bouts are played, the
// host presses «Пересев», ranks follow the declared points_share/taken_share
// order, and the next block's seats fill from the ranks.
func TestBrainReseedFlow(t *testing.T) {
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

	dsl := "[defaults]\nquestions: 3\n\n[scheme]\ntype: roundrobin\ngroups: 2\nteams_in_group: 2\nproceeding_teams: 1\n---\n" +
		"type: roundrobin\nteams_in_group: 2\nreseed: true\nsorting: [points_share desc, taken_share desc]\n"
	form := url.Values{"game_type": {"brain"}, "brain_dsl": {dsl}}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/host/fest/%d/game/new", festID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})
	resp := httptest.NewRecorder()
	srv.HostPageServer().HandleHostRouter(resp, req)
	if resp.Code != http.StatusSeeOther {
		t.Fatalf("create game status = %d, body %s", resp.Code, resp.Body.String())
	}
	var brainID int64
	if err := srv.Eng().DB.QueryRow(`select id from games where fest_id = ? and game_type = 'brain'`, festID).Scan(&brainID); err != nil {
		t.Fatalf("brain game: %v", err)
	}

	// Snake deal of 4 seeds: группа 1 = Астра и Гинкго, группа 2 = Берёза и Вяз.
	playBout := func(code string, sideRights ...[2]int) {
		t.Helper()
		var ops []map[string]any
		for _, sr := range sideRights {
			for row := 0; row < sr[1]; row++ {
				ops = append(ops, map[string]any{"path": []any{"teams", sr[0], "rows", row, "mark"}, "value": "right"})
			}
		}
		if resp := scopedAPIRequest(t, srv, http.MethodPatch,
			fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/state", festID, brainID, code),
			map[string]any{"ops": ops}, token); resp.Code != http.StatusOK {
			t.Fatalf("patch %s = %d, body %s", code, resp.Code, resp.Body.String())
		}
		if resp := scopedAPIRequest(t, srv, http.MethodPost,
			fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/finish", festID, brainID, code),
			map[string]any{"finished": true}, token); resp.Code != http.StatusOK {
			t.Fatalf("finish %s = %d, body %s", code, resp.Code, resp.Body.String())
		}
	}
	playBout("s1-g1-1", [2]int{0, 3}) // Астра 3:0 Гинкго

	early := scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/stages/s2-reseed/reseed", festID, brainID), nil, token)
	if early.Code != http.StatusBadRequest {
		t.Fatalf("early reseed status = %d, want 400", early.Code)
	}

	playBout("s1-g2-1", [2]int{0, 2}, [2]int{1, 1}) // Берёза 2:1 Вяз

	if resp := scopedAPIRequest(t, srv, http.MethodPost,
		fmt.Sprintf("/api/fest/%d/games/%d/stages/s2-reseed/reseed", festID, brainID), nil, token); resp.Code != http.StatusOK {
		t.Fatalf("reseed status = %d, body %s", resp.Code, resp.Body.String())
	}

	// Both winners took every bout (points_share 1.0); Астра's 3/3 взятых beat
	// Берёза's 2/3.
	rows, err := srv.Eng().DB.Query(`
select tm.name, ss.metrics_json from stage_standings ss
join stages s on s.id = ss.stage_id
join teams tm on tm.id = ss.participant_id
where s.game_id = ? and s.code = 's2-reseed' order by ss.rank`, brainID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ranked []string
	var metrics []map[string]any
	for rows.Next() {
		var name, metricsJSON string
		if err := rows.Scan(&name, &metricsJSON); err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(metricsJSON), &m); err != nil {
			t.Fatal(err)
		}
		ranked = append(ranked, name)
		metrics = append(metrics, m)
	}
	if len(ranked) != 2 || ranked[0] != "Астра" || ranked[1] != "Берёза" {
		t.Fatalf("reseed order = %v, want [Астра Берёза]", ranked)
	}
	if metrics[0]["points_share"] != 1.0 || metrics[0]["taken_share"] != 1.0 {
		t.Fatalf("Астра metrics = %v", metrics[0])
	}
	if share := metrics[1]["taken_share"].(float64); share < 0.66 || share > 0.67 {
		t.Fatalf("Берёза taken_share = %v, want 2/3", share)
	}

	teamAt := func(slot int) string {
		var name string
		if err := srv.Eng().DB.QueryRow(`
select tm.name from match_slots ms
join matches m on m.id = ms.match_id
join teams tm on tm.id = ms.team_id
where m.game_id = ? and m.code = 's2-g1-1' and ms.slot_index = ?`, brainID, slot).Scan(&name); err != nil {
			t.Fatalf("seat %d: %v", slot, err)
		}
		return name
	}
	if a, b := teamAt(0), teamAt(1); a != "Астра" || b != "Берёза" {
		t.Fatalf("бой второго блока = %s vs %s, want Астра vs Берёза", a, b)
	}
}
