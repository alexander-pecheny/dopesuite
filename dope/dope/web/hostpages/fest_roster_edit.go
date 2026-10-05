package hostpages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"dope/dope/domain/core"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/imports"
	"dope/dope/domain/roster"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	"dope/dope/web/route"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
)

// maxRosterUploadBytes is how much of a roster sheet upload is kept in memory.
const maxRosterUploadBytes = 4 << 20

// The host's edits to the fest roster (ADR-0024): a team added, edited or
// taken off by hand, and the undo of a rating import. Each goes through the
// same writer an import uses, and then tells the open pages.

// editFestRoster runs one roster edit in a write transaction: the edit, the
// Troika games that follow a division, the fest revision, then the broadcasts.
func (s *Server) editFestRoster(reqCtx context.Context, festID int64, label string, edit func(ctx context.Context, tx *sql.Tx) (imports.RosterWrite, error)) error {
	var written imports.RosterWrite
	var revision int64
	err := s.h.Engine().WithWriteTx(reqCtx, festID, label, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if written, err = edit(ctx, tx); err != nil {
			return err
		}
		if _, err := gamebuild.SyncDivisionEntrantsTx(ctx, tx, festID, 0); err != nil {
			return err
		}
		revision, err = festwrite.BumpFestRevisionTx(ctx, tx, festID, label, "{}")
		return err
	})
	if err != nil {
		return err
	}
	for _, update := range written.Updates {
		s.h.Engine().BroadcastState(festID, core.GameStateScope(update.GameID), revision, update.StateJSON)
	}
	s.broadcastRosterOverride(festID, revision, written.EKGameIDs)
	s.broadcastTroikaGames(reqCtx, festID, revision)
	return nil
}

// CreateFestTeam adds a team the host made and returns its id.
func (s *Server) CreateFestTeam(ctx context.Context, festID int64, in imports.TeamInput) (int64, error) {
	var teamID int64
	err := s.editFestRoster(ctx, festID, "fest:team-create", func(ctx context.Context, tx *sql.Tx) (imports.RosterWrite, error) {
		id, written, err := imports.CreateHandTeamTx(ctx, tx, festID, in)
		teamID = id
		return written, err
	})
	return teamID, err
}

// SaveFestTeam sets a team's name, city and people as the host typed them.
func (s *Server) SaveFestTeam(ctx context.Context, festID, teamID int64, in imports.TeamInput) error {
	return s.editFestRoster(ctx, festID, "fest:team-edit", func(ctx context.Context, tx *sql.Tx) (imports.RosterWrite, error) {
		return imports.SaveHandTeamTx(ctx, tx, festID, teamID, in)
	})
}

// RemoveFestTeam takes a team without results off the fest roster.
func (s *Server) RemoveFestTeam(ctx context.Context, festID, teamID int64) error {
	return s.editFestRoster(ctx, festID, "fest:team-remove", func(ctx context.Context, tx *sql.Tx) (imports.RosterWrite, error) {
		return imports.RemoveTeamTx(ctx, tx, festID, teamID)
	})
}

// UndoRosterImport puts the fest roster back as it was before the last import.
func (s *Server) UndoRosterImport(ctx context.Context, festID int64) error {
	return s.editFestRoster(ctx, festID, "rating:roster-undo", func(ctx context.Context, tx *sql.Tx) (imports.RosterWrite, error) {
		return imports.UndoRosterImportTx(ctx, tx, festID)
	})
}

// festTeamDetail is one team as the editor opens it, with every person the
// fest knows to suggest.
type festTeamDetail struct {
	ID       int64            `json:"id"`
	RatingID int64            `json:"rating_id"`
	Name     string           `json:"name"`
	City     string           `json:"city"`
	Hand     bool             `json:"hand"`
	Edited   bool             `json:"edited"`
	Players  []festTeamPlayer `json:"players"`
	Choices  []festTeamPlayer `json:"choices"`
}

// festTeamPlayer is a person as the editor sends and shows them. Team is the
// team they are on now, for the suggestions.
type festTeamPlayer struct {
	RatingID  int64  `json:"rating_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Team      string `json:"team,omitempty"`
}

func (s *Server) loadFestTeamDetail(ctx context.Context, festID, teamID int64) (festTeamDetail, error) {
	state, err := roster.LoadHandState(ctx, s.h.Engine().DB, festID)
	if err != nil {
		return festTeamDetail{}, err
	}
	edited := editedTeams(state)
	out := festTeamDetail{Players: []festTeamPlayer{}, Choices: []festTeamPlayer{}}
	found := false
	for _, team := range state.Teams {
		if team.Deleted {
			continue
		}
		for _, p := range team.Players {
			out.Choices = append(out.Choices, festTeamPlayer{RatingID: p.RatingID, FirstName: p.FirstName, LastName: p.LastName, Team: team.Name})
		}
		if team.ID != teamID {
			continue
		}
		found = true
		out.ID, out.RatingID, out.Name, out.City, out.Hand, out.Edited = team.ID, team.RatingID, team.Name, team.City, team.Hand, edited[team.ID]
		for _, p := range team.Players {
			out.Players = append(out.Players, festTeamPlayer{RatingID: p.RatingID, FirstName: p.FirstName, LastName: p.LastName})
		}
	}
	// Team 0 is a team about to be made: no people yet, the suggestions only.
	if !found && teamID != 0 {
		return festTeamDetail{}, route.NotFound
	}
	sortPlayersByName(out.Choices)
	return out, nil
}

func sortPlayersByName(players []festTeamPlayer) {
	sort.SliceStable(players, func(i, j int) bool {
		return util.CompareAlpha(store.JoinPlayerName(players[i].FirstName, players[i].LastName),
			store.JoinPlayerName(players[j].FirstName, players[j].LastName)) < 0
	})
}

// editedTeams is every team the host changed by hand: made, renamed, its
// Flags typed, or its people edited.
func editedTeams(state roster.HandState) map[int64]bool {
	out := map[int64]bool{}
	for _, team := range state.Teams {
		if team.Hand || team.HandName != nil || team.HandCity != nil || team.HandFlags {
			out[team.ID] = true
		}
	}
	for _, edit := range state.Edits {
		out[edit.TeamID] = true
	}
	return out
}

type teamRequest struct {
	// RatingID, for a new team, is the rating site's team it is.
	RatingID int64            `json:"rating_id"`
	Name     string           `json:"name"`
	City     string           `json:"city"`
	Players  []festTeamPlayer `json:"players"`
}

func (req teamRequest) input() imports.TeamInput {
	in := imports.TeamInput{RatingID: req.RatingID, Name: req.Name, City: req.City}
	for _, p := range req.Players {
		in.Players = append(in.Players, roster.FestRosterImportPlayer{RatingID: p.RatingID, FirstName: p.FirstName, LastName: p.LastName})
	}
	return in
}

func teamPathID(r *http.Request) (int64, error) { return troikaID(r) }

func (s *Server) apiTeam(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	id, err := teamPathID(r)
	if err != nil {
		return err
	}
	detail, err := s.loadFestTeamDetail(r.Context(), sc.FestID, id)
	if err != nil {
		return err
	}
	return route.JSON(w, detail)
}

// apiNewTeam is what the editor opens a new team with: the fest's people to
// suggest.
func (s *Server) apiNewTeam(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	detail, err := s.loadFestTeamDetail(r.Context(), sc.FestID, 0)
	if err != nil {
		return err
	}
	return route.JSON(w, detail)
}

func (s *Server) apiCreateTeam(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req teamRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	id, err := s.CreateFestTeam(r.Context(), sc.FestID, req.input())
	if err != nil {
		return err
	}
	detail, err := s.loadFestTeamDetail(r.Context(), sc.FestID, id)
	if err != nil {
		return err
	}
	return route.JSON(w, detail)
}

func (s *Server) apiSaveTeam(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	id, err := teamPathID(r)
	if err != nil {
		return err
	}
	var req teamRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.SaveFestTeam(r.Context(), sc.FestID, id, req.input()); err != nil {
		return err
	}
	detail, err := s.loadFestTeamDetail(r.Context(), sc.FestID, id)
	if err != nil {
		return err
	}
	return route.JSON(w, detail)
}

func (s *Server) apiRemoveTeam(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	id, err := teamPathID(r)
	if err != nil {
		return err
	}
	if err := s.RemoveFestTeam(r.Context(), sc.FestID, id); err != nil {
		return err
	}
	return writeOK(w)
}

func (s *Server) apiUndoRatingImport(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := s.UndoRosterImport(r.Context(), sc.FestID); err != nil {
		return err
	}
	return s.apiTeams(w, r, sc)
}

// apiImportPlan is an ImportPlan as the API answers it.
type apiImportPlan struct {
	AddedTeams   []string         `json:"added_teams"`
	DroppedTeams []string         `json:"dropped_teams"`
	Renamed      []apiRename      `json:"renamed"`
	Players      []apiTeamPlayers `json:"players"`
	Kept         apiKept          `json:"kept"`
	Conflicts    []apiConflict    `json:"conflicts"`
}

type apiRename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type apiTeamPlayers struct {
	Team    string   `json:"team"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

type apiKept struct {
	HandTeams    int `json:"hand_teams"`
	Renamed      int `json:"renamed"`
	Flags        int `json:"flags"`
	RemovedTeams int `json:"removed_teams"`
	Added        int `json:"added_players"`
	Removed      int `json:"removed_players"`
}

type apiConflict struct {
	Key      string `json:"key"`
	Player   string `json:"player"`
	HandTeam string `json:"hand_team"`
	SiteTeam string `json:"site_team"`
}

func planJSON(p *imports.ImportPlan) *apiImportPlan {
	if p == nil {
		return nil
	}
	out := &apiImportPlan{AddedTeams: nonNil(p.AddedTeams), DroppedTeams: nonNil(p.DroppedTeams),
		Renamed: []apiRename{}, Players: []apiTeamPlayers{}, Conflicts: []apiConflict{},
		Kept: apiKept{p.Kept.HandTeams, p.Kept.Renamed, p.Kept.Flags, p.Kept.RemovedTeams, p.Kept.Added, p.Kept.Removed}}
	for _, r := range p.Renamed {
		out.Renamed = append(out.Renamed, apiRename{r.From, r.To})
	}
	for _, t := range p.Players {
		out.Players = append(out.Players, apiTeamPlayers{t.Team, nonNil(t.Added), nonNil(t.Removed)})
	}
	for _, c := range p.Conflicts {
		out.Conflicts = append(out.Conflicts, apiConflict{c.Key, store.JoinPlayerName(c.Player.FirstName, c.Player.LastName), c.HandTeam, c.SiteTeam})
	}
	return out
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// conflictField names one conflict's checkbox on the import page.
func conflictField(key string) string {
	return fmt.Sprintf("accept_%s", key)
}

// apiRatingPlayers suggests the rating site's people for what the host typed.
func (s *Server) apiRatingPlayers(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	return route.JSON(w, imports.SearchRatingPlayers(r.Context(), s.h.Engine().Buff, r.URL.Query().Get("q")))
}

// apiRatingTeams suggests the rating site's teams for what the host typed.
func (s *Server) apiRatingTeams(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	return route.JSON(w, imports.SearchRatingTeams(r.Context(), s.h.Engine().Buff, r.URL.Query().Get("q")))
}

// apiRatingBaseRoster is a rating team's base roster now.
func (s *Server) apiRatingBaseRoster(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	id, err := teamPathID(r)
	if err != nil {
		return err
	}
	team, people, err := imports.RatingBaseRoster(r.Context(), s.h.Engine().Buff, id)
	if err != nil {
		return route.BadUser(corei18n.User(dopestrings.Default.Imports.HandRoster.BaseRosterFailed()))
	}
	return route.JSON(w, map[string]any{"team": team, "players": people})
}

// errRosterPreview rolls back the transaction a preview ran the sheet in.
var errRosterPreview = errors.New("roster preview")

// apiTeamsXLSX is the fest roster as a workbook, a row per person.
func (s *Server) apiTeamsXLSX(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	book, err := imports.BuildRosterXLSX(r.Context(), s.h.Engine().DB, sc.FestID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="roster.xlsx"`)
	_, err = w.Write(book)
	return err
}

// readRosterUpload parses the roster sheet uploaded in field "file".
func readRosterUpload(r *http.Request) ([]imports.SheetTeam, error) {
	if err := r.ParseMultipartForm(maxRosterUploadBytes); err != nil {
		return nil, route.BadRequest("bad form")
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, route.BadRequest("no file")
	}
	defer file.Close()
	teams, err := imports.ParseRosterXLSX(file)
	if err != nil {
		return nil, route.BadUser(err)
	}
	return teams, nil
}

// apiImportTeamsXLSX reads a roster sheet (field "file") and sets the teams it
// names. With ?preview=1 it runs in a transaction it rolls back, and answers
// only what it would change.
func (s *Server) apiImportTeamsXLSX(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	teams, err := readRosterUpload(r)
	if err != nil {
		return err
	}
	var plan imports.ImportPlan
	if r.URL.Query().Get("preview") == "1" {
		err = s.h.Engine().WithWriteTx(r.Context(), sc.FestID, "fest:roster-xlsx-preview", func(ctx context.Context, tx *sql.Tx) error {
			var err error
			if plan, _, err = imports.ApplyRosterSheetTx(ctx, tx, sc.FestID, teams); err != nil {
				return err
			}
			return errRosterPreview
		})
		if err != nil && !errors.Is(err, errRosterPreview) {
			return route.BadUser(err)
		}
		return route.JSON(w, map[string]any{"preview": true, "plan": planJSON(&plan)})
	}
	err = s.editFestRoster(r.Context(), sc.FestID, "fest:roster-xlsx", func(ctx context.Context, tx *sql.Tx) (imports.RosterWrite, error) {
		p, written, err := imports.ApplyRosterSheetTx(ctx, tx, sc.FestID, teams)
		plan = p
		return written, err
	})
	if err != nil {
		return route.BadUser(err)
	}
	return route.JSON(w, map[string]any{"preview": false, "plan": planJSON(&plan)})
}
