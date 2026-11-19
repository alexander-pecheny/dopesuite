package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	_ "modernc.org/sqlite"
)

// An edit whose window the database refuses (another process, such as
// litestream, holding the write lock past the busy timeout) is answered 503,
// which the editor sends again; it used to be a 400, which the editor drops
// as an edit the server refused, so at Polyfest an ОД taker or a КСИ blank
// colour went back after its spinner and the host entered it twice.
func TestAnEditTheDatabaseRefusedIsToRetry(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	token := createTestSession(t, srv, systemUserID(t, db))
	seedFestTeams(t, db, festID, 4)
	odID := createGameThroughForm(t, srv, festID, token, map[string]string{"game_type": "od", "od_tours": "1", "od_questions": "2"})
	stateURL := fmt.Sprintf("/api/fest/%d/games/%d/state", festID, odID)
	tick := map[string]any{"ops": []map[string]any{{"path": []any{"entries", 0, 0}, "value": 3}}}

	var path string
	if err := db.QueryRow(`select file from pragma_database_list where name = 'main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx := context.Background()
	lock, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.ExecContext(ctx, "begin immediate"); err != nil {
		t.Fatal(err)
	}
	resp := scopedAPIRequest(t, srv, http.MethodPatch, stateURL, tick, token)
	if _, err := lock.ExecContext(ctx, "rollback"); err != nil {
		t.Fatal(err)
	}
	lock.Close()
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("tick under a foreign write lock: %d %s, want 503 for the editor to send it again", resp.Code, resp.Body.String())
	}

	resp = scopedAPIRequest(t, srv, http.MethodPatch, stateURL, tick, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("the tick sent again: %d %s", resp.Code, resp.Body.String())
	}
	var state struct {
		Entries [][]int `json:"entries"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Entries) == 0 || len(state.Entries[0]) == 0 || state.Entries[0][0] != 3 {
		t.Fatalf("entries %v, want team 3 on question 1", state.Entries)
	}
}
