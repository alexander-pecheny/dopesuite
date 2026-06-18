package dopeserver

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestScopedGameArchive drives /api/fest/{fid}/games/{gid}/export.json.gz: it is
// host-gated (anon 401, role-less session 403, organizer 200), and the organizer
// download gunzips to a self-contained EK archive carrying the game's current
// relational state plus the edit history of an audited cell update.
func TestScopedGameArchive(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, gameID := scopedAPITestIDs(t, srv)
	path := fmt.Sprintf("/api/fest/%d/games/%d/export.json.gz", festID, gameID)

	anon := scopedAPIRequest(t, srv, http.MethodGet, path, nil, "")
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous archive status = %d, want 401", anon.Code)
	}

	_, readerToken := createAPITestSession(t, srv, "archive-reader")
	roleless := scopedAPIRequest(t, srv, http.MethodGet, path, nil, readerToken)
	if roleless.Code != http.StatusForbidden {
		t.Fatalf("role-less archive status = %d, want 403", roleless.Code)
	}

	organizerID, organizerToken := createAPITestSession(t, srv, "archive-organizer")
	addAPITestOrganizer(t, srv, festID, organizerID)

	// An audited cell edit so the archive's history has something game-scoped to
	// capture (writes answers + match_results + a matches revision bump).
	theme, answer, mark := 0, 0, "right"
	updatePath := fmt.Sprintf("/api/fest/%d/games/%d/matches/%s/update", festID, gameID, defaultMatchCode)
	upd := scopedAPIRequest(t, srv, http.MethodPost, updatePath, updateRequest{Team: 0, Theme: &theme, Answer: &answer, Mark: &mark}, organizerToken)
	if upd.Code != http.StatusOK {
		t.Fatalf("seed cell update status = %d, body %s", upd.Code, upd.Body.String())
	}

	resp := scopedAPIRequest(t, srv, http.MethodGet, path, nil, organizerToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("organizer archive status = %d, body %s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("Content-Type = %q, want application/gzip", ct)
	}
	if cd := resp.Header().Get("Content-Disposition"); cd == "" {
		t.Fatalf("missing Content-Disposition")
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	var archive gameArchive
	if err := json.Unmarshal(raw, &archive); err != nil {
		t.Fatalf("unmarshal archive: %v (body %s)", err, raw)
	}
	if archive.Format != "dope.game-archive.v2" {
		t.Fatalf("format = %q", archive.Format)
	}
	if archive.Game.GameID != gameID || archive.Game.FestID != festID {
		t.Fatalf("game ids = (%d,%d), want (%d,%d)", archive.Game.FestID, archive.Game.GameID, festID, gameID)
	}
	if archive.ExportedAt == "" {
		t.Fatalf("missing exportedAt")
	}
	// EK relational state: the fixture has at least one match.
	if len(archive.Rows["matches"]) == 0 {
		t.Fatalf("archive rows.matches is empty (rows keys: %v)", keysOf(archive.Rows))
	}
	// Fest-level context for offline name resolution.
	if archive.Fest == nil {
		t.Fatalf("archive fest context missing")
	}
	// The edit must surface in the game's relational state (answers carry marks).
	// The before/after audit trail is retired; edit history now lives in the
	// per-game journal, not the archive.
	if len(archive.Rows["answers"]) == 0 {
		t.Fatalf("archive rows.answers is empty after an edit (rows keys: %v)", keysOf(archive.Rows))
	}
}

func keysOf(m map[string][]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
