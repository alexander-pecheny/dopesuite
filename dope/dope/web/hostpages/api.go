package hostpages

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"dope/dope/domain/festops"
	"dope/dope/domain/imports"
	"dope/dope/domain/numbering"
	"dope/dope/domain/overrides"
	"dope/dope/domain/roster"
	"dope/dope/platform/roles"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"
	"dope/dope/web/pages"
	"dope/dope/web/route"
	dopestrings "dope/i18nstrings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	"pecheny.me/dopecore/idstr"
)

// APIRoutes adds the host pages' JSON twins to the /api table: every form on
// /host/… has a route here that does the same thing through the same
// function, so an agent holding an API token (ADR-0021) can do whatever an
// organizer does by hand. The live game editing is already JSON (routes_api.go
// in the server) and needs no twin.
func (s *Server) APIRoutes(t *route.Table) {
	const fest, game = "/api/fest/{fest}", "/api/fest/{fest}/games/{game}"
	p := s.pages()

	t.Handle("GET /api/fests", route.Session, s.apiFests)
	t.Handle("POST /api/fests", route.Session, s.apiCreateFest)

	t.Handle("GET "+fest+"/settings", route.Member, s.apiFestSettings)
	t.Handle("PATCH "+fest+"/settings", route.Manager, s.apiUpdateFest)
	t.Handle("DELETE "+fest, route.Creator, s.apiDeleteFest)
	t.Handle("GET "+fest+"/access", route.Manager, s.apiAccess)
	t.Handle("POST "+fest+"/access", route.Manager, s.apiSaveAccess)

	t.Handle("GET "+fest+"/entrants", route.Manager, s.apiEntrants)
	t.Handle("POST "+fest+"/games", route.Manager, s.apiCreateGame)
	t.Handle("GET "+game+"/settings", route.Manager, s.apiGameSettings)
	t.Handle("PATCH "+game+"/settings", route.Manager, s.apiUpdateGame)
	t.Handle("DELETE "+game, route.Manager, s.apiDeleteGame)
	t.Handle("POST "+game+"/clear", route.Manager, s.apiClearGame)
	t.Handle("GET "+game+"/journal", route.Manager, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		changes, err := p.LoadGameJournal(r.Context(), sc.FestID, sc.GameID)
		if err != nil {
			return err
		}
		if changes == nil {
			changes = []pages.JournalChange{}
		}
		return route.JSON(w, changes)
	})
	t.Handle("POST "+game+"/revert", route.Manager, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		var req struct {
			Target *int64 `json:"target"`
		}
		if err := route.DecodeJSON(r, &req); err != nil {
			return err
		}
		if req.Target == nil {
			return route.BadRequest("missing target")
		}
		if err := p.RevertGame(r.Context(), sc.FestID, sc.GameID, *req.Target); err != nil {
			return err
		}
		return writeOK(w)
	})

	t.Handle("GET "+fest+"/teams", route.Manager, s.apiTeams)
	t.Handle("PATCH "+fest+"/teams/flags", route.Manager, s.apiTeamFlags)
	t.Handle("POST "+fest+"/teams", route.Manager, s.apiCreateTeam)
	t.Handle("GET "+fest+"/teams/new", route.Manager, s.apiNewTeam)
	t.Handle("GET "+fest+"/teams/export.xlsx", route.Manager, s.apiTeamsXLSX)
	t.Handle("POST "+fest+"/teams/xlsx", route.Manager, s.apiImportTeamsXLSX)
	t.Handle("GET "+fest+"/rating/players", route.Manager, s.apiRatingPlayers)
	t.Handle("GET "+fest+"/rating/teams", route.Manager, s.apiRatingTeams)
	t.Handle("GET "+fest+"/rating/teams/{id}/base", route.Manager, s.apiRatingBaseRoster)
	t.Handle("GET "+fest+"/teams/{id}", route.Manager, s.apiTeam)
	t.Handle("PUT "+fest+"/teams/{id}", route.Manager, s.apiSaveTeam)
	t.Handle("DELETE "+fest+"/teams/{id}", route.Manager, s.apiRemoveTeam)
	t.Handle("GET "+fest+"/players", route.Manager, s.apiPlayers)
	t.Handle("POST "+fest+"/players/overrides", route.Manager, s.apiAddOverride)
	t.Handle("PUT "+fest+"/players/overrides", route.Manager, s.apiReplaceOverride)
	t.Handle("DELETE "+fest+"/players/overrides", route.Manager, s.apiDeleteOverride)
	t.Handle("GET "+fest+"/troikas", route.Manager, s.apiTroikas)
	t.Handle("POST "+fest+"/troikas", route.Manager, s.apiAddTroikas)
	t.Handle("PUT "+fest+"/troikas/{id}", route.Manager, s.apiSaveTroika)
	t.Handle("DELETE "+fest+"/troikas/{id}", route.Manager, s.apiDeleteTroika)
	t.Handle("POST "+fest+"/rating-import", route.Manager, s.apiRatingImport)
	t.Handle("POST "+fest+"/rating-import/undo", route.Manager, s.apiUndoRatingImport)

	t.Handle("GET "+fest+"/numbers", route.Manager, s.apiNumbers)
	t.Handle("POST "+fest+"/numbers/assign", route.Manager, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		var req struct {
			Assignments []struct {
				TeamID int64 `json:"team_id"`
				Number int   `json:"number"`
			} `json:"assignments"`
		}
		if err := route.DecodeJSON(r, &req); err != nil {
			return err
		}
		assignments := make([]pages.NumberAssignment, len(req.Assignments))
		for i, a := range req.Assignments {
			assignments[i] = pages.NumberAssignment{TeamID: a.TeamID, Number: a.Number}
		}
		if err := p.AssignFestNumbers(r.Context(), sc.FestID, assignments); err != nil {
			return err
		}
		return s.apiNumbers(w, r, sc)
	})
	t.Handle("POST "+fest+"/numbers/auto", route.Manager, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		if err := p.AutoFestNumbers(r.Context(), sc.FestID); err != nil {
			return err
		}
		return s.apiNumbers(w, r, sc)
	})
	t.Handle("POST "+fest+"/numbers/clear", route.Manager, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		if err := p.ClearFestNumbers(r.Context(), sc.FestID); err != nil {
			return err
		}
		return s.apiNumbers(w, r, sc)
	})
	t.Handle("POST "+fest+"/numbers/match", route.Manager, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		var req struct {
			Text string `json:"text"`
		}
		if err := route.DecodeJSON(r, &req); err != nil {
			return err
		}
		match, err := p.MatchFestNumbers(r.Context(), sc.FestID, req.Text)
		if err != nil {
			return err
		}
		return route.JSON(w, match)
	})
}

// writeOK answers a write that has nothing else to return: {"ok": true}.
func writeOK(w http.ResponseWriter) error { return route.JSON(w, map[string]bool{"ok": true}) }

// ---- fests ----

type apiFest struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	IsPublic  bool   `json:"is_public"`
}

func (s *Server) apiFests(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	fests, err := s.loadHostFests(r.Context(), sc.User)
	if err != nil {
		return err
	}
	out := make([]apiFest, len(fests))
	for i, f := range fests {
		out[i] = apiFest{ID: f.ID, Slug: f.Slug, Title: f.Title, StartDate: f.StartDate, EndDate: f.EndDate, IsPublic: f.IsPublic}
	}
	return route.JSON(w, out)
}

func (s *Server) apiCreateFest(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var f FestSettings
	if err := route.DecodeJSON(r, &f); err != nil {
		return err
	}
	festID, err := s.CreateFest(r.Context(), sc.User.UserID, f)
	if err != nil {
		return err
	}
	sc.FestID, sc.Role = festID, roles.Creator
	return s.apiFestSettings(w, r, sc)
}

type apiGame struct {
	ID    int64  `json:"id"`
	Slug  string `json:"slug"`
	Code  string `json:"code"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type apiFestSettings struct {
	ID int64 `json:"id"`
	FestSettings
	// Role is the caller's own role on the fest.
	Role            string    `json:"role"`
	Games           []apiGame `json:"games"`
	Teams           int       `json:"teams"`
	Players         int       `json:"players"`
	Troikas         int       `json:"troikas"`
	NumbersAssigned int       `json:"numbers_assigned"`
}

// apiFestSettings is the dashboard as data: the fest's header, its games and
// how far its roster has got.
func (s *Server) apiFestSettings(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	ctx := r.Context()
	f, err := s.LoadFestSettings(ctx, sc.FestID)
	if err != nil {
		return err
	}
	rows, err := LoadFestGames(ctx, s.h.Engine().DB, sc.FestID)
	if err != nil {
		return err
	}
	out := apiFestSettings{ID: sc.FestID, FestSettings: f, Role: sc.Role, Games: make([]apiGame, len(rows))}
	for i, g := range rows {
		out.Games[i] = apiGame{ID: g.ID, Slug: g.Slug, Code: g.Code, Title: g.Title, Type: g.Type}
	}
	if out.Teams, out.Players, out.Troikas, err = s.loadHostFestRosterCounts(ctx, sc.FestID); err != nil {
		return err
	}
	if err := s.h.Engine().DB.QueryRowContext(ctx, `
select count(*) from fest_teams where fest_id = ? and deleted = 0 and number is not null`, sc.FestID).Scan(&out.NumbersAssigned); err != nil {
		return err
	}
	return route.JSON(w, out)
}

func (s *Server) apiUpdateFest(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	f, err := s.LoadFestSettings(r.Context(), sc.FestID)
	if err != nil {
		return err
	}
	// Decoding over the current values is the PATCH: a field the body leaves
	// out keeps what it had.
	if err := route.DecodeJSON(r, &f); err != nil {
		return err
	}
	if err := s.UpdateFest(r.Context(), sc.FestID, f); err != nil {
		return err
	}
	return s.apiFestSettings(w, r, sc)
}

func (s *Server) apiDeleteFest(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := s.DeleteFest(r.Context(), sc.FestID, sc.User.UserID); err != nil {
		return err
	}
	return writeOK(w)
}

type apiMember struct {
	UserID   int64  `json:"user_id"`
	Nickname string `json:"nickname"`
	Role     string `json:"role"`
	// Games are the ids of the Games a host is limited to; absent, the host
	// runs every Game.
	Games []int64 `json:"games,omitempty"`
}

func (s *Server) apiAccess(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	members, err := festaccess.LoadFestAccessMembers(s.h.Engine(), r.Context(), sc.FestID)
	if err != nil {
		return err
	}
	limits, err := festaccess.HostGamesByUser(r.Context(), s.h.Engine().DB, sc.FestID)
	if err != nil {
		return err
	}
	out := make([]apiMember, len(members))
	for i, m := range members {
		out[i] = apiMember{UserID: m.UserID, Nickname: m.Nickname, Role: m.Role}
		if m.Role == "host" {
			out[i].Games = limits[m.UserID]
		}
	}
	return route.JSON(w, out)
}

// apiSaveAccess grants, changes and removes roles. It takes the dashboard's
// bulk grammar as `lines` ("username:role", "username:remove"), or the same
// as a list of `changes`. A change that carries `games` (ids, codes or slugs)
// also limits that host to those Games, and `games: []` lifts the limit; a
// change may carry games alone, without a role.
func (s *Server) apiSaveAccess(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req struct {
		Lines   string `json:"lines"`
		Changes []struct {
			User   string    `json:"user"`
			Role   string    `json:"role"`
			Remove bool      `json:"remove"`
			Games  *[]string `json:"games"`
		} `json:"changes"`
	}
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	lines := req.Lines
	games := map[string][]string{}
	for _, c := range req.Changes {
		if c.Games != nil {
			games[c.User] = *c.Games
		}
		role := c.Role
		if c.Remove {
			role = "remove"
		}
		if role != "" {
			lines += "\n" + c.User + ":" + role
		}
	}
	err := s.saveAccess(r.Context(), sc.FestID, func(ctx context.Context, tx *sql.Tx) error {
		if strings.TrimSpace(lines) != "" || len(games) == 0 {
			if _, err := festaccess.SaveFestAccessBulkTx(ctx, tx, sc.FestID, sc.User.UserID, lines); err != nil {
				return err
			}
		}
		if len(games) > 0 {
			return festaccess.SetHostGamesTx(ctx, tx, sc.FestID, sc.User.UserID, games)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.apiAccess(w, r, sc)
}

// ---- games ----

type apiEntrant struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	// Troika marks a team assembled from fest players.
	Troika bool `json:"troika"`
	// Player marks a person. A rating player no individual Game has seated
	// yet has id 0 and only a ref, which `entrant_refs` takes.
	Player bool   `json:"player,omitempty"`
	Ref    string `json:"ref"`
}

// apiEntrants lists whom a new game may seat: the ids its `entrants` takes,
// and the refs its `entrant_refs` takes.
func (s *Server) apiEntrants(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	options, err := festEntrantOptions(r.Context(), s.h.Engine().DB, sc.FestID)
	if err != nil {
		return err
	}
	out := make([]apiEntrant, len(options))
	for i, o := range options {
		out[i] = apiEntrant{ID: o.ID, Label: o.Label, Troika: o.assembled, Player: o.player, Ref: o.value()}
	}
	return route.JSON(w, out)
}

func (s *Server) apiCreateGame(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req GameCreateRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	gameID, err := s.CreateGame(r.Context(), sc.FestID, req)
	if err != nil {
		return err
	}
	sc.GameID = gameID
	return s.apiGameSettings(w, r, sc)
}

type apiGameSettings struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Slug      string `json:"slug"`
	SchemeDSL string `json:"scheme_dsl"`
	// Divisions is every division the game's teams offer (read only), and
	// HiddenDivisions the ones the game does not show.
	Divisions       []string `json:"divisions"`
	HiddenDivisions []string `json:"hidden_divisions"`
}

func (g apiGameSettings) settings() GameSettings {
	hidden := append([]string{}, g.HiddenDivisions...)
	return GameSettings{Title: g.Title, Slug: g.Slug, SchemeDSL: g.SchemeDSL, HiddenDivisions: &hidden}
}

func (s *Server) loadGameSettings(ctx context.Context, festID, gameID int64) (apiGameSettings, error) {
	var g apiGameSettings
	var slug sql.NullString
	var hidden string
	err := s.h.Engine().DB.QueryRowContext(ctx, `
select id, code, title, game_type, slug, coalesce(scheme_dsl, ''), coalesce(hidden_divisions, '') from games where id = ? and fest_id = ?`, gameID, festID).
		Scan(&g.ID, &g.Code, &g.Title, &g.Type, &slug, &g.SchemeDSL, &hidden)
	if err != nil {
		return g, err
	}
	g.Slug = slug.String
	g.HiddenDivisions = store.ParseHiddenDivisions(hidden)
	if g.HiddenDivisions == nil {
		g.HiddenDivisions = []string{}
	}
	if g.Divisions, err = festops.OfferedDivisions(ctx, s.h.Engine().DB, festID, g.Type); err != nil {
		return g, err
	}
	if g.Divisions == nil {
		g.Divisions = []string{}
	}
	return g, nil
}

func (s *Server) apiGameSettings(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	g, err := s.loadGameSettings(r.Context(), sc.FestID, sc.GameID)
	if err != nil {
		return err
	}
	return route.JSON(w, g)
}

func (s *Server) apiUpdateGame(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	current, err := s.loadGameSettings(r.Context(), sc.FestID, sc.GameID)
	if err != nil {
		return err
	}
	settings := current.settings()
	if err := route.DecodeJSON(r, &settings); err != nil {
		return err
	}
	if err := s.UpdateGameSettings(r.Context(), sc.FestID, sc.GameID, settings); err != nil {
		return err
	}
	return s.apiGameSettings(w, r, sc)
}

func (s *Server) apiDeleteGame(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := s.DeleteGame(r.Context(), sc.FestID, sc.GameID); err != nil {
		return err
	}
	return writeOK(w)
}

func (s *Server) apiClearGame(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := s.ClearGame(r.Context(), sc.FestID, sc.GameID); err != nil {
		return err
	}
	return writeOK(w)
}

// ---- roster ----

type apiTeam struct {
	ID       int64  `json:"id"`
	RatingID int64  `json:"rating_id"`
	Name     string `json:"name"`
	City     string `json:"city"`
	Players  int    `json:"players"`
	Flags    string `json:"flags"`
	// Hand marks a team the host made; Edited, one the host changed by hand in
	// any way (ADR-0024), which an import keeps.
	Hand   bool `json:"hand"`
	Edited bool `json:"edited"`
}

func (s *Server) apiTeams(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	teams, err := s.loadHostFestTeams(r.Context(), sc.FestID)
	if err != nil {
		return err
	}
	out := make([]apiTeam, len(teams))
	for i, t := range teams {
		out[i] = apiTeam{ID: t.ID, RatingID: t.RatingID, Name: t.Name, City: t.City, Players: t.Players, Flags: t.Flags, Hand: t.Hand, Edited: t.Edited}
	}
	return route.JSON(w, out)
}

// apiTeamFlags sets the Flags (Divisions) of the teams it names: `flags` maps
// a team id to its flags typed as the teams page takes them: short names, comma separated.
func (s *Server) apiTeamFlags(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req struct {
		Flags map[string]string `json:"flags"`
	}
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	typed := make(map[int64]string, len(req.Flags))
	for key, value := range req.Flags {
		id, err := idstr.Parse(key)
		if err != nil || id <= 0 {
			return route.BadRequest("bad team id " + key)
		}
		typed[id] = value
	}
	if err := s.SaveTeamFlags(r.Context(), sc.FestID, typed); err != nil {
		return err
	}
	return s.apiTeams(w, r, sc)
}

type apiOption struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}

type apiOverride struct {
	PlayerID       int64   `json:"player_id"`
	SourceTeamID   int64   `json:"source_team_id"`
	TeamID         int64   `json:"team_id"`
	Player         string  `json:"player"`
	SourceTeam     string  `json:"source_team"`
	Team           string  `json:"team"`
	GameIDs        []int64 `json:"game_ids"`
	GamesDescribed string  `json:"games"`
}

func (s *Server) apiPlayers(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	ctx := r.Context()
	players, err := s.loadHostFestPlayers(ctx, sc.FestID)
	if err != nil {
		return err
	}
	pl, teams, gameOpts, rows, err := overrides.LoadHostPlayerOverrideOptions(ctx, s.h.Engine().DB, sc.FestID)
	if err != nil {
		return err
	}
	type player struct {
		RatingID int64  `json:"rating_id"`
		Name     string `json:"name"`
		Team     string `json:"team"`
	}
	out := struct {
		Players   []player      `json:"players"`
		Overrides []apiOverride `json:"overrides"`
		// The ids an override takes: players and teams by fest id, games.
		PlayerIDs []apiOption `json:"player_ids"`
		TeamIDs   []apiOption `json:"team_ids"`
		GameIDs   []apiOption `json:"game_ids"`
	}{Players: make([]player, len(players)), Overrides: make([]apiOverride, len(rows))}
	for i, p := range players {
		out.Players[i] = player{RatingID: p.RatingID, Name: p.Name, Team: p.Team}
	}
	for i, o := range rows {
		ids := o.GameIDs
		if ids == nil {
			ids = []int64{}
		}
		out.Overrides[i] = apiOverride{PlayerID: o.PlayerID, SourceTeamID: o.SourceTeamID, TeamID: o.OverrideTeamID,
			Player: o.Player, SourceTeam: o.SourceTeam, Team: o.OverrideTeam, GameIDs: ids, GamesDescribed: o.Games}
	}
	for _, o := range pl {
		out.PlayerIDs = append(out.PlayerIDs, apiOption{o.ID, o.Label})
	}
	for _, o := range teams {
		out.TeamIDs = append(out.TeamIDs, apiOption{o.ID, o.Label})
	}
	for _, o := range gameOpts {
		out.GameIDs = append(out.GameIDs, apiOption{o.ID, o.Label})
	}
	return route.JSON(w, out)
}

type overrideRequest struct {
	PlayerID     int64   `json:"player_id"`
	SourceTeamID int64   `json:"source_team_id"`
	TeamID       int64   `json:"team_id"`
	GameIDs      []int64 `json:"game_ids"`
}

// apiAddOverride moves a player to another team for the listed games; like
// the form, it needs at least one.
func (s *Server) apiAddOverride(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req overrideRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.AddPlayerOverride(r.Context(), sc.FestID, req.PlayerID, req.TeamID, req.GameIDs); err != nil {
		return err
	}
	return s.apiPlayers(w, r, sc)
}

func (s *Server) apiReplaceOverride(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req overrideRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	// An empty list would delete the override, which the edit form only does
	// on its delete button; here that is DELETE.
	if len(req.GameIDs) == 0 {
		return corei18n.User(dopestrings.Default.Override.Entry.PickGames())
	}
	if err := s.ReplacePlayerOverride(r.Context(), sc.FestID, req.PlayerID, req.SourceTeamID, req.TeamID, req.GameIDs); err != nil {
		return err
	}
	return s.apiPlayers(w, r, sc)
}

// apiDeleteOverride names the override in the query: player_id,
// source_team_id and team_id, as GET …/players lists them.
func (s *Server) apiDeleteOverride(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	q := r.URL.Query()
	id := func(key string) int64 { v, _ := idstr.Parse(q.Get(key)); return v }
	if err := s.ReplacePlayerOverride(r.Context(), sc.FestID, id("player_id"), id("source_team_id"), id("team_id"), nil); err != nil {
		return err
	}
	return s.apiPlayers(w, r, sc)
}

// apiTroika is one troika: team is its head team's name, headTeamID 0 for
// none; division is the host's choice (null while it follows the team) and
// flags the division it plays in.
type apiTroika struct {
	ID int64 `json:"id"`
	// Applied is the troika's place in the order applications came in.
	Applied    int      `json:"applied"`
	Name       string   `json:"name"`
	Players    []string `json:"players"`
	Team       string   `json:"team"`
	HeadTeamID int64    `json:"headTeamID"`
	Division   *string  `json:"division"`
	Flags      []string `json:"flags"`
	Seated     bool     `json:"seated"`
}

func (s *Server) apiTroikas(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	troikas, err := roster.LoadAssembled(r.Context(), s.h.Engine().DB, sc.FestID)
	if err != nil {
		return err
	}
	out := make([]apiTroika, len(troikas))
	for i, t := range troikas {
		players := t.Players
		if players == nil {
			players = []string{}
		}
		flags := t.Flags
		if flags == nil {
			flags = []string{}
		}
		out[i] = apiTroika{ID: t.ID, Applied: t.Applied, Name: t.Name, Players: players, Team: t.HeadTeam, HeadTeamID: t.HeadTeamID,
			Division: t.Division, Flags: flags, Seated: t.Seated}
	}
	return route.JSON(w, out)
}

// apiAddTroikas takes the troikas page's pasted lines: one troika per line,
// its name and its players.
func (s *Server) apiAddTroikas(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req struct {
		Lines string `json:"lines"`
	}
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if _, _, err := s.AddTroikas(r.Context(), sc.FestID, req.Lines); err != nil {
		return err
	}
	return s.apiTroikas(w, r, sc)
}

func troikaID(r *http.Request) (int64, error) {
	id, err := idstr.Parse(r.PathValue("id"))
	if err != nil || id <= 0 {
		return 0, route.BadRequest("bad id")
	}
	return id, nil
}

func (s *Server) apiSaveTroika(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	id, err := troikaID(r)
	if err != nil {
		return err
	}
	// headTeamID, when given, sets the head team (0 for none) and with it the
	// division: division absent or null follows the team, "" is no division.
	var req struct {
		Name       string   `json:"name"`
		Players    []string `json:"players"`
		HeadTeamID *int64   `json:"headTeamID"`
		Division   *string  `json:"division"`
		// Applied, when given, moves the troika to that place in the order of
		// applications.
		Applied int `json:"applied"`
	}
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Applied < 0 {
		return route.BadRequest("bad applied")
	}
	in := roster.AssembledInput{Name: req.Name, Players: req.Players, Applied: req.Applied}
	if req.HeadTeamID != nil {
		if *req.HeadTeamID < 0 {
			return route.BadRequest("bad head team")
		}
		in.Placement = &roster.AssembledPlacement{HeadTeamID: *req.HeadTeamID, Division: req.Division}
	}
	if _, err := s.SaveTroika(r.Context(), sc.FestID, id, in); err != nil {
		return err
	}
	return s.apiTroikas(w, r, sc)
}

func (s *Server) apiDeleteTroika(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	id, err := troikaID(r)
	if err != nil {
		return err
	}
	if _, err := s.DeleteTroika(r.Context(), sc.FestID, id); err != nil {
		return err
	}
	return s.apiTroikas(w, r, sc)
}

// apiRatingImport pulls the roster from rating.chgk.info. When teams that
// have results would disappear it answers 409 with both sides of the
// conflict, and the caller repeats the call with `merge` (fest team id →
// the rating id it now has) and `drop` (fest team ids that may lose results).
func (s *Server) apiRatingImport(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req struct {
		Merge map[string]int64 `json:"merge"`
		Drop  []int64          `json:"drop"`
		// Preview answers what the import would do and writes nothing.
		Preview bool `json:"preview"`
		// AcceptSite names the player conflicts where the site's placement
		// wins; every other conflict keeps the host's (ADR-0024).
		AcceptSite []string `json:"accept_site"`
	}
	// No body at all is the plain import, which asks nothing.
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return route.BadUser(err)
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return route.BadRequest("bad json")
		}
	}
	choice := imports.RosterChoice{Merge: map[int64]int64{}, Drop: map[int64]bool{}, AcceptSite: map[string]bool{}, Preview: req.Preview}
	for _, key := range req.AcceptSite {
		choice.AcceptSite[key] = true
	}
	for key, ratingID := range req.Merge {
		teamID, err := idstr.Parse(key)
		if err != nil || teamID <= 0 {
			return route.BadRequest("bad team id " + key)
		}
		choice.Merge[teamID] = ratingID
	}
	for _, teamID := range req.Drop {
		choice.Drop[teamID] = true
	}
	result, err := s.ImportRatingRoster(r.Context(), sc.FestID, choice)
	var conflict *imports.RosterConflict
	if errors.As(err, &conflict) {
		type dropped struct {
			TeamID   int64    `json:"team_id"`
			RatingID int64    `json:"rating_id"`
			Number   int64    `json:"number"`
			Name     string   `json:"name"`
			City     string   `json:"city"`
			Games    []string `json:"games"`
		}
		type added struct {
			RatingID int64  `json:"rating_id"`
			Name     string `json:"name"`
			City     string `json:"city"`
		}
		body := struct {
			Error   string    `json:"error"`
			Dropped []dropped `json:"dropped"`
			Added   []added   `json:"added"`
		}{Error: conflict.Error(), Dropped: []dropped{}, Added: []added{}}
		for _, d := range conflict.Dropped {
			body.Dropped = append(body.Dropped, dropped{d.TeamID, d.RatingID, d.Number, d.Name, d.City, d.Games})
		}
		for _, a := range conflict.Added {
			body.Added = append(body.Added, added{a.RatingID, a.Name, a.City})
		}
		return &route.JSONStatus{Code: http.StatusConflict, Body: body}
	}
	if err != nil {
		return err
	}
	return route.JSON(w, map[string]any{
		"teams": result.TeamCount, "players": result.PlayerCount,
		"od_games": result.ODGameCount, "ksi_games": result.KSIGameCount,
		"unchanged": result.Unchanged, "preview": req.Preview, "plan": planJSON(result.Plan),
	})
}

// ---- numbers ----

type apiNumberedTeam struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	City   string `json:"city"`
	Number int    `json:"number"`
}

func (s *Server) apiNumbers(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	teams, err := numbering.LoadFestTeams(r.Context(), s.h.Engine().DB, sc.FestID)
	if err != nil {
		return err
	}
	out := make([]apiNumberedTeam, len(teams))
	for i, t := range teams {
		out[i] = apiNumberedTeam{ID: t.ID, Name: t.Name, City: t.City, Number: t.Number}
	}
	return route.JSON(w, out)
}
