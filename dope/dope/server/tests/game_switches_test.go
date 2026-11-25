package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"dope/dope/platform/roles"
)

// Contested answers and venue names by room are a Game's settings: an OD's
// carries the contested switch, a KSI's does not, and either saves its own
// venue_rooms without touching the other Game's.
func TestContestedAndVenueRoomsAreAGamesSettings(t *testing.T) {
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
		Contested  *bool `json:"contested"`
		VenueRooms bool  `json:"venue_rooms"`
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
	if got := call(http.MethodGet, odID, nil); got.Contested == nil || *got.Contested || got.VenueRooms {
		t.Fatalf("a new OD of a regular fest: %+v, want contested off and venues by number", got)
	}
	if got := call(http.MethodGet, ksiID, nil); got.Contested != nil {
		t.Fatalf("a KSI takes no contested answers, yet its settings carry the switch")
	}
	if got := call(http.MethodPatch, odID, map[string]any{"contested": true, "venue_rooms": true}); got.Contested == nil || !*got.Contested || !got.VenueRooms {
		t.Fatalf("after the switches: %+v", got)
	}
	if got := call(http.MethodGet, ksiID, nil); got.VenueRooms {
		t.Fatalf("the OD's venue_rooms reached the KSI")
	}
}
