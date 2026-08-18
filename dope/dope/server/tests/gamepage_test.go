package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

var pageBundle = regexp.MustCompile(`dist/([a-z]+)\.js`)

// A game type's page is one datum: the live viewer route and the lockdown
// snapshot serve the same bundle. Личная СИ borrows ЭК's page for its bracket,
// and lockdown used to hand it КСИ's blank instead.
func TestLockdownServesTheLivePage(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestPlayers(t, db, festID, 4)
	seedFestTeams(t, db, festID, 4)

	cases := []struct{ gameType, dsl, bundle string }{
		{"si", "[scheme]\nkind: roundrobin\ngroup_size: 4\nmatch_size: 2\nthemes: 2\n", "ek"},
		{"ksi", "", "si"},
		{"od", "", "od"},
		{"brain", "[scheme]\nkind: roundrobin\ngroup_size: 4\nmatch_size: 2\n", "brain"},
	}
	for _, c := range cases {
		gameID := createSchemeGame(t, db, festID, c.gameType, c.gameType, c.dsl)
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/fest/%d/game/%d", festID, gameID), nil)
		rec := httptest.NewRecorder()
		srv.HandleFestRouter(rec, req)
		live := pageBundle.FindStringSubmatch(rec.Body.String())
		snapshot, err := srv.StaticSnapshotHTML(festID, gameID)
		if err != nil {
			t.Fatalf("%s: snapshot: %v", c.gameType, err)
		}
		locked := pageBundle.FindStringSubmatch(string(snapshot))
		if live == nil || locked == nil {
			t.Fatalf("%s: no bundle in live (%d) or snapshot", c.gameType, rec.Code)
		}
		if live[1] != c.bundle || locked[1] != c.bundle {
			t.Errorf("%s: live serves %s, lockdown serves %s, want %s", c.gameType, live[1], locked[1], c.bundle)
		}
	}
}
