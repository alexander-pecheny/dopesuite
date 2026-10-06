package dopeserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/overrides"
	"dope/dope/domain/roster"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/web/route"

	"pecheny.me/dopecore/idstr"
)

// A Game's roster tab reads who the Game seats and the roster each team plays
// it with (CONTEXT.md, Game roster), and in the team buzzer formats the host
// edits a team's roster there for this Game only.

// scopedGameRoster serves the Game's roster tab. A Troika gets its troikas with
// their people, head team and division. A team buzzer format gets its teams
// with the rosters they play it with, and with ?choices=1 the fest's players to
// suggest. A Game with no teams of its own yet gets the fest roster, except a
// Troika, which lists troikas even before it seats them.
func (s *server) scopedGameRoster(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	ctx := r.Context()
	var gameType string
	if err := s.eng.DB.QueryRowContext(ctx, `select game_type from games where id = ? and fest_id = ?`, sc.GameID, sc.FestID).Scan(&gameType); err != nil {
		return err
	}
	if roster.HandRoster(gameType) {
		return s.handGameRoster(w, r, sc)
	}
	if !games.SeatsTroikas(gameType) {
		return s.scopedFestRoster(w, r, sc)
	}
	entrants, err := s.troikaRoster(ctx, sc)
	if err != nil {
		return err
	}
	if entrants == nil {
		entrants = []roster.GameEntrantView{}
	}
	return route.JSON(w, map[string]any{"teams": entrants, "entrants": true})
}

// handGameRoster serves the roster tab of a team buzzer format: the Game's own
// teams if it has any, else the fest roster.
func (s *server) handGameRoster(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	ctx := r.Context()
	teams, err := roster.LoadGameRosters(ctx, s.eng.DB, sc.FestID, sc.GameID)
	if err != nil {
		return err
	}
	if len(teams) == 0 {
		return s.scopedFestRoster(w, r, sc)
	}
	out := map[string]any{"teams": teams, "game": true}
	if r.URL.Query().Get("choices") == "1" {
		choices, err := roster.LoadFestPlayerChoices(ctx, s.eng.DB, sc.FestID)
		if err != nil {
			return err
		}
		if choices == nil {
			choices = []roster.FestPlayerChoice{}
		}
		out["choices"] = choices
	}
	return route.JSON(w, out)
}

// troikaRoster is who a Troika's roster tab lists: the troikas it seats; while
// it seats nobody yet (a seed: players scheme seats them only once the seed is
// known), the troikas its entrant list names; with no list either, the
// troikas it would seat by default (imports.DefaultEntrants): those of its
// division, or every troika of the fest. Never the fest's teams: a Troika
// plays troikas.
func (s *server) troikaRoster(ctx context.Context, sc route.Scope) ([]roster.GameEntrantView, error) {
	seated, err := roster.LoadGameEntrantsView(ctx, s.eng.DB, sc.FestID, sc.GameID)
	if err != nil || len(seated) > 0 {
		return seated, err
	}
	list, err := imports.LoadListTx(ctx, s.eng.DB, core.FestScope{FestID: sc.FestID, GameID: sc.GameID})
	if err != nil {
		return nil, err
	}
	ids := list.Active()
	if len(ids) == 0 {
		declared, err := imports.LoadDeclared(ctx, s.eng.DB, sc.GameID)
		if err != nil {
			return nil, err
		}
		division, _ := declared.EntrantDivision()
		troikas, err := imports.DefaultEntrants(ctx, s.eng.DB, sc.FestID, imports.KindTroika, division, 0)
		if err != nil {
			return nil, err
		}
		for _, t := range troikas {
			ids = append(ids, t.ParticipantID)
		}
	}
	return roster.LoadParticipantsView(ctx, s.eng.DB, sc.FestID, ids)
}

type gameRosterRequest struct {
	Players []string `json:"players"`
}

// scopedGameRosterPut keeps one team's roster in this Game by hand.
func (s *server) scopedGameRosterPut(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	participantID, err := rosterParticipant(r)
	if err != nil {
		return err
	}
	var req gameRosterRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.writeGameRoster(w, r, sc, "game-roster:edit", participantID, func(ctx context.Context, tx *sql.Tx) error {
		return roster.SaveGameRosterTx(ctx, tx, sc.FestID, sc.GameID, participantID, req.Players)
	})
}

// scopedGameRosterReset gives a team back the roster it brought to this Game.
func (s *server) scopedGameRosterReset(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	participantID, err := rosterParticipant(r)
	if err != nil {
		return err
	}
	return s.writeGameRoster(w, r, sc, "game-roster:reset", participantID, func(ctx context.Context, tx *sql.Tx) error {
		return roster.ResetGameRosterTx(ctx, tx, sc.FestID, sc.GameID, participantID, func(ctx context.Context, tx *sql.Tx) error {
			return overrides.MaterializeGameRosterOverridesTx(ctx, tx, sc.FestID, sc.GameID)
		})
	})
}

func rosterParticipant(r *http.Request) (int64, error) {
	id, err := idstr.Parse(r.PathValue("participant"))
	if err != nil || id <= 0 {
		return 0, route.BadRequest("bad participant")
	}
	return id, nil
}

// writeGameRoster runs a roster write, then tells the Game's open pages, which
// fetch their bouts and the roster tab again, and answers with the Game's rosters.
func (s *server) writeGameRoster(w http.ResponseWriter, r *http.Request, sc route.Scope, event string, participantID int64, apply func(ctx context.Context, tx *sql.Tx) error) error {
	var revision int64
	err := s.eng.WithWriteTx(r.Context(), sc.FestID, event, func(ctx context.Context, tx *sql.Tx) error {
		if err := apply(ctx, tx); err != nil {
			return err
		}
		var err error
		revision, err = festwrite.BumpFestRevisionTx(ctx, tx, sc.FestID, event, util.MustJSON(map[string]any{
			"gameID":        sc.GameID,
			"participantID": participantID,
		}))
		return err
	})
	if err != nil {
		return route.BadUser(err)
	}
	s.eng.BroadcastState(sc.FestID, gameRosterScope(sc.GameID), revision, []byte(`{}`))
	teams, err := roster.LoadGameRosters(r.Context(), s.eng.DB, sc.FestID, sc.GameID)
	if err != nil {
		return err
	}
	return route.JSON(w, map[string]any{"teams": teams, "game": true})
}

// gameRosterScope is the event scope a Game's pages listen on for its rosters
// changing — a roster edited here, or a player override.
func gameRosterScope(gameID int64) string { return fmt.Sprintf("game-roster:%d", gameID) }
