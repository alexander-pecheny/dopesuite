package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func scopedAPITestIDs(t *testing.T, srv *server) (int64, int64) {
	t.Helper()
	var tournamentID int64
	if err := srv.db.QueryRow(`select id from tournaments order by id limit 1`).Scan(&tournamentID); err != nil {
		t.Fatalf("tournament id: %v", err)
	}
	gameID, err := defaultGameID(t.Context(), srv.db, tournamentID)
	if err != nil {
		t.Fatalf("game id: %v", err)
	}
	return tournamentID, gameID
}

func createAPITestSession(t *testing.T, srv *server, username string) (int64, string) {
	t.Helper()
	tx, err := srv.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	now := utcNow()
	userID, err := insertReturningID(t.Context(), tx, `
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 0, ?, ?)`, username, now, now)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	token, err := createSessionTx(t.Context(), tx, userID, time.Now().UTC())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit user/session: %v", err)
	}
	return userID, token
}

func addAPITestOrganizer(t *testing.T, srv *server, tournamentID, userID int64) {
	t.Helper()
	if _, err := srv.db.Exec(`
insert into tournament_organizers(tournament_id, user_id, added_at)
values(?, ?, ?)`, tournamentID, userID, utcNow()); err != nil {
		t.Fatalf("add organizer: %v", err)
	}
}

func scopedAPIRequest(t *testing.T, srv *server, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	resp := httptest.NewRecorder()
	srv.handleScopedAPI(resp, req)
	return resp
}

func TestScopedAPIRequiresOrganizerForPrivateReadsAndWrites(t *testing.T) {
	srv := newAuthTestServer(t)
	tournamentID, gameID := scopedAPITestIDs(t, srv)
	gamePath := fmt.Sprintf("/api/tournament/%d/games/%d", tournamentID, gameID)

	publicRead := scopedAPIRequest(t, srv, http.MethodGet, gamePath, nil, "")
	if publicRead.Code != http.StatusOK {
		t.Fatalf("public read status = %d, body %s", publicRead.Code, publicRead.Body.String())
	}

	if _, err := srv.db.Exec(`update tournaments set is_public = 0 where id = ?`, tournamentID); err != nil {
		t.Fatalf("make private: %v", err)
	}
	privateAnonRead := scopedAPIRequest(t, srv, http.MethodGet, gamePath, nil, "")
	if privateAnonRead.Code != http.StatusNotFound {
		t.Fatalf("private anonymous read status = %d, want 404", privateAnonRead.Code)
	}

	_, nonOrganizerToken := createAPITestSession(t, srv, "reader")
	privateNonOrganizerRead := scopedAPIRequest(t, srv, http.MethodGet, gamePath, nil, nonOrganizerToken)
	if privateNonOrganizerRead.Code != http.StatusNotFound {
		t.Fatalf("private non-organizer read status = %d, want 404", privateNonOrganizerRead.Code)
	}

	organizerID, organizerToken := createAPITestSession(t, srv, "organizer")
	addAPITestOrganizer(t, srv, tournamentID, organizerID)
	privateOrganizerRead := scopedAPIRequest(t, srv, http.MethodGet, gamePath, nil, organizerToken)
	if privateOrganizerRead.Code != http.StatusOK {
		t.Fatalf("private organizer read status = %d, body %s", privateOrganizerRead.Code, privateOrganizerRead.Body.String())
	}

	theme := 0
	answer := 0
	mark := "right"
	updatePath := fmt.Sprintf("/api/tournament/%d/games/%d/matches/%s/update", tournamentID, gameID, defaultMatchCode)
	payload := updateRequest{Team: 0, Theme: &theme, Answer: &answer, Mark: &mark}

	anonymousWrite := scopedAPIRequest(t, srv, http.MethodPost, updatePath, payload, "")
	if anonymousWrite.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous write status = %d, want 401", anonymousWrite.Code)
	}
	nonOrganizerWrite := scopedAPIRequest(t, srv, http.MethodPost, updatePath, payload, nonOrganizerToken)
	if nonOrganizerWrite.Code != http.StatusForbidden {
		t.Fatalf("non-organizer write status = %d, want 403", nonOrganizerWrite.Code)
	}
	organizerWrite := scopedAPIRequest(t, srv, http.MethodPost, updatePath, payload, organizerToken)
	if organizerWrite.Code != http.StatusOK {
		t.Fatalf("organizer write status = %d, body %s", organizerWrite.Code, organizerWrite.Body.String())
	}
}

func TestScopedAPIImportRequiresTournamentOrganizer(t *testing.T) {
	srv := newAuthTestServer(t)
	tournamentID, _ := scopedAPITestIDs(t, srv)
	scheme := tournamentScheme{
		SchemaVersion: 2,
		Slug:          "authz-import",
		Title:         "authz import",
		GameType:      "ek",
		Stages: []schemeStage{{
			Code:      "r1",
			Title:     "r1",
			StageType: "matches",
			Position:  1,
			Matches: []schemeMatch{{
				Code:             defaultMatchCode,
				Title:            "A",
				ParticipantCount: 1,
				Slots: []schemeSlot{{
					Seed: &schemeSeedRef{Basket: 1, Number: 1},
				}},
			}},
		}},
		Teams: []schemeTeam{{Name: "Alpha", Basket: 1, Number: 1}},
	}

	path := fmt.Sprintf("/api/import?tournament_id=%d", tournamentID)
	body, _ := json.Marshal(scheme)

	anonReq := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	anonReq.Header.Set("Content-Type", "application/json")
	anonResp := httptest.NewRecorder()
	srv.handleImport(anonResp, anonReq)
	if anonResp.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous import status = %d, want 401", anonResp.Code)
	}

	_, nonOrganizerToken := createAPITestSession(t, srv, "import-reader")
	nonOrganizerReq := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	nonOrganizerReq.Header.Set("Content-Type", "application/json")
	nonOrganizerReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: nonOrganizerToken})
	nonOrganizerResp := httptest.NewRecorder()
	srv.handleImport(nonOrganizerResp, nonOrganizerReq)
	if nonOrganizerResp.Code != http.StatusForbidden {
		t.Fatalf("non-organizer import status = %d, want 403", nonOrganizerResp.Code)
	}

	organizerID, organizerToken := createAPITestSession(t, srv, "import-organizer")
	addAPITestOrganizer(t, srv, tournamentID, organizerID)
	organizerReq := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	organizerReq.Header.Set("Content-Type", "application/json")
	organizerReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: organizerToken})
	organizerResp := httptest.NewRecorder()
	srv.handleImport(organizerResp, organizerReq)
	if organizerResp.Code != http.StatusOK {
		t.Fatalf("organizer import status = %d, body %s", organizerResp.Code, organizerResp.Body.String())
	}
}

func TestScopedGameStatePatchMergesIndependentEdits(t *testing.T) {
	srv := newAuthTestServer(t)
	tournamentID, gameID := scopedAPITestIDs(t, srv)
	organizerID, token := createAPITestSession(t, srv, "state-patcher")
	addAPITestOrganizer(t, srv, tournamentID, organizerID)

	path := fmt.Sprintf("/api/tournament/%d/games/%d/state", tournamentID, gameID)
	patch := func(path []any, value any) map[string]any {
		return map[string]any{
			"ops": []map[string]any{{
				"op":    "set",
				"path":  path,
				"value": value,
			}},
		}
	}

	first := scopedAPIRequest(t, srv, http.MethodPatch, path, patch([]any{"entries", 0, 0}, 1), token)
	if first.Code != http.StatusOK {
		t.Fatalf("first patch status = %d, body %s", first.Code, first.Body.String())
	}
	second := scopedAPIRequest(t, srv, http.MethodPatch, path, patch([]any{"entries", 0, 1}, 2), token)
	if second.Code != http.StatusOK {
		t.Fatalf("second patch status = %d, body %s", second.Code, second.Body.String())
	}

	var state struct {
		Entries [][]int `json:"entries"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode patched state: %v", err)
	}
	if len(state.Entries) != 1 || len(state.Entries[0]) != 2 {
		t.Fatalf("entries shape = %#v, want one row with two values", state.Entries)
	}
	if state.Entries[0][0] != 1 || state.Entries[0][1] != 2 {
		t.Fatalf("entries = %#v, want both independent patches", state.Entries)
	}
}

func TestHostPresenceRequiresTournamentOrganizer(t *testing.T) {
	srv := newAuthTestServer(t)
	tournamentID, _ := scopedAPITestIDs(t, srv)
	path := fmt.Sprintf("/api/tournament/%d/presence", tournamentID)
	payload := map[string]any{
		"active": true,
		"cursor": map[string]any{
			"app":  "od",
			"kind": "entry",
			"q":    0,
			"row":  0,
		},
	}

	anonymous := scopedAPIRequest(t, srv, http.MethodPost, path, payload, "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous presence status = %d, want 401", anonymous.Code)
	}

	_, nonOrganizerToken := createAPITestSession(t, srv, "presence-reader")
	nonOrganizer := scopedAPIRequest(t, srv, http.MethodPost, path, payload, nonOrganizerToken)
	if nonOrganizer.Code != http.StatusForbidden {
		t.Fatalf("non-organizer presence status = %d, want 403", nonOrganizer.Code)
	}

	organizerID, organizerToken := createAPITestSession(t, srv, "presence-organizer")
	addAPITestOrganizer(t, srv, tournamentID, organizerID)
	organizer := scopedAPIRequest(t, srv, http.MethodPost, path, payload, organizerToken)
	if organizer.Code != http.StatusOK {
		t.Fatalf("organizer presence status = %d, body %s", organizer.Code, organizer.Body.String())
	}
}

func TestEventsRequireAuthorizedTournamentScope(t *testing.T) {
	srv := newAuthTestServer(t)
	tournamentID, _ := scopedAPITestIDs(t, srv)

	missingReq := httptest.NewRequest(http.MethodGet, "/events", nil)
	missingResp := httptest.NewRecorder()
	srv.handleEvents(missingResp, missingReq)
	if missingResp.Code != http.StatusBadRequest {
		t.Fatalf("missing tournament_id status = %d, want 400", missingResp.Code)
	}

	if _, err := srv.db.Exec(`update tournaments set is_public = 0 where id = ?`, tournamentID); err != nil {
		t.Fatalf("make private: %v", err)
	}
	privateReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/events?tournament_id=%d", tournamentID), nil)
	privateResp := httptest.NewRecorder()
	srv.handleEvents(privateResp, privateReq)
	if privateResp.Code != http.StatusNotFound {
		t.Fatalf("private anonymous events status = %d, want 404", privateResp.Code)
	}
}
