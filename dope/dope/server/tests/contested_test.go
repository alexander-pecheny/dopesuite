package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"dope/dope/domain/games"
	"dope/dope/platform/roles"
)

// A host records спорные; the document every viewer gets carries them without
// their text; the jury reads the text, rules once per answer, and the ruling
// moves the standings either way; the fest's switch takes them all away.
func TestContestedAreRuledOnPerAnswer(t *testing.T) {
	t.Parallel()
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 3)
	gameID := createSchemeGame(t, db, festID, "od", "od", "")
	if _, err := db.Exec(`update fests set contested = 1, is_public = 1 where id = ?`, festID); err != nil {
		t.Fatal(err)
	}
	adminID, admin := createAPITestSession(t, srv, "boss")
	addAPITestRole(t, srv, festID, adminID, roles.Admin)
	hostID, host := createAPITestSession(t, srv, "host")
	addAPITestRole(t, srv, festID, hostID, roles.Host)
	juryID, jury := createAPITestSession(t, srv, "jury")
	addAPITestRole(t, srv, festID, juryID, roles.GameJury)
	base := fmt.Sprintf("/api/fest/%d/games/%d", festID, gameID)
	call := func(method, path string, body any, token string, want int) string {
		t.Helper()
		resp := scopedAPIRequest(t, srv, method, base+path, body, token)
		if resp.Code != want {
			t.Fatalf("%s %s: %d %s, want %d", method, path, resp.Code, resp.Body.String(), want)
		}
		return resp.Body.String()
	}
	contestedOf := func() []games.ODContested {
		t.Helper()
		var doc games.ODState
		if err := json.Unmarshal([]byte(call(http.MethodGet, "/state", nil, "", http.StatusOK)), &doc); err != nil {
			t.Fatal(err)
		}
		return doc.Contested
	}
	totals := func() map[int64]int {
		t.Helper()
		var results games.ODResults
		if err := json.Unmarshal([]byte(call(http.MethodGet, "/results", nil, "", http.StatusOK)), &results); err != nil {
			t.Fatal(err)
		}
		out := map[int64]int{}
		for _, team := range results.Teams {
			out[team.Number] = team.Total
		}
		return out
	}

	call(http.MethodPatch, "/state", map[string]any{"ops": []map[string]any{{"path": []any{"completed", 0}, "value": true}}}, host, http.StatusOK)
	call(http.MethodPost, "/contested", map[string]any{"question": 0, "number": 1, "answer": "Пушкин"}, host, http.StatusOK)
	call(http.MethodPost, "/contested", map[string]any{"question": 0, "number": 2, "answer": "  пушкин "}, host, http.StatusOK)
	call(http.MethodPost, "/contested", map[string]any{"question": 0, "number": 9, "answer": "Лермонтов"}, host, http.StatusBadRequest)

	if state := call(http.MethodGet, "/state", nil, "", http.StatusOK); strings.Contains(strings.ToLower(state), "пушкин") {
		t.Fatalf("a viewer's document carries the answer: %s", state)
	}
	if got := contestedOf(); len(got) != 2 || got[0].Status != games.ContestedPending {
		t.Fatalf("the document's спорные: %+v", got)
	}
	call(http.MethodGet, "/contested", nil, "", http.StatusUnauthorized)
	if texts := call(http.MethodGet, "/contested", nil, jury, http.StatusOK); !strings.Contains(texts, "Пушкин") {
		t.Fatalf("the jury cannot read the answers: %s", texts)
	}
	call(http.MethodPatch, "/state", map[string]any{"ops": []map[string]any{{"path": []any{"completed", 1}, "value": true}}}, jury, http.StatusForbidden)
	call(http.MethodPatch, "/state", map[string]any{"ops": []map[string]any{{"path": []any{"contested"}, "value": []any{}}}}, host, http.StatusBadRequest)
	call(http.MethodPut, "/contested/ruling", map[string]any{"question": 0, "answer": "Пушкин", "status": "accepted"}, host, http.StatusForbidden)

	call(http.MethodPut, "/contested/ruling", map[string]any{"question": 0, "answer": "ПУШКИН", "status": "accepted"}, jury, http.StatusOK)
	if got := totals(); got[1] != 1 || got[2] != 1 || got[3] != 0 {
		t.Fatalf("after the jury accepted: %v", got)
	}
	call(http.MethodPut, "/contested/ruling", map[string]any{"question": 0, "answer": "пушкин", "status": "rejected"}, admin, http.StatusOK)
	if got := totals(); got[1] != 0 || got[2] != 0 {
		t.Fatalf("after the admin rejected: %v", got)
	}
	if got := contestedOf(); len(got) != 2 || got[1].Status != games.ContestedRejected {
		t.Fatalf("a rejected спорный is kept: %+v", got)
	}
	// An answer the jury has already ruled on takes that ruling as it is recorded.
	call(http.MethodPost, "/contested", map[string]any{"question": 0, "number": 3, "answer": "Пушкин "}, host, http.StatusOK)
	if got := contestedOf(); len(got) != 3 || got[2].Status != games.ContestedRejected {
		t.Fatalf("a late спорный is not ruled: %+v", got)
	}
	call(http.MethodDelete, "/contested", map[string]any{"question": 0, "number": 3}, host, http.StatusOK)

	// Several teams with the same answer go in one save, all of them or none.
	call(http.MethodPost, "/contested", map[string]any{"question": 1, "numbers": []int{1, 3}, "answer": "Гоголь"}, host, http.StatusOK)
	call(http.MethodPost, "/contested", map[string]any{"question": 1, "numbers": []int{2, 9}, "answer": "Гоголь"}, host, http.StatusBadRequest)
	if got := contestedOf(); len(got) != 4 {
		t.Fatalf("after a save of two teams and a refused one: %+v", got)
	}

	if resp := scopedAPIRequest(t, srv, http.MethodPatch, fmt.Sprintf("/api/fest/%d/settings", festID), map[string]any{"contested": false}, admin); resp.Code != http.StatusOK {
		t.Fatalf("switch off: %d %s", resp.Code, resp.Body.String())
	}
	if got := contestedOf(); len(got) != 0 {
		t.Fatalf("a fest without спорные keeps them in the document: %+v", got)
	}
	call(http.MethodPost, "/contested", map[string]any{"question": 0, "number": 3, "answer": "Гоголь"}, host, http.StatusNotFound)
}
