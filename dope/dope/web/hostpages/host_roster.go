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
	page := []dopeui.Item{
		dopeui.Title(data.Fest.Title + " · команды"), dopeui.PagePublic,
		dopeui.Publictopbar(dopeui.Title("Команды"), dopeui.Back("/host/fest/"+data.Fest.Ref())),
	}
	if len(data.Teams) > 0 {
		rows := []dopeui.Item{dopeui.Trow(
			dopeui.Hcell(dopeui.Text("ID")), dopeui.Hcell(dopeui.Text("Команда")),
			dopeui.Hcell(dopeui.Text("Город")), dopeui.Hcell(dopeui.Text("Игроков")),
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
		page = append(page, dopeui.Empty(dopeui.Text("Команды пока не загружены.")))
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
	ref := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(data.Fest.Title + " · игроки"), dopeui.PagePublic, dopeui.Classicscripts("pageforms.js roster.js"),
		dopeui.Publictopbar(dopeui.Title("Игроки"), dopeui.Back("/host/fest/"+ref)),
	}
	if data.Error != "" {
		page = append(page, dopeui.Empty(dopeui.Text(data.Error)))
	}
	if data.Notice != "" {
		page = append(page, dopeui.Note(dopeui.Text(data.Notice)))
	}
	page = append(page,
		dopeui.Row(dopeui.Button(dopeui.Data("dialog-open", "playerOverrideDialog"), dopeui.Text("Добавить оверрайд для игры"))),
		hostAddOverrideDialog(data, ref),
	)
	if len(data.Overrides) > 0 {
		page = append(page, hostOverridesSection(data, ref))
	}
	if len(data.Players) > 0 {
		rows := []dopeui.Item{dopeui.Trow(dopeui.Hcell(dopeui.Text("ID")), dopeui.Hcell(dopeui.Text("Игрок")), dopeui.Hcell(dopeui.Text("Команда")))}
		for _, p := range data.Players {
			rows = append(rows, dopeui.Trow(dopeui.Cell(dopeui.Text(optionalID(p.RatingID))), dopeui.Cell(dopeui.Text(p.Name)), dopeui.Cell(dopeui.Text(p.Team))))
		}
		page = append(page, dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...))
	} else {
		page = append(page, dopeui.Empty(dopeui.Text("Игроки пока не загружены.")))
	}
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

func hostAddOverrideDialog(data hostFestRosterData, ref string) *dopeui.Element {
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
		gamePicker = dopeui.Empty(dopeui.Text("В фесте пока нет игр КСИ или ЭК."))
	}
	return dopeui.Dialog(dopeui.ID("playerOverrideDialog"),
		dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/players/overrides"), dopeui.Autocomplete("off"), dopeui.Data("player-override-form", ""),
			dopeui.Subhead(dopeui.Text("Оверрайд игрока")),
			dopeui.Hiddenfield(dopeui.Name("player_id"), dopeui.Data("player-override-player-id", "")),
			dopeui.Hiddenfield(dopeui.Name("team_id"), dopeui.Data("player-override-team-id", "")),
			dopeui.Field(dopeui.Label("Игрок"),
				dopeui.Textfield(dopeui.Name("player_label"), dopeui.InputList("playerOverridePlayers"), dopeui.Required(), dopeui.Data("player-override-player", ""))),
			dopeui.Datalist(append([]dopeui.Item{dopeui.ID("playerOverridePlayers")}, playerOpts...)...),
			dopeui.Field(dopeui.Label("Новая команда"),
				dopeui.Textfield(dopeui.Name("team_label"), dopeui.InputList("playerOverrideTeams"), dopeui.Required(), dopeui.Data("player-override-team", ""))),
			dopeui.Datalist(append([]dopeui.Item{dopeui.ID("playerOverrideTeams")}, teamOpts...)...),
			dopeui.Pickgroup(dopeui.Label("Игры"), gamePicker),
			dopeui.Row(
				dopeui.Button(dopeui.Submit(), dopeui.Text("Сохранить")),
				dopeui.Button(dopeui.Data("dialog-close", ""), dopeui.Text("Отмена")),
			),
		),
	)
}

func hostOverridesSection(data hostFestRosterData, ref string) *dopeui.Element {
	rows := []dopeui.Item{dopeui.Trow(
		dopeui.Hcell(dopeui.Text("Игрок")), dopeui.Hcell(dopeui.Text("Из команды")),
		dopeui.Hcell(dopeui.Text("В команду")), dopeui.Hcell(dopeui.Text("Игры")), dopeui.Hcell(),
	)}
	for _, o := range data.Overrides {
		rows = append(rows, dopeui.Trow(
			dopeui.Cell(dopeui.Text(o.Player)), dopeui.Cell(dopeui.Text(o.SourceTeam)),
			dopeui.Cell(dopeui.Text(o.OverrideTeam)), dopeui.Cell(dopeui.Text(o.Games)),
			dopeui.Cell(dopeui.Iconbtn(dopeui.Label("Редактировать оверрайд"), dopeui.Data("dialog-open", o.DialogID()), dopeui.Text("✏️"))),
		))
	}
	sect := []dopeui.Item{
		dopeui.ID("overrides"),
		dopeui.Subhead(dopeui.Text("Оверрайды")),
		dopeui.Table(append([]dopeui.Item{dopeui.Scroll()}, rows...)...),
	}
	for _, o := range data.Overrides {
		sect = append(sect, hostOverrideEditDialog(data, ref, o))
	}
	return dopeui.Section(sect...)
}

func hostOverrideEditDialog(data hostFestRosterData, ref string, o overrides.HostPlayerOverrideRow) *dopeui.Element {
	boxes := make([]dopeui.Item, 0, len(data.OverrideGames))
	for _, g := range data.OverrideGames {
		items := []dopeui.Item{dopeui.Name("game_id"), dopeui.Value(strconv.FormatInt(g.ID, 10))}
		if o.HasGame(g.ID) {
			items = append(items, dopeui.Checked())
		}
		boxes = append(boxes, dopeui.Checkbox(append(items, dopeui.Text(g.Label))...))
	}
	summary := dopeui.Row(dopeui.SpaceMD, dopeui.Wrap(),
		dopeui.Col(dopeui.SpaceNone, dopeui.Muted(dopeui.Text("Игрок")), dopeui.Strong(dopeui.Text(o.Player))),
		dopeui.Col(dopeui.SpaceNone, dopeui.Muted(dopeui.Text("Из команды")), dopeui.Strong(dopeui.Text(o.SourceTeam))),
		dopeui.Col(dopeui.SpaceNone, dopeui.Muted(dopeui.Text("В команду")), dopeui.Strong(dopeui.Text(o.OverrideTeam))),
	)
	return dopeui.Dialog(dopeui.ID(o.DialogID()),
		dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+ref+"/players/overrides"), dopeui.Autocomplete("off"),
			dopeui.Subhead(dopeui.Text("Оверрайд игрока")),
			dopeui.Hiddenfield(dopeui.Name("mode"), dopeui.Value("edit")),
			dopeui.Hiddenfield(dopeui.Name("player_id"), dopeui.Value(strconv.FormatInt(o.PlayerID, 10))),
			dopeui.Hiddenfield(dopeui.Name("source_team_id"), dopeui.Value(strconv.FormatInt(o.SourceTeamID, 10))),
			dopeui.Hiddenfield(dopeui.Name("team_id"), dopeui.Value(strconv.FormatInt(o.OverrideTeamID, 10))),
			summary,
			dopeui.Pickgroup(append([]dopeui.Item{dopeui.Label("Игры")}, dopeui.Col(append([]dopeui.Item{dopeui.SpaceSM}, boxes...)...))...),
			dopeui.Row(
				dopeui.Button(dopeui.Submit(), dopeui.Text("Сохранить")),
				dopeui.Button(dopeui.Danger, dopeui.Submit(), dopeui.Name("delete"), dopeui.Value("1"), dopeui.Formnovalidate(),
					dopeui.Data("confirm", "Удалить оверрайд?"), dopeui.Text("Удалить")),
				dopeui.Button(dopeui.Data("dialog-close", ""), dopeui.Text("Отмена")),
			),
		),
	)
}

// hostRatingImportDoc builds the rating.chgk.info roster-import page: when the
// fest has a rating id, a confirm-and-import form; otherwise a note to set one.
func hostRatingImportDoc(data hostFestImportData) *dopeui.Doc {
	festRef := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(data.Fest.Title + " · импорт участников"), dopeui.PagePublic,
		dopeui.Publictopbar(dopeui.Title("Импорт участников"), dopeui.Back("/host/fest/"+festRef)),
	}
	page = append(page, importMessages(data.Error, data.Notice)...)

	var sect []dopeui.Item
	if data.RatingID != 0 {
		sect = []dopeui.Item{
			dopeui.Note(dopeui.Text("Источник: rating.chgk.info ID " + strconv.FormatInt(data.RatingID, 10))),
			dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+festRef+"/rating/import"), dopeui.Autocomplete("off"),
				dopeui.Note(dopeui.Text("Импорт заменит списки команд и игроков феста и обновит список команд в играх ЧГК и КСИ.")),
				dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text("Загрузить команды и игроков"))),
			),
		}
	} else {
		sect = []dopeui.Item{dopeui.Empty(dopeui.Text("Сначала сохраните rating.chgk.info ID в свойствах феста."))}
	}
	page = append(page, dopeui.Section(sect...))
	return &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(page...)}}
}

// hostSchemeImportDoc builds the JSON-scheme import page: a paste-and-import form.
func hostSchemeImportDoc(data hostFestImportData) *dopeui.Doc {
	festRef := data.Fest.Ref()
	page := []dopeui.Item{
		dopeui.Title(data.Fest.Title + " · импорт схемы"), dopeui.PagePublic,
		dopeui.Publictopbar(dopeui.Title("Импорт схемы"), dopeui.Back("/host/fest/"+festRef)),
	}
	page = append(page, importMessages(data.Error, data.Notice)...)
	page = append(page, dopeui.Form(dopeui.DirCol, dopeui.Method("post"), dopeui.Action("/host/fest/"+festRef+"/import"), dopeui.Autocomplete("off"),
		dopeui.Note(dopeui.Text("Импорт пересоздаёт игру феста из JSON-схемы. Существующие игры этого феста будут заменены.")),
		dopeui.Field(dopeui.Label("JSON-схема"),
			dopeui.Editor(dopeui.Name("scheme"), dopeui.Rows("14"), dopeui.Placeholder(`{"slug":"...","title":"...","gameType":"ek","stages":[...]}`)),
		),
		dopeui.Row(dopeui.Button(dopeui.Submit(), dopeui.Text("Импортировать"))),
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
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	teams, err := s.loadHostFestTeams(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostTeamsDoc(hostFestRosterData{Fest: fest, Teams: teams}))
}

func (s *Server) renderHostFestPlayers(w http.ResponseWriter, r *http.Request, festID int64) {
	s.renderHostFestPlayersWithMessage(w, r, festID, "", "")
}

func (s *Server) renderHostFestPlayersWithMessage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	players, err := s.loadHostFestPlayers(r.Context(), festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	overridePlayers, overrideTeams, overrideGames, overrides, err := overrides.LoadHostPlayerOverrideOptions(r.Context(), s.h.Engine().DB, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostPlayersDoc(hostFestRosterData{
		Fest:            fest,
		Players:         players,
		OverridePlayers: overridePlayers,
		OverrideTeams:   overrideTeams,
		OverrideGames:   overrideGames,
		Overrides:       overrides,
		Error:           errMsg,
		Notice:          notice,
	}))
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
	playerID, err := overrides.ParseHostOverrideID(r.Form.Get("player_id"), "игрока")
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	teamID, err := overrides.ParseHostOverrideID(r.Form.Get("team_id"), "команду")
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	gameIDs, err := overrides.ParseHostOverrideGameIDs(r.Form["game_id"])
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	revision, ekGameIDs, err := overrides.SavePlayerTeamOverride(s.h, r.Context(), festID, playerID, teamID, gameIDs)
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
	playerID, err := overrides.ParseHostOverrideID(r.Form.Get("player_id"), "игрока")
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	sourceTeamID, err := overrides.ParseHostOverrideID(r.Form.Get("source_team_id"), "исходную команду")
	if err != nil {
		s.renderHostFestPlayersWithMessage(w, r, festID, err.Error(), "")
		return
	}
	teamID, err := overrides.ParseHostOverrideID(r.Form.Get("team_id"), "команду")
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
	revision, ekGameIDs, err := overrides.ReplacePlayerTeamOverride(s.h, r.Context(), festID, playerID, sourceTeamID, teamID, gameIDs)
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
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ratingID, err := s.loadFestRatingID(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostRatingImportDoc(hostFestImportData{
		Fest:     fest,
		RatingID: ratingID,
		Error:    errMsg,
		Notice:   notice,
	}))
}

func (s *Server) renderHostSchemeImportPage(w http.ResponseWriter, r *http.Request, festID int64, errMsg, notice string) {
	fest, err := s.h.LoadHostFestHeader(r.Context(), festID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pages.RenderDoc(w, s.h.Engine().AssetETags, hostSchemeImportDoc(hostFestImportData{
		Fest:   fest,
		Error:  errMsg,
		Notice: notice,
	}))
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
		s.renderHostSchemeImportPage(w, r, festID, "Вставьте JSON схемы.", "")
		return
	}
	var scheme store.FestScheme
	if err := json.Unmarshal([]byte(raw), &scheme); err != nil {
		s.renderHostSchemeImportPage(w, r, festID, "Не удалось разобрать JSON: "+err.Error(), "")
		return
	}
	if err := s.h.ImportSchemeIntoFest(r.Context(), festID, scheme); err != nil {
		s.renderHostSchemeImportPage(w, r, festID, err.Error(), "")
		return
	}
	s.renderHostSchemeImportPage(w, r, festID, "", "Импорт выполнен.")
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
		s.renderHostRatingImportPage(w, r, festID, "Сначала сохраните rating.chgk.info ID в свойствах феста.", "")
		return
	}
	result, err := imports.FetchAndImportRatingRoster(s.h.Engine(), r.Context(), festID, ratingID)
	if err != nil {
		s.renderHostRatingImportPage(w, r, festID, err.Error(), "")
		return
	}
	var msg string
	if result.Unchanged {
		msg = fmt.Sprintf("Списки уже совпадают с рейтингом — изменений нет. Команд: %d, игроков: %d.", result.TeamCount, result.PlayerCount)
	} else {
		msg = fmt.Sprintf("Загружено команд: %d, игроков: %d. Обновлено игр ЧГК: %d, КСИ: %d.", result.TeamCount, result.PlayerCount, result.ODGameCount, result.KSIGameCount)
	}
	s.renderHostRatingImportPage(w, r, festID, "", msg)
}
