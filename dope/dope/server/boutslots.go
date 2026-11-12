package dopeserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"dope/dope/domain/boutslot"
	"dope/dope/domain/core"
	"dope/dope/web/route"
)

// slotSwapRequest names the bout of the same Round whose slot the bout in the
// path takes, and which takes the bout's.
type slotSwapRequest struct {
	With string `json:"with"`
}

// scopedMatchSlot swaps two bouts of one Round between their slots: each is
// played in the other's wave, at its venue and time (boutslot). Both bouts and
// the fest view are broadcast; a page reads the new order from the scheme the
// fest view carries.
func (s *server) scopedMatchSlot(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	mscope, err := s.matchScopeOf(r, sc)
	if err != nil {
		return err
	}
	var req slotSwapRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	var touched []int64
	revision, err := s.eng.CommitFestWrite(r.Context(), mscope.FestID, "match-slot", func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		var gameType string
		if err := tx.QueryRowContext(ctx, `select game_type from games where id = ?`, mscope.GameID).Scan(&gameType); err != nil {
			return core.FestWrite{}, err
		}
		ids, err := boutslot.SwapTx(ctx, tx, mscope.festScope, gameType, mscope.Code, req.With)
		if err != nil {
			return core.FestWrite{}, err
		}
		touched = ids
		return core.FestWrite{Event: "match:slot", Payload: map[string]any{"code": mscope.Code, "with": req.With}}, nil
	})
	if err != nil {
		return err
	}
	var own []byte
	for i, id := range touched {
		code := mscope.Code
		if i == 1 {
			code = req.With
		}
		bout := matchScope{festScope: mscope.festScope, MatchID: id, Code: code}
		view, err := s.loadScopedMatchViewSnapshot(bout)
		if err != nil {
			continue
		}
		data, err := json.Marshal(view)
		if err != nil {
			continue
		}
		s.eng.BroadcastState(sc.FestID, matchScopeKey(bout), revision, data)
		if i == 0 {
			own = data
		}
	}
	s.broadcastFestView(festScope{FestID: sc.FestID, GameID: sc.GameID}, revision)
	if own == nil {
		return route.JSON(w, map[string]any{"ok": true})
	}
	return route.JSONBytes(w, own)
}
