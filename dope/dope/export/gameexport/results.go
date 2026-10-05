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
	"dope/dope/web/route"
)

// results.go exposes a computed "results" view for a game: the same total
// and per-tour standings the #results page renders client-side, but computed
// server-side so callers (bots, exports, integrations) don't have to replicate
// the scoring. OD games are supported, and a friendship cup answers its
// personal standings; the scoring itself lives in the games package
// (games.ComputeODResults, games.ComputeKDResults), shared with the xlsx export.

// decimal is the base the state seq header is written in.
const decimal = 10

// loadGameDoc loads the game a handler serves, answering 404 or the error
// itself when it cannot; ok is false when the response is already written.
func loadGameDoc(s Host, w http.ResponseWriter, r *http.Request, festID, gameID int64) (store.GameDoc, bool) {
	doc, err := store.LoadGameDoc(r.Context(), s.DB(), festID, gameID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return doc, false
	}
	if err != nil {
		route.WriteError(w, r, err)
		return doc, false
	}
	return doc, true
}

// HandleScopedGameResults serves GET /api/fest/{fid}/games/{gid}/results.
func HandleScopedGameResults(s Host, w http.ResponseWriter, r *http.Request, festID, gameID int64) {
	// Read seq before the row (same ordering rationale as handleScopedGameState)
	// so the X-State-Seq we report is never ahead of the state we scored.
	seq := s.CurrentStateSeq(core.GameStateScope(gameID))
	doc, ok := loadGameDoc(s, w, r, festID, gameID)
	if !ok {
		return
	}
	gameType, schemeJSON, stateJSON := doc.GameType, doc.SchemeJSON, doc.State
	def, known := games.Lookup(gameType)
	if !known || def.Results == nil {
		http.Error(w, fmt.Sprintf("results view not available for game type %q", gameType), http.StatusBadRequest)
		return
	}
	results, err := def.Results(schemeJSON, stateJSON)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	body, err := json.Marshal(results)
	if err != nil {
		route.WriteError(w, r, err)
		return
	}
	w.Header().Set("X-State-Seq", strconv.FormatUint(seq, decimal))
	w.Header().Set("X-State-Epoch", s.Epoch())
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}
