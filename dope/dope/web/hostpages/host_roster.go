package hostpages

import (
	"context"
	"database/sql"
	"dope/dope/domain/imports"
	"dope/dope/domain/overrides"
	"dope/dope/domain/view"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type hostFestTeam struct {
	RatingID int64
	Name     string
	City     string
	Players  int
}

type hostFestPlayer struct {
	RatingID int64
	Name     string
	Team     string
}

type hostFestRosterData struct {
	Fest            view.HostFest
	Teams           []hostFestTeam
	Players         []hostFestPlayer
	OverridePlayers []overrides.HostPlayerOverrideOption
	OverrideTeams   []overrides.HostTeamOverrideOption
	OverrideGames   []overrides.HostGameOverrideOption
	Overrides       []overrides.HostPlayerOverrideRow
	Error           string
	Notice          string
}

type hostFestImportData struct {
	Fest     view.HostFest
	RatingID int64
	Error    string
	Notice   string
}

// hostTeamsDoc builds the fest's teams table (or an empty note).
func hostTeamsDoc(data hostFestRosterData) *dopeui.Doc {
	s := dopestrings.Default
	page := []dopeui.Item{
		dopeui.Title(s.Host.Roster.TeamsTitle(data.Fest.Title)), dopeui.PagePublic,
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(data.Fest.Ref(), data.Fest.Title), s.Host.Roster.TeamsCrumb())),
	}
	if len(data.Teams) > 0 {
		rows := []dopeui.Item{dopeui.Trow(
			dopeui.Hcell(dopeui.Text("ID")), dopeui.Hcell(dopeui.Text(s.Host.Roster.TeamLabel())),
			dopeui.Hcell(dopeui.Text(s.Host.Roster.ColCity())), dopeui.Hcell(dopeui.Text(s.Host.Roster.ColPlayers())),
		)}
		for _, t := range data.Teams {
			rows = append(rows, dopeui.Trow(
				dopeui.Cell(dopeui.Text(optionalID(t.RatingID))),
				dopeui.Cell(dopeui.Text(t.Name)),
				dopeui.Cell(dopeui.Text(t.City)),
				dopeui.Cell(dopeui.Text(strconv.Itoa(t.Players))),
			))
		}
		page = append(page, dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...))
	} else {
		page = append(page, dopeui.Empty(dopeui.Text(s.Host.Roster.TeamsEmpty())))
	}
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// optionalID renders a rating id, or "" when it is 0 (matching {{if .RatingID}}).
func optionalID(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// hostPlayersDoc builds the fest players page: the add-override dialog (datalist
// autocomplete + game picker), the overrides table with per-row edit dialogs, and
// the players table. Dialog open/close, the delete confirm, and the datalist →
// hidden-id validation run through pageforms.js / roster.js data-attributes.
func hostPlayersDoc(data hostFestRosterData) *dopeui.Doc {
	s := dopestrings.Default
	ref := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(s.Host.Roster.PlayersTitle(data.Fest.Title)), dopeui.PagePublic, dopeui.Classicscripts("dist/pageforms.js dist/roster.js"),
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(ref, data.Fest.Title), s.Host.Roster.PlayersCrumb())),
	}
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}
	if data.Notice != "" {
		page = append(page, dopeui.Note(dopeui.Text(data.Notice)))
	}
	page = append(page,
		dopeui.Row(dopeui.Button(dopeui.Data("dialog-open", "playerOverrideDialog"), dopeui.Text(s.Host.Roster.AddOverrideBtn()))),
		hostAddOverrideDialog(data, ref),
	)
	if len(data.Overrides) > 0 {
		page = append(page, hostOverridesSection(data, ref))
	}
	if len(data.Players) > 0 {
		rows := []dopeui.Item{dopeui.Trow(dopeui.Hcell(dopeui.Text("ID")), dopeui.Hcell(dopeui.Text(s.Host.Roster.PlayerLabel())), dopeui.Hcell(dopeui.Text(s.Host.Roster.TeamLabel())))}
		for _, p := range data.Players {
			rows = append(rows, dopeui.Trow(dopeui.Cell(dopeui.Text(optionalID(p.RatingID))), dopeui.Cell(dopeui.Text(p.Name)), dopeui.Cell(dopeui.Text(p.Team))))
		}
		page = append(page, dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...))
	} else {
		page = append(page, dopeui.Empty(dopeui.Text(s.Host.Roster.PlayersEmpty())))
	}
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

func hostAddOverrideDialog(data hostFestRosterData, ref string) *dopeui.Element {
	s := dopestrings.Default
	playerOpts := make([]dopeui.Item, 0, len(data.OverridePlayers))
	for _, o := range data.OverridePlayers {
		playerOpts = append(playerOpts, dopeui.Option(dopeui.Value(o.Label), dopeui.Data("id", strconv.FormatInt(o.ID, 10))))
	}
	teamOpts := make([]dopeui.Item, 0, len(data.OverrideTeams))
	for _, o := range data.OverrideTeams {
		teamOpts = append(teamOpts, dopeui.Option(dopeui.Value(o.Label), dopeui.Data("id", strconv.FormatInt(o.ID, 10))))
	}
	var gamePicker dopeui.Item
	if len(data.OverrideGames) > 0 {
		boxes := make([]dopeui.Item, 0, len(data.OverrideGames))
		for _, g := range data.OverrideGames {
			boxes = append(boxes, dopeui.Checkbox(dopeui.Name("game_id"), dopeui.Value(strconv.FormatInt(g.ID, 10)), dopeui.Text(g.Label)))
		}
		gamePicker = dopeui.Col(append([]dopeui.Item{dopeui.SpaceSM}, boxes...)...)
	} else {
		gamePicker = dopeui.Empty(dopeui.Text(s.Host.Roster.NoOverrideGames()))
	}
	return dopeui.Dialog(dopeui.ID("playerOverrideDialog"),
		dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/players/overrides"), dopeui.Autocomplete("off"), dopeui.Data("player-override-form", ""),
			dopeui.Subhead(dopeui.Text(s.Host.Roster.OverrideTitle())),
			dopeui.Hiddenfield(dopeui.Name("player_id"), dopeui.Data("player-override-player-id", "")),
			dopeui.Hiddenfield(dopeui.Name("team_id"), dopeui.Data("player-override-team-id", "")),
			dopeui.Field(dopeui.Label(s.Host.Roster.PlayerLabel()),
				dopeui.Textfield(dopeui.Name("player_label"), dopeui.InputList("playerOverridePlayers"), dopeui.Required(), dopeui.Data("player-override-player", ""))),
			dopeui.Datalist(append([]dopeui.Item{dopeui.ID("playerOverridePlayers")}, playerOpts...)...),
			dopeui.Field(dopeui.Label(s.Host.Roster.NewTeamLabel()),
				dopeui.Textfield(dopeui.Name("team_label"), dopeui.InputList("playerOverrideTeams"), dopeui.Required(), dopeui.Data("player-override-team", ""))),
			dopeui.Datalist(append([]dopeui.Item{dopeui.ID("playerOverrideTeams")}, teamOpts...)...),
			dopeui.Pickgroup(dopeui.Label(s.Host.Roster.GamesLabel()), gamePicker),
			dopeui.Row(
				dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.SaveSubmit())),
				dopeui.Button(dopeui.Data("dialog-close", ""), dopeui.Text(s.Host.Roster.CancelBtn())),
			),
		),
	)
}

func hostOverridesSection(data hostFestRosterData, ref string) *dopeui.Element {
	s := dopestrings.Default
	rows := []dopeui.Item{dopeui.Trow(
		dopeui.Hcell(dopeui.Text(s.Host.Roster.PlayerLabel())), dopeui.Hcell(dopeui.Text(s.Host.Roster.ColFromTeam())),
		dopeui.Hcell(dopeui.Text(s.Host.Roster.ColToTeam())), dopeui.Hcell(dopeui.Text(s.Host.Roster.GamesLabel())), dopeui.Hcell(),
	)}
	for _, o := range data.Overrides {
		rows = append(rows, dopeui.Trow(
			dopeui.Cell(dopeui.Text(o.Player)), dopeui.Cell(dopeui.Text(o.SourceTeam)),
			dopeui.Cell(dopeui.Text(o.OverrideTeam)), dopeui.Cell(dopeui.Text(o.Games)),
			dopeui.Cell(dopeui.Iconbtn(dopeui.IconPencil, dopeui.Label(s.Host.Roster.EditOverrideLabel()), dopeui.Data("dialog-open", o.DialogID()))),
		))
	}
	sect := []dopeui.Item{
		dopeui.ID("overrides"),
		dopeui.Subhead(dopeui.Text(s.Host.Roster.OverridesSubhead())),
		dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...),
	}
	for _, o := range data.Overrides {
		sect = append(sect, hostOverrideEditDialog(data, ref, o))
	}
	return dopeui.Section(sect...)
}

func hostOverrideEditDialog(data hostFestRosterData, ref string, o overrides.HostPlayerOverrideRow) *dopeui.Element {
	s := dopestrings.Default
	boxes := make([]dopeui.Item, 0, len(data.OverrideGames))
	for _, g := range data.OverrideGames {
		items := []dopeui.Item{dopeui.Name("game_id"), dopeui.Value(strconv.FormatInt(g.ID, 10))}
		if o.HasGame(g.ID) {
			items = append(items, dopeui.Checked())
		}
		boxes = append(boxes, dopeui.Checkbox(append(items, dopeui.Text(g.Label))...))
	}
	summary := dopeui.Row(dopeui.SpaceMD, dopeui.Wrap(),
		dopeui.Col(dopeui.SpaceNone, dopeui.Muted(dopeui.Text(s.Host.Roster.PlayerLabel())), dopeui.Strong(dopeui.Text(o.Player))),
		dopeui.Col(dopeui.SpaceNone, dopeui.Muted(dopeui.Text(s.Host.Roster.ColFromTeam())), dopeui.Strong(dopeui.Text(o.SourceTeam))),
		dopeui.Col(dopeui.SpaceNone, dopeui.Muted(dopeui.Text(s.Host.Roster.ColToTeam())), dopeui.Strong(dopeui.Text(o.OverrideTeam))),
	)
	return dopeui.Dialog(dopeui.ID(o.DialogID()),
		dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/players/overrides"), dopeui.Autocomplete("off"),
			dopeui.Subhead(dopeui.Text(s.Host.Roster.OverrideTitle())),
			dopeui.Hiddenfield(dopeui.Name("mode"), dopeui.Value("edit")),
			dopeui.Hiddenfield(dopeui.Name("player_id"), dopeui.Value(strconv.FormatInt(o.PlayerID, 10))),
			dopeui.Hiddenfield(dopeui.Name("source_team_id"), dopeui.Value(strconv.FormatInt(o.SourceTeamID, 10))),
			dopeui.Hiddenfield(dopeui.Name("team_id"), dopeui.Value(strconv.FormatInt(o.OverrideTeamID, 10))),
			summary,
			dopeui.Pickgroup(append([]dopeui.Item{dopeui.Label(s.Host.Roster.GamesLabel())}, dopeui.Col(append([]dopeui.Item{dopeui.SpaceSM}, boxes...)...))...),
			dopeui.Row(
				dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.SaveSubmit())),
				dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Name("delete"), dopeui.Value("1"), dopeui.Formnovalidate(),
					dopeui.Data("confirm", s.Host.Roster.DeleteOverrideConfirm()), dopeui.Text(s.Host.Roster.DeleteBtn())),
				dopeui.Button(dopeui.Data("dialog-close", ""), dopeui.Text(s.Host.Roster.CancelBtn())),
			),
		),
	)
}

// hostRatingImportDoc builds the rating.chgk.info roster-import page: when the
// fest has a rating id, a confirm-and-import form; otherwise a note to set one.
func hostRatingImportDoc(data hostFestImportData) *dopeui.Doc {
	s := dopestrings.Default
	festRef := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(s.Host.Roster.RatingImportTitle(data.Fest.Title)), dopeui.PagePublic,
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(festRef, data.Fest.Title), s.Host.Roster.RatingImportCrumb())),
	}
	page = append(page, importMessages(data.Error, data.Notice)...)

	var sect []dopeui.Item
	if data.RatingID != 0 {
		sect = []dopeui.Item{
			dopeui.Note(dopeui.Text(s.Host.Roster.RatingSource(strconv.FormatInt(data.RatingID, 10)))),
			dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+festRef+"/rating/import"), dopeui.Autocomplete("off"),
				dopeui.Note(dopeui.Text(s.Host.Roster.RatingImportNote())),
				dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.ImportSubmit()))),
			),
		}
	} else {
		sect = []dopeui.Item{dopeui.Empty(dopeui.Text(s.Host.Roster.NeedRatingNote()))}
	}
	page = append(page, dopeui.Section(sect...))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// hostSchemeImportDoc builds the JSON-scheme import page: a paste-and-import form.
func hostSchemeImportDoc(data hostFestImportData) *dopeui.Doc {
	festRef := data.Fest.Ref()
	s := dopestrings.Default
	page := []dopeui.Item{
		dopeui.Title(s.Host.Roster.SchemeImportTitle(data.Fest.Title)), dopeui.PagePublic,
		dopeui.Publictopbar(pages.Trail(pages.FestCrumbs(festRef, data.Fest.Title), s.Host.Roster.SchemeImportCrumb())),
	}
	page = append(page, importMessages(data.Error, data.Notice)...)
	page = append(page, dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+festRef+"/import"), dopeui.Autocomplete("off"),
		dopeui.Note(dopeui.Text(s.Host.Roster.SchemeImportNote())),
		dopeui.Field(dopeui.Label(s.Host.Roster.SchemeJsonLabel()),
			dopeui.Editor(dopeui.Name("scheme"), dopeui.Rows("14"), dopeui.Placeholder(`{"slug":"...","title":"...","gameType":"ek","stages":[...]}`)),
		),
		dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text(s.Host.Roster.SchemeImportSubmit()))),
	))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// importMessages renders the shared error (empty) + notice (muted) lines the
// import pages show above their forms.
func importMessages(errMsg, notice string) []dopeui.Item {
	var out []dopeui.Item
	if errMsg != "" {
		out = append(out, dopeui.Empty(dopeui.Text(errMsg)))
	}
	if notice != "" {
		out = append(out, dopeui.Note(dopeui.Text(notice)))
	}
	return out
}

func (s *Server) renderHostFestTeams(w http.ResponseWriter, r *http.Request, festID int64) {
	s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
		teams, err := s.loadHostFestTeams(r.Context(), festID)
		if err != nil {
			return nil, err
		}
		return hostTeamsDoc(hostFestRosterData{Fest: fest, Teams: teams}), nil
	})
}

func (s *Server) renderHostFestPlayers(w http.ResponseWriter, r *http.Request, festID int64) {
	s.renderHostFestPlayersWithMessage(w, r, festID, "", "")
}

func (s *Server) renderHostFestPlayersWithMessage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
		players, err := s.loadHostFestPlayers(r.Context(), festID)
		if err != nil {
			return nil, err
		}
		overridePlayers, overrideTeams, overrideGames, overrides, err := overrides.LoadHostPlayerOverrideOptions(r.Context(), s.h.Engine().DB, festID)
		if err != nil {
			return nil, err
		}
		return hostPlayersDoc(hostFestRosterData{
			Fest:            fest,
			Players:         players,
			OverridePlayers: overridePlayers,
			OverrideTeams:   overrideTeams,
			OverrideGames:   overrideGames,
			Overrides:       overrides,
			Error:           errMsg,
			Notice:          notice,
		}), nil
	})
}

func (s *Server) handleHostAddPlayerOverride(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("mode") == "edit" || r.Form.Get("delete") == "1" {
		s.handleHostEditPlayerOverride(w, r, festID)
		return
	}
	playerID, err := overrides.ParseHostOverrideID(r.Form.Get("player_id"), dopestrings.Default.Host.Roster.ErrorObjPlayer())
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	teamID, err := overrides.ParseHostOverrideID(r.Form.Get("team_id"), dopestrings.Default.Host.Roster.ErrorObjTeam())
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	gameIDs, err := overrides.ParseHostOverrideGameIDs(r.Form["game_id"])
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	revision, ekGameIDs, err := overrides.SavePlayerTeamOverride(s.h.Engine(), r.Context(), festID, playerID, teamID, gameIDs)
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	for _, gameID := range ekGameIDs {
		s.h.Engine().BroadcastState(festID, fmt.Sprintf("game-roster:%d", gameID), revision, []byte(`{}`))
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/players#overrides", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

func (s *Server) handleHostEditPlayerOverride(w http.ResponseWriter, r *http.Request, festID int64) {
	playerID, err := overrides.ParseHostOverrideID(r.Form.Get("player_id"), dopestrings.Default.Host.Roster.ErrorObjPlayer())
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	sourceTeamID, err := overrides.ParseHostOverrideID(r.Form.Get("source_team_id"), dopestrings.Default.Host.Roster.ErrorObjSourceTeam())
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	teamID, err := overrides.ParseHostOverrideID(r.Form.Get("team_id"), dopestrings.Default.Host.Roster.ErrorObjTeam())
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	var gameIDs []int64
	if r.Form.Get("delete") != "1" {
		gameIDs, err = overrides.ParseHostOverrideGameIDs(r.Form["game_id"])
		if err != nil {
			s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
			return
		}
	}
	revision, ekGameIDs, err := overrides.ReplacePlayerTeamOverride(s.h.Engine(), r.Context(), festID, playerID, sourceTeamID, teamID, gameIDs)
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	for _, gameID := range ekGameIDs {
		s.h.Engine().BroadcastState(festID, fmt.Sprintf("game-roster:%d", gameID), revision, []byte(`{}`))
	}
	http.Redirect(w, r, fmt.Sprintf("/host/fest/%s/players#overrides", s.festRefOrID(r.Context(), festID)), http.StatusSeeOther)
}

func (s *Server) renderHostRatingImportPage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
		ratingID, err := s.loadFestRatingID(r.Context(), festID)
		if err != nil {
			return nil, err
		}
		return hostRatingImportDoc(hostFestImportData{Fest: fest, RatingID: ratingID, Error: errMsg, Notice: notice}), nil
	})
}

func (s *Server) renderHostSchemeImportPage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	s.festPage(w, r, festID, func(fest view.HostFest) (*dopeui.Doc, error) {
		return hostSchemeImportDoc(hostFestImportData{Fest: fest, Error: errMsg, Notice: notice}), nil
	})
}

func (s *Server) loadHostFestTeams(ctx context.Context, festID int64) ([]hostFestTeam, error) {
	teams, err := store.CollectRows(ctx, s.h.Engine().DB, `
select coalesce(tt.rating_id, 0), tt.name, tt.city, count(ttp.player_id)
from fest_teams tt
left join fest_team_players ttp on ttp.team_id = tt.id
where tt.fest_id = ? and tt.deleted = 0
group by tt.id
order by tt.position, tt.id`, []any{festID}, func(rows *sql.Rows) (hostFestTeam, error) {
		var team hostFestTeam
		if err := rows.Scan(&team.RatingID, &team.Name, &team.City, &team.Players); err != nil {
			return team, err
		}
		return team, nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(teams, func(i, j int) bool {
		if cmp := util.CompareAlpha(teams[i].Name, teams[j].Name); cmp != 0 {
			return cmp < 0
		}
		if cmp := util.CompareAlpha(teams[i].City, teams[j].City); cmp != 0 {
			return cmp < 0
		}
		return teams[i].RatingID < teams[j].RatingID
	})
	return teams, nil
}

func (s *Server) loadHostFestPlayers(ctx context.Context, festID int64) ([]hostFestPlayer, error) {
	players, err := store.CollectRows(ctx, s.h.Engine().DB, `
select coalesce(p.rating_id, 0), p.first_name, p.last_name, tt.name
from fest_team_players ttp
join fest_players p on p.id = ttp.player_id
join fest_teams tt on tt.id = ttp.team_id
where tt.fest_id = ? and tt.deleted = 0
order by tt.position, tt.id, ttp.roster_order, p.id`, []any{festID}, func(rows *sql.Rows) (hostFestPlayer, error) {
		var firstName, lastName, teamName string
		var ratingID int64
		if err := rows.Scan(&ratingID, &firstName, &lastName, &teamName); err != nil {
			return hostFestPlayer{}, err
		}
		return hostFestPlayer{
			RatingID: ratingID,
			Name:     store.JoinPlayerName(firstName, lastName),
			Team:     teamName,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(players, func(i, j int) bool {
		if cmp := util.CompareAlpha(players[i].Team, players[j].Team); cmp != 0 {
			return cmp < 0
		}
		if cmp := util.CompareAlpha(players[i].Name, players[j].Name); cmp != 0 {
			return cmp < 0
		}
		return players[i].RatingID < players[j].RatingID
	})
	return players, nil
}

func (s *Server) handleHostImportScheme(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	raw := strings.TrimSpace(r.Form.Get("scheme"))
	if raw == "" {
		s.renderHostSchemeImportPage(w, r, festID, dopestrings.Default.Host.Roster.ErrorJsonEmpty(), "")
		return
	}
	var scheme store.FestScheme
	if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
		s.renderHostSchemeImportPage(w, r, festID, dopestrings.Default.Host.Roster.ErrorJsonParse(err.Error()), "")
		return
	}
	if err := s.h.ImportSchemeIntoFest(r.Context(), festID, scheme); err != nil {
		s.renderHostSchemeImportPage(w, r, festID, err.Error(), "")
		return
	}
	s.renderHostSchemeImportPage(w, r, festID, "", dopestrings.Default.Host.Roster.ImportDoneNotice())
}

func (s *Server) handleHostImportRatingRoster(w http.ResponseWriter, r *http.Request, festID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ratingID, err := s.loadFestRatingID(r.Context(), festID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ratingID <= 0 {
		s.renderHostRatingImportPage(w, r, festID, dopestrings.Default.Host.Roster.NeedRatingNote(), "")
		return
	}
	result, err := imports.FetchAndImportRatingRoster(s.h.Engine(), r.Context(), festID, ratingID)
	if err != nil {
		s.renderHostRatingImportPage(w, r, festID, err.Error(), "")
		return
	}
	var msg string
	if result.Unchanged {
		msg = dopestrings.Default.Host.Roster.ImportUnchangedNotice(strconv.Itoa(result.TeamCount), strconv.Itoa(result.PlayerCount))
	} else {
		msg = dopestrings.Default.Host.Roster.ImportDoneCounts(strconv.Itoa(result.TeamCount), strconv.Itoa(result.PlayerCount), strconv.Itoa(result.ODGameCount), strconv.Itoa(result.KSIGameCount))
	}
	s.renderHostRatingImportPage(w, r, festID, "", msg)
}
