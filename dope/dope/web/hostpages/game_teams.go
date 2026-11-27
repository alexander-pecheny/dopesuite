package hostpages

import (
	"context"
	"database/sql"
	"net/http"

	"dope/dope/domain/core"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	"dope/dope/web/route"
)

// A flat Game's entrants tab (roster.GameTeamsView): the fest's teams, or
// the Game's separate list, which the host edits by hand or loads from a
// roster sheet. The game page's tab and an agent use the same routes.

type gameTeamsMode struct {
	Own bool `json:"own"`
}

type gameTeamsList struct {
	Teams []roster.OwnTeam `json:"teams"`
}

func (s *Server) apiGameTeams(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	view, err := roster.LoadGameTeams(r.Context(), s.h.Engine().DB, sc.FestID, sc.GameID)
	if err != nil {
		return route.BadUser(err)
	}
	return route.JSON(w, view)
}

// apiSetGameTeamsMode is PUT {own}: the Game plays its separate list, made
// from the teams it seats the first time, or the fest's teams again.
func (s *Server) apiSetGameTeamsMode(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req gameTeamsMode
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.writeGameTeams(w, r, sc, "game-teams:mode", func(ctx context.Context, tx *sql.Tx) (roster.GameStateBroadcast, error) {
		return roster.SetOwnTeamsTx(ctx, tx, sc.FestID, sc.GameID, req.Own)
	})
}

// apiSaveGameTeams is PUT …/teams/list {teams}: the whole separate list, as
// the tab saves it after each edit.
func (s *Server) apiSaveGameTeams(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req gameTeamsList
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.writeGameTeams(w, r, sc, "game-teams:list", func(ctx context.Context, tx *sql.Tx) (roster.GameStateBroadcast, error) {
		return roster.SaveOwnTeamsTx(ctx, tx, sc.FestID, sc.GameID, req.Teams)
	})
}

// apiImportGameTeamsXLSX is POST …/teams/xlsx (field "file"): a roster
// sheet becomes the separate list. A team keeps the sheet's number, or takes
// the next one free; a number the list already had keeps that team's answers.
func (s *Server) apiImportGameTeamsXLSX(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	sheet, err := readRosterUpload(r)
	if err != nil {
		return err
	}
	return s.writeGameTeams(w, r, sc, "game-teams:xlsx", func(ctx context.Context, tx *sql.Tx) (roster.GameStateBroadcast, error) {
		before, err := roster.GameSheetTeams(ctx, tx, sc.FestID, sc.GameID)
		if err != nil {
			return roster.GameStateBroadcast{}, err
		}
		return roster.SaveOwnTeamsTx(ctx, tx, sc.FestID, sc.GameID, sheetOwnTeams(sheet, before))
	})
}

// sheetOwnTeams is a roster sheet as a separate list's rows.
func sheetOwnTeams(sheet []imports.SheetTeam, before []roster.FestRosterImportTeam) []roster.OwnTeam {
	had := map[int64]bool{}
	for _, team := range before {
		had[team.Number] = true
	}
	teams := make([]roster.FestRosterImportTeam, 0, len(sheet))
	for _, row := range sheet {
		team := roster.FestRosterImportTeam{Number: row.Number, RatingID: row.RatingID, Name: row.Name, City: row.City, Players: row.Players}
		if row.Flags != nil {
			team.Flags = roster.ParseFlags(*row.Flags)
		}
		teams = append(teams, team)
	}
	out := roster.NumberedOwnTeams(roster.OwnTeams(teams))
	for i := range out {
		if had[out[i].Number] {
			out[i].Prev = out[i].Number
		}
	}
	return out
}

// apiGameTeamsSheet is GET …/teams/sample.xlsx: the teams the tab shows, as
// the roster sheet the xlsx import reads.
func (s *Server) apiGameTeamsSheet(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	teams, err := roster.GameSheetTeams(r.Context(), s.h.Engine().DB, sc.FestID, sc.GameID)
	if err != nil {
		return route.BadUser(err)
	}
	book, err := imports.RosterSheetXLSX(teams)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="teams.xlsx"`)
	_, err = w.Write(book)
	return err
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
