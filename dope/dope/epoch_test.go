package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestScopedGameStateCarriesEpoch guards the seq-reset divergence fix: every
// delta envelope and the GET /state header must carry the server epoch, so a
// client can detect a restart (seq space reset) and resync instead of silently
// dropping post-restart deltas as "seq <= lastSeq".
func TestScopedGameStateCarriesEpoch(t *testing.T) {
	srv := newAuthTestServer(t)
	srv.epoch = "ep-test-123"
	festID, gameID := scopedAPITestIDs(t, srv)
	organizerID, token := createAPITestSession(t, srv, "epoch-editor")
	addAPITestOrganizer(t, srv, festID, organizerID)

	ch := make(chan event, 8)
	srv.addSubscriber(festID, ch, false, 0)
	defer srv.removeSubscriber(festID, ch)

	path := fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID)
	body := map[string]any{"ops": []map[string]any{{"op": "set", "path": []any{"entries", 0, 0}, "value": 1}}}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, path, body, token); resp.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", resp.Code, resp.Body.String())
	}
	wantScope := fmt.Sprintf("game-state:%d", gameID)
	srv.flushDelta(wantScope)

	select {
	case ev := <-ch:
		var env struct {
			Epoch string          `json:"epoch"`
			Ops   json.RawMessage `json:"ops"`
		}
		if err := json.Unmarshal(ev.data, &env); err != nil {
			t.Fatalf("decode envelope: %v (raw %s)", err, ev.data)
		}
		if env.Epoch != "ep-test-123" {
			t.Fatalf("delta envelope epoch = %q, want ep-test-123", env.Epoch)
		}
		if len(env.Ops) == 0 {
			t.Fatalf("expected a delta with ops, got %s", ev.data)
		}
	default:
		t.Fatal("expected a broadcast event, got none")
	}

	get := scopedAPIRequest(t, srv, http.MethodGet, path, nil, token)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d", get.Code)
	}
	if got := get.Header().Get("X-State-Epoch"); got != "ep-test-123" {
		t.Fatalf("X-State-Epoch = %q, want ep-test-123", got)
	}
}
