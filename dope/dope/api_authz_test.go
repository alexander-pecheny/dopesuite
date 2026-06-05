package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func scopedAPITestIDs(t *testing.T, srv *server) (int64, int64) {
	t.Helper()
	var festID int64
	if err := srv.db.QueryRow(`select id from fests order by id limit 1`).Scan(&festID); err != nil {
		t.Fatalf("fest id: %v", err)
	}
	gameID, err := defaultGameID(t.Context(), srv.db, festID)
	if err != nil {
		t.Fatalf("game id: %v", err)
	}
	return festID, gameID
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

func addAPITestOrganizer(t *testing.T, srv *server, festID, userID int64) {
	t.Helper()
	addAPITestRole(t, srv, festID, userID, festRoleAdmin)
}

func addAPITestRole(t *testing.T, srv *server, festID, userID int64, role string) {
	t.Helper()
	if _, err := srv.db.Exec(`
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, ?, ?)
on conflict(fest_id, user_id) do update set role = excluded.role`, festID, userID, role, utcNow()); err != nil {
		t.Fatalf("add role %s: %v", role, err)
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
	festID, gameID := scopedAPITestIDs(t, srv)
	gamePath := fmt.Sprintf("/api/fest/%d/games/%d", festID, gameID)

	publicRead := scopedAPIRequest(t, srv, http.MethodGet, gamePath, nil, "")
	if publicRead.Code != http.StatusOK {
		t.Fatalf("public read status = %d, body %s", publicRead.Code, publicRead.Body.String())
	}

	if _, err := srv.db.Exec(`update fests set is_public = 0 where id = ?`, festID); err != nil {
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
	addAPITestOrganizer(t, srv, festID, organizerID)
	privateOrganizerRead := scopedAPIRequest(t, srv, http.MethodGet, gamePath, nil, organizerToken)
	if privateOrganizerRead.Code != http.StatusOK {
		t.Fatalf("private organizer read status = %d, body %s", privateOrganizerRead.Code, privateOrganizerRead.Body.String())
	}

	theme := 0
	answer := 0
	mark := "right"
	updatePath := fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/update", festID, gameID, defaultMatchCode)
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

func TestHostRoleCanEditGameTablesOnly(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	hostID, hostToken := createAPITestSession(t, srv, "table-host")
	addAPITestRole(t, srv, festID, hostID, festRoleHost)

	theme := 0
	answer := 0
	mark := "right"
	updatePath := fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/update", festID, gameID, defaultMatchCode)
	updateResp := scopedAPIRequest(t, srv, http.MethodPost, updatePath, updateRequest{
		Team:   0,
		Theme:  &theme,
		Answer: &answer,
		Mark:   &mark,
	}, hostToken)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("host match update status = %d, body %s", updateResp.Code, updateResp.Body.String())
	}

	venueResp := scopedAPIRequest(t, srv, http.MethodPut, fmt.Sprintf("/api/fest/%d/venues/1", festID), venueUpdateRequest{Title: "Новая"}, hostToken)
	if venueResp.Code != http.StatusOK {
		t.Fatalf("host venue title update status = %d, body %s", venueResp.Code, venueResp.Body.String())
	}

	scheme := festScheme{SchemaVersion: 2, Slug: "host-import", Title: "Host import", GameType: "ek"}
	body, _ := json.Marshal(scheme)
	importReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/import?fest_id=%d", festID), bytes.NewReader(body))
	importReq.Header.Set("Content-Type", "application/json")
	importReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: hostToken})
	importResp := httptest.NewRecorder()
	srv.handleImport(importResp, importReq)
	if importResp.Code != http.StatusForbidden {
		t.Fatalf("host import status = %d, want 403", importResp.Code)
	}
}

func TestScopedAPIImportRequiresFestOrganizer(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	scheme := festScheme{
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
	}

	path := fmt.Sprintf("/api/import?fest_id=%d", festID)
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
	addAPITestOrganizer(t, srv, festID, organizerID)
	organizerReq := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	organizerReq.Header.Set("Content-Type", "application/json")
	organizerReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: organizerToken})
	organizerResp := httptest.NewRecorder()
	srv.handleImport(organizerResp, organizerReq)
	if organizerResp.Code != http.StatusOK {
		t.Fatalf("organizer import status = %d, body %s", organizerResp.Code, organizerResp.Body.String())
	}

	scheme.Teams = []schemeTeam{{Name: "Manual", Basket: 1, Number: 1}}
	body, _ = json.Marshal(scheme)
	manualTeamsReq := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	manualTeamsReq.Header.Set("Content-Type", "application/json")
	manualTeamsReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: organizerToken})
	manualTeamsResp := httptest.NewRecorder()
	srv.handleImport(manualTeamsResp, manualTeamsReq)
	if manualTeamsResp.Code != http.StatusBadRequest {
		t.Fatalf("manual teams import status = %d, want 400", manualTeamsResp.Code)
	}
}

func TestScopedGameStatePatchMergesIndependentEdits(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	organizerID, token := createAPITestSession(t, srv, "state-patcher")
	addAPITestOrganizer(t, srv, festID, organizerID)

	path := fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID)
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

func TestScopedGameStatePatchBroadcastsDelta(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	organizerID, token := createAPITestSession(t, srv, "delta-patcher")
	addAPITestOrganizer(t, srv, festID, organizerID)

	// Subscribe like an SSE viewer so we can read what the PATCH fans out.
	ch := make(chan event, 8)
	srv.addSubscriber(festID, ch, false)
	defer srv.removeSubscriber(festID, ch)

	path := fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID)
	patch := func(p []any, value any) map[string]any {
		return map[string]any{"ops": []map[string]any{{"op": "set", "path": p, "value": value}}}
	}

	type envelope struct {
		Scope   string          `json:"scope"`
		Seq     uint64          `json:"seq"`
		PrevSeq uint64          `json:"prevSeq"`
		Ops     json.RawMessage `json:"ops"`
		Data    json.RawMessage `json:"data"`
	}
	nextEnvelope := func() envelope {
		t.Helper()
		select {
		case ev := <-ch:
			var env envelope
			if err := json.Unmarshal(ev.data, &env); err != nil {
				t.Fatalf("decode envelope: %v (raw %s)", err, ev.data)
			}
			return env
		default:
			t.Fatal("expected a broadcast event, got none")
			return envelope{}
		}
	}

	wantScope := fmt.Sprintf("game-state:%d", gameID)

	if resp := scopedAPIRequest(t, srv, http.MethodPatch, path, patch([]any{"entries", 0, 0}, 1), token); resp.Code != http.StatusOK {
		t.Fatalf("first patch status = %d, body %s", resp.Code, resp.Body.String())
	}
	// Delta broadcasts coalesce over a window; force the flush so the test reads
	// the merged delta deterministically instead of waiting on the timer.
	srv.flushDelta(wantScope)
	first := nextEnvelope()
	if first.Scope != wantScope {
		t.Fatalf("scope = %q, want %q", first.Scope, wantScope)
	}
	if len(first.Ops) == 0 {
		t.Fatalf("first delta carried no ops: %+v", first)
	}
	if len(first.Data) != 0 {
		t.Fatalf("delta must not carry full state, got data %s", first.Data)
	}
	if first.Seq != 1 || first.PrevSeq != 0 {
		t.Fatalf("first delta seq/prevSeq = %d/%d, want 1/0", first.Seq, first.PrevSeq)
	}

	if resp := scopedAPIRequest(t, srv, http.MethodPatch, path, patch([]any{"entries", 0, 1}, 2), token); resp.Code != http.StatusOK {
		t.Fatalf("second patch status = %d, body %s", resp.Code, resp.Body.String())
	}
	srv.flushDelta(wantScope)
	second := nextEnvelope()
	if second.Seq != 2 || second.PrevSeq != 1 {
		t.Fatalf("second delta seq/prevSeq = %d/%d, want 2/1 (must chain)", second.Seq, second.PrevSeq)
	}

	// A resyncing client reads the current seq off the GET so its next delta chains.
	get := scopedAPIRequest(t, srv, http.MethodGet, path, nil, token)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d", get.Code)
	}
	if got := get.Header().Get("X-State-Seq"); got != "2" {
		t.Fatalf("X-State-Seq = %q, want \"2\"", got)
	}
}

// TestScopedMatchUpdateResponseCarriesBroadcastSeq guards the EK host stage
// skeleton-flash fix: a match update must return the same seq it broadcast, so
// the editor's locally-applied view chains onto the delta it also receives over
// SSE instead of gap-resyncing (which repaints the stage skeleton on every
// edit). Two consecutive edits must produce strictly increasing, chained seqs.
func TestScopedMatchUpdateResponseCarriesBroadcastSeq(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	organizerID, token := createAPITestSession(t, srv, "seq-editor")
	addAPITestOrganizer(t, srv, festID, organizerID)

	ch := make(chan event, 8)
	srv.addSubscriber(festID, ch, false)
	defer srv.removeSubscriber(festID, ch)

	type envelope struct {
		Scope   string `json:"scope"`
		Seq     uint64 `json:"seq"`
		PrevSeq uint64 `json:"prevSeq"`
	}
	nextSeq := func() envelope {
		t.Helper()
		select {
		case ev := <-ch:
			var env envelope
			if err := json.Unmarshal(ev.data, &env); err != nil {
				t.Fatalf("decode envelope: %v (raw %s)", err, ev.data)
			}
			return env
		default:
			t.Fatal("expected a broadcast event, got none")
			return envelope{}
		}
	}

	theme := 0
	mark := "right"
	updatePath := fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/update", festID, gameID, defaultMatchCode)
	edit := func(ans int) MatchView {
		t.Helper()
		a := ans
		resp := scopedAPIRequest(t, srv, http.MethodPost, updatePath, updateRequest{Team: 0, Theme: &theme, Answer: &a, Mark: &mark}, token)
		if resp.Code != http.StatusOK {
			t.Fatalf("update status = %d, body %s", resp.Code, resp.Body.String())
		}
		var view MatchView
		if err := json.Unmarshal(resp.Body.Bytes(), &view); err != nil {
			t.Fatalf("decode update response: %v", err)
		}
		return view
	}

	// Delta broadcasts coalesce over a window; flush so the merged broadcast is
	// emitted now. The response seq is the seq the window WILL flush as, so it
	// still equals the broadcast seq and chains across windows.
	matchScope := fmt.Sprintf("match:%d:%s", gameID, defaultMatchCode)

	firstView := edit(0)
	srv.flushDelta(matchScope)
	firstBroadcast := nextSeq()
	if firstView.Seq == 0 {
		t.Fatalf("update response carried no seq; editor cannot chain onto its own broadcast")
	}
	if firstView.Seq != firstBroadcast.Seq {
		t.Fatalf("response seq %d != broadcast seq %d", firstView.Seq, firstBroadcast.Seq)
	}

	secondView := edit(1)
	srv.flushDelta(matchScope)
	secondBroadcast := nextSeq()
	if secondView.Seq != secondBroadcast.Seq {
		t.Fatalf("second response seq %d != broadcast seq %d", secondView.Seq, secondBroadcast.Seq)
	}
	if secondBroadcast.PrevSeq != firstBroadcast.Seq || secondBroadcast.Seq != firstBroadcast.Seq+1 {
		t.Fatalf("second broadcast seq/prevSeq = %d/%d, want %d/%d (must chain)",
			secondBroadcast.Seq, secondBroadcast.PrevSeq, firstBroadcast.Seq+1, firstBroadcast.Seq)
	}
}

func TestScopedGameStateRejectsRatingRosterEdits(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	festID, chgkGameID, ksiGameID := createRosterPropagationFixture(t, db)
	srv := &server{db: db, subscribers: make(map[int64]map[chan event]bool)}
	organizerID, token := createAPITestSession(t, srv, "roster-editor")
	addAPITestOrganizer(t, srv, festID, organizerID)

	patch := func(path []any, value any) map[string]any {
		return map[string]any{
			"ops": []map[string]any{{
				"op":    "set",
				"path":  path,
				"value": value,
			}},
		}
	}

	chgkPath := fmt.Sprintf("/api/fest/%d/games/%d/state", festID, chgkGameID)
	chgkPatch := scopedAPIRequest(t, srv, http.MethodPatch, chgkPath, patch([]any{"teams", 0, "name"}, "Manual"), token)
	if chgkPatch.Code != http.StatusBadRequest {
		t.Fatalf("chgk team patch status = %d, want 400", chgkPatch.Code)
	}
	chgkEntriesPatch := scopedAPIRequest(t, srv, http.MethodPatch, chgkPath, patch([]any{"entries", 0, 0}, 1), token)
	if chgkEntriesPatch.Code != http.StatusOK {
		t.Fatalf("chgk entries patch status = %d, body %s", chgkEntriesPatch.Code, chgkEntriesPatch.Body.String())
	}
	chgkPut := scopedAPIRequest(t, srv, http.MethodPut, chgkPath, map[string]any{
		"teams": []map[string]string{{"name": "Manual"}},
	}, token)
	if chgkPut.Code != http.StatusBadRequest {
		t.Fatalf("chgk state replace status = %d, want 400", chgkPut.Code)
	}

	ksiPath := fmt.Sprintf("/api/fest/%d/games/%d/state", festID, ksiGameID)
	ksiPatch := scopedAPIRequest(t, srv, http.MethodPatch, ksiPath, patch([]any{"participants", 0}, "Manual"), token)
	if ksiPatch.Code != http.StatusBadRequest {
		t.Fatalf("ksi participant patch status = %d, want 400", ksiPatch.Code)
	}
	ksiAnswerPatch := scopedAPIRequest(t, srv, http.MethodPatch, ksiPath, patch([]any{"themes", 0, "answers", 0, 0}, "right"), token)
	if ksiAnswerPatch.Code != http.StatusOK {
		t.Fatalf("ksi answer patch status = %d, body %s", ksiAnswerPatch.Code, ksiAnswerPatch.Body.String())
	}
}

func TestHostPresenceRequiresFestOrganizer(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	path := fmt.Sprintf("/api/fest/%d/presence", festID)
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
	addAPITestOrganizer(t, srv, festID, organizerID)
	organizer := scopedAPIRequest(t, srv, http.MethodPost, path, payload, organizerToken)
	if organizer.Code != http.StatusOK {
		t.Fatalf("organizer presence status = %d, body %s", organizer.Code, organizer.Body.String())
	}
}

func TestEventsRequireAuthorizedFestScope(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)

	missingReq := httptest.NewRequest(http.MethodGet, "/events", nil)
	missingResp := httptest.NewRecorder()
	srv.handleEvents(missingResp, missingReq)
	if missingResp.Code != http.StatusBadRequest {
		t.Fatalf("missing fest_id status = %d, want 400", missingResp.Code)
	}

	if _, err := srv.db.Exec(`update fests set is_public = 0 where id = ?`, festID); err != nil {
		t.Fatalf("make private: %v", err)
	}
	privateReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/events?fest_id=%d", festID), nil)
	privateResp := httptest.NewRecorder()
	srv.handleEvents(privateResp, privateReq)
	if privateResp.Code != http.StatusNotFound {
		t.Fatalf("private anonymous events status = %d, want 404", privateResp.Code)
	}
}
