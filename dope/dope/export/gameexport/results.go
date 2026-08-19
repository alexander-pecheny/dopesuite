package gameexport

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/storage/store"
)

// results.go exposes a computed "results" view for a game: the same total
// and per-tour standings the #results page renders client-side, but computed
// server-side so callers (bots, exports, integrations) don't have to replicate
// the scoring. Currently only OD games are supported; the scoring itself lives
// in the games package (games.ComputeODResults), shared with the xlsx export.

// HandleScopedGameResults serves GET /api/fest/{fid}/games/{gid}/results.
func HandleScopedGameResults(s Host, w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	// Read seq before the row (same ordering rationale as handleScopedGameState)
	// so the X-State-Seq we report is never ahead of the state we scored.
	seq := s.CurrentStateSeq(core.GameStateScope(gameID))
	doc, err := store.LoadGameDoc(r.Context(), s.DB(), festID, gameID)
	gameType, schemeJSON, stateJSON := doc.GameType, doc.SchemeJSON, doc.State
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if gameType != games.OD {
		http.Error(w, fmt.Sprintf("results view not available for game type %q", gameType), http.StatusBadRequest)
		return
	}
	results, err := games.ComputeODResults(schemeJSON, stateJSON)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body, err := json.Marshal(results)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("X-State-Seq", strconv.FormatUint(seq, 10))
	w.Header().Set("X-State-Epoch", s.Epoch())
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}
