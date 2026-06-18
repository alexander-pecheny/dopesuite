package tests

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"strings"
	"testing"
)

// TestPatchEmitsEditMetric drives a real game-state PATCH through the handler
// with metrics on and asserts the per-edit line is emitted with the timing
// fields populated — guarding the exact handler/patchGameState wiring.
func TestPatchEmitsEditMetric(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	organizerID, token := createAPITestSession(t, srv, "metric-patcher")
	addAPITestOrganizer(t, srv, festID, organizerID)

	srv.Metrics().On = true // enable gating without starting the summary goroutine

	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) }()

	path := fmt.Sprintf("/api/fest/%d/games/%d/state", festID, gameID)
	body := map[string]any{"ops": []map[string]any{{"op": "set", "path": []any{"entries", 0, 0}, "value": 1}}}
	if resp := scopedAPIRequest(t, srv, http.MethodPatch, path, body, token); resp.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body %s", resp.Code, resp.Body.String())
	}

	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(l, "editmetric edit ") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("no 'editmetric edit' line emitted; log was:\n%s", buf.String())
	}
	for _, want := range []string{
		fmt.Sprintf("fest=%d", festID), fmt.Sprintf("game=%d", gameID),
		"ops=1", "wait_ms=", "hold_ms=", "db_ms=", "e2e_ms=", "bytes=",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("edit line missing %q: %s", want, line)
		}
	}
}
