package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

func festRevision(t *testing.T, srv *server, festID int64) int64 {
	t.Helper()
	var rev int64
	if err := srv.db.QueryRow(`select revision from fests where id = ?`, festID).Scan(&rev); err != nil {
		t.Fatalf("fest revision: %v", err)
	}
	return rev
}

// TestEditBatchCoalescesConcurrentEdits asserts the editor-side batcher folds a
// burst of concurrent edits to one game into a SINGLE write window: every edit
// still succeeds and still gets its own fest revision (per-edit audit/journal
// granularity preserved), but the window fans out as ONE coalesced delta (one
// seq bump) rather than one broadcast per edit, and the final state reflects
// every edit.
func TestEditBatchCoalescesConcurrentEdits(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	organizerID, token := createAPITestSession(t, srv, "batch-editor")
	addAPITestOrganizer(t, srv, festID, organizerID)

	scopeKey := fmt.Sprintf("game-state:%d", gameID)
	seqBefore := srv.currentStateSeq(scopeKey)
	revBefore := festRevision(t, srv, festID)

	path := fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID)
	const n = 6
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := map[string]any{"ops": []map[string]any{
				{"op": "set", "path": []any{"entries", 0, i}, "value": i + 1},
			}}
			resp := scopedAPIRequest(t, srv, http.MethodPatch, path, body, token)
			codes[i] = resp.Code
		}(i)
	}
	wg.Wait()

	for i, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("edit %d status = %d, want 200", i, c)
		}
	}

	// One fest revision per edit: the batch preserves per-edit journal granularity.
	if got := festRevision(t, srv, festID) - revBefore; got != n {
		t.Errorf("fest revision delta = %d, want %d (one per edit)", got, n)
	}
	// One coalesced broadcast for the whole window (one seq bump), not n.
	if got := srv.currentStateSeq(scopeKey) - seqBefore; got != 1 {
		t.Errorf("seq delta = %d, want 1 (coalesced broadcast)", got)
	}

	// Final committed state reflects every edit.
	getResp := scopedAPIRequest(t, srv, http.MethodGet, path, nil, token)
	if getResp.Code != http.StatusOK {
		t.Fatalf("get state status = %d", getResp.Code)
	}
	var state struct {
		Entries [][]int `json:"entries"`
	}
	if err := json.Unmarshal(getResp.Body.Bytes(), &state); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	if len(state.Entries) == 0 || len(state.Entries[0]) < n {
		t.Fatalf("entries[0] = %v, want at least %d cells", state.Entries, n)
	}
	for i := 0; i < n; i++ {
		if state.Entries[0][i] != i+1 {
			t.Errorf("entries[0][%d] = %d, want %d", i, state.Entries[0][i], i+1)
		}
	}
}

// TestEditBatchErrorIsolation asserts an invalid edit in a window fails on its
// own (its caller gets a 4xx) without rolling back the valid edits sharing the
// window.
func TestEditBatchErrorIsolation(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	organizerID, token := createAPITestSession(t, srv, "iso-editor")
	addAPITestOrganizer(t, srv, festID, organizerID)

	path := fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID)

	var wg sync.WaitGroup
	var validCode, invalidCode int
	wg.Add(2)
	go func() {
		defer wg.Done()
		body := map[string]any{"ops": []map[string]any{
			{"op": "set", "path": []any{"entries", 0, 0}, "value": 7},
		}}
		validCode = scopedAPIRequest(t, srv, http.MethodPatch, path, body, token).Code
	}()
	go func() {
		defer wg.Done()
		// Unsupported op type — rejected during apply, must not poison the window.
		body := map[string]any{"ops": []map[string]any{
			{"op": "remove", "path": []any{"entries", 0, 1}},
		}}
		invalidCode = scopedAPIRequest(t, srv, http.MethodPatch, path, body, token).Code
	}()
	wg.Wait()

	if validCode != http.StatusOK {
		t.Errorf("valid edit status = %d, want 200", validCode)
	}
	if invalidCode != http.StatusBadRequest {
		t.Errorf("invalid edit status = %d, want 400", invalidCode)
	}

	getResp := scopedAPIRequest(t, srv, http.MethodGet, path, nil, token)
	var state struct {
		Entries [][]int `json:"entries"`
	}
	if err := json.Unmarshal(getResp.Body.Bytes(), &state); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	if len(state.Entries) == 0 || len(state.Entries[0]) == 0 || state.Entries[0][0] != 7 {
		t.Fatalf("valid edit not persisted: entries = %v", state.Entries)
	}
}
