package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"dope/dope/platform/roles"
)

// Contested answers are a Game's setting: an OD's of a synch fest carries
// the switch, a regular fest's and a KSI's do not.
func TestContestedIsAGamesSetting(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 2)
	odID := createSchemeGame(t, db, festID, "od", "od", "")
	ksiID := createSchemeGame(t, db, festID, "ksi", "ksi", "")
	adminID, admin := createAPITestSession(t, srv, "boss")
	addAPITestRole(t, srv, festID, adminID, roles.Admin)
	type settings struct {
		Contested *bool `json:"contested"`
	}
	call := func(method string, gameID int64, body any) settings {
		t.Helper()
		resp := scopedAPIRequest(t, srv, method, fmt.Sprintf("/api/fest/%d/games/%d/settings", festID, gameID), body, admin)
		var got settings
		if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &got) != nil {
			t.Fatalf("%s %d: %d %s", method, gameID, resp.Code, resp.Body.String())
		}
		return got
	}
	if got := call(http.MethodGet, odID, nil); got.Contested != nil {
		t.Fatalf("a new OD of a regular fest: %+v, want no contested switch", got)
	}
	if _, err := db.Exec(`update fests set kind = 'synch' where id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	if got := call(http.MethodGet, odID, nil); got.Contested == nil || !*got.Contested {
		t.Fatalf("an OD of a synch fest: %+v, want contested on", got)
	}
	if got := call(http.MethodGet, ksiID, nil); got.Contested != nil {
		t.Fatalf("a KSI takes no contested answers, yet its settings carry the switch")
	}
	if got := call(http.MethodPatch, odID, map[string]any{"contested": false}); got.Contested == nil || *got.Contested {
		t.Fatalf("after the switch: %+v", got)
	}
}
