package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"
)

// A Троечка's Составы tab lists troikas, never the fest's teams: before its
// seed is known (seed: xlsx, seed: players) it seats nobody, and the tab used
// to fall back to the fest roster — Octobearfest's Троечка showed the 56 rating
// teams instead of its 37 troikas.
func TestTroikaRosterTabListsTroikasBeforeTheSeed(t *testing.T) {
	srv, festID, token, createGame := troikaSeedFest(t)
	dsl := "[init]\nseed: xlsx\n\n[scheme]\nkind: roundrobin\ngroup_size: 4\nthemes: 6\nmetric: total\npoints: [1, 0.5, 0]\n"
	gameID := createGame(url.Values{"game_type": {"troika"}, "troika_dsl": {dsl}})
	if got := gameEntrants(t, srv.Eng().DB, gameID); len(got) != 0 {
		t.Fatalf("the game seats %v before its seed", got)
	}
	tab := func() (bool, []string) {
		t.Helper()
		resp := scopedAPIRequest(t, srv, http.MethodGet, fmt.Sprintf("/api/fest/%d/games/%d/roster", festID, gameID), nil, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("roster = %d %s", resp.Code, resp.Body.String())
		}
		var out struct {
			Entrants bool `json:"entrants"`
			Teams    []struct {
				Name    string `json:"name"`
				Players []struct {
					Name string `json:"name"`
				} `json:"players"`
			} `json:"teams"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, team := range out.Teams {
			if len(team.Players) == 0 {
				t.Fatalf("%s has no people on the tab", team.Name)
			}
			names = append(names, team.Name)
		}
		return out.Entrants, names
	}

	// No entrant list yet: every troika of the fest.
	if troikas, names := tab(); !troikas || !slices.Equal(names, []string{"Василёк", "Лютик", "По коням", "Ромашка"}) {
		t.Fatalf("before the list: troikas=%v %v", troikas, names)
	}

	// The list names them; one declines and leaves the tab.
	if resp := scopedAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/fest/%d/games/%d/entrants/import", festID, gameID),
		map[string]any{"kind": "troikas"}, token); resp.Code != http.StatusOK {
		t.Fatalf("import = %d %s", resp.Code, resp.Body.String())
	}
	var lutik int64
	if err := srv.Eng().DB.QueryRow(`select id from participants where fest_id = ? and name = 'Лютик' and assembled = 1`, festID).Scan(&lutik); err != nil {
		t.Fatal(err)
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/games/%d/entrants/%d", festID, gameID, lutik),
		map[string]any{"declined": true}, token); resp.Code != http.StatusOK {
		t.Fatalf("decline = %d %s", resp.Code, resp.Body.String())
	}
	troikas, names := tab()
	if !troikas || len(names) != 3 || slices.Contains(names, "Лютик") || slices.Contains(names, "Астра") {
		t.Fatalf("from the list: troikas=%v %v", troikas, names)
	}
}
