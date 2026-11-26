package hostpages

import (
	"context"
	"database/sql"
	"net/http"

	"dope/dope/domain/core"
	"dope/dope/domain/roster"
	"dope/dope/web/route"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"
)

// A flat Game's entrants tab (roster.GameTeamsView): who plays it, the
// fest's whole team list or a list of its own, and the hand edits to that
// list. The game page's tab and an agent use the same routes.

type gameTeamsMode struct {
	Own bool `json:"own"`
}

type gameTeamsAdd struct {
	Team int64 `json:"team"`
}

func (s *Server) apiGameTeams(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	view, err := roster.LoadGameTeams(r.Context(), s.h.Engine().DB, sc.FestID, sc.GameID)
	if err != nil {
		return route.BadUser(err)
	}
	return route.JSON(w, view)
}

// apiSetGameTeamsMode is PUT {own}: a list of the Game's own, copied from the
// teams it seats now, or the fest's again.
func (s *Server) apiSetGameTeamsMode(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req gameTeamsMode
	return s.writeGameTeamsJSON(w, r, sc, &req, "game-teams:mode", func(ctx context.Context, tx *sql.Tx) (roster.GameStateBroadcast, error) {
		return roster.SetOwnTeamsTx(ctx, tx, sc.FestID, sc.GameID, req.Own)
	})
}

// apiAddGameTeam is POST {team}: a fest team onto the Game's own list.
func (s *Server) apiAddGameTeam(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req gameTeamsAdd
	return s.writeGameTeamsJSON(w, r, sc, &req, "game-teams:add", func(ctx context.Context, tx *sql.Tx) (roster.GameStateBroadcast, error) {
		return roster.AddGameTeamTx(ctx, tx, sc.FestID, sc.GameID, req.Team)
	})
}

// writeGameTeamsJSON reads the request's body into req, then runs the write.
func (s *Server) writeGameTeamsJSON(w http.ResponseWriter, r *http.Request, sc route.Scope, req any, event string,
	write func(ctx context.Context, tx *sql.Tx) (roster.GameStateBroadcast, error)) error {
	if err := route.DecodeJSON(r, req); err != nil {
		return err
	}
	return s.writeGameTeams(w, r, sc, event, write)
}

// apiRemoveGameTeam is DELETE …/teams/{team}: a team off the Game's own list.
func (s *Server) apiRemoveGameTeam(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	teamID, err := idstr.Parse(r.PathValue("team"))
	if err != nil || teamID <= 0 {
		return route.BadRequest("bad team")
	}
	return s.writeGameTeams(w, r, sc, "game-teams:remove", func(ctx context.Context, tx *sql.Tx) (roster.GameStateBroadcast, error) {
		return roster.RemoveGameTeamTx(ctx, tx, sc.FestID, sc.GameID, teamID)
	})
}

// writeGameTeams runs one write to the Game's list, sends the document it
// rewrote to the Game's open pages, and answers the tab afresh.
func (s *Server) writeGameTeams(w http.ResponseWriter, r *http.Request, sc route.Scope, event string,
	write func(ctx context.Context, tx *sql.Tx) (roster.GameStateBroadcast, error)) error {
	_, err := s.commit(r.Context(), sc.FestID, event, []int64{sc.GameID}, func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		update, err := write(ctx, tx)
		if err != nil {
			return core.FestWrite{}, err
		}
		written := core.FestWrite{Event: event, Payload: map[string]any{"gameID": sc.GameID}}
		written.Broadcast.States = []core.GameState{{GameID: update.GameID, StateJSON: update.StateJSON}}
		return written, nil
	})
	if err != nil {
		return route.BadUser(err)
	}
	return s.apiGameTeams(w, r, sc)
}
