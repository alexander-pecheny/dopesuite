package dopeserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"dope/dope/games"
)

// game_results.go exposes a computed "results" view for a game: the same total
// and per-tour standings the #results page renders client-side, but computed
// server-side so callers (bots, exports, integrations) don't have to replicate
// the scoring. Currently only OD games are supported; the scoring itself lives
// in the games package (games.ComputeODResults), shared with the xlsx export.

func (s *server) handleScopedGameResults(w http.ResponseWriter, r *http.Request, scope festScope) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeFestRead(w, r, scope.FestID) {
		return
	}
	// Read seq before the row (same ordering rationale as handleScopedGameState)
	// so the X-State-Seq we report is never ahead of the state we scored.
	seq := s.currentStateSeq(fmt.Sprintf("game-state:%d", scope.GameID))
	var gameType, schemeJSON, stateJSON string
	err := s.db.QueryRowContext(r.Context(), `
select game_type, scheme_json, state_json from games where fest_id = ? and id = ?`,
		scope.FestID, scope.GameID).Scan(&gameType, &schemeJSON, &stateJSON)
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
	w.Header().Set("X-State-Epoch", s.epoch)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}
