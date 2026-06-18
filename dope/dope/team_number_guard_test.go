package dopeserver

import (
	"fmt"
	"net/http"
	"testing"
)

// TestScopedGameStateRejectsUnnumberedTeams covers the Stage 2 editing guard:
// a game whose fest has any active unnumbered team cannot be edited (409), and
// editing is allowed again once every team is numbered.
func TestScopedGameStateRejectsUnnumberedTeams(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	organizerID, token := createAPITestSession(t, srv, "guard-patcher")
	addAPITestOrganizer(t, srv, festID, organizerID)

	// Start from a known roster: one active team with no number.
	if _, err := srv.db.Exec(`delete from fest_teams where fest_id = ?`, festID); err != nil {
		t.Fatalf("clear fest_teams: %v", err)
	}
	if _, err := srv.db.Exec(`
insert into fest_teams(fest_id, rating_id, name, city, position, number, deleted)
values(?, 201, 'Без номера', '', 1, null, 0)`, festID); err != nil {
		t.Fatalf("insert unnumbered team: %v", err)
	}

	path := fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID)
	patch := map[string]any{"ops": []map[string]any{{"op": "set", "path": []any{"entries", 0, 0}, "value": 1}}}

	if resp := scopedAPIRequest(t, srv, http.MethodPatch, path, patch, token); resp.Code != http.StatusConflict {
		t.Fatalf("patch with unnumbered team = %d, want 409; body %s", resp.Code, resp.Body.String())
	}

	// Number the team — editing is unblocked.
	if _, err := srv.db.Exec(`update fest_teams set number = 1 where fest_id = ? and rating_id = 201`, festID); err != nil {
		t.Fatalf("number team: %v", err)
	}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, path, patch, token); resp.Code != http.StatusOK {
		t.Fatalf("patch after numbering = %d, want 200; body %s", resp.Code, resp.Body.String())
	}
}
