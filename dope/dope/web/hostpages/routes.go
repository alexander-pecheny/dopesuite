package hostpages

import (
	"net/http"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/web/route"
)

// HandleHostRouter serves /host/…: every page as one row of the table below.
func (s *Server) HandleHostRouter(w http.ResponseWriter, r *http.Request) {
	if strings.TrimPrefix(r.URL.Path, "/host/") == "" {
		http.Redirect(w, r, "/host", http.StatusSeeOther)
		return
	}
	s.routes().Mux.ServeHTTP(w, r)
}

// routes is the /host/ table. The host pages' denial policy: no session or no
// role on the fest sends the visitor back to /host; the wrong role is a 403.
func (s *Server) routes() *route.Table {
	s.once.Do(func() { s.table = s.buildRoutes() })
	return s.table
}

func denyHost(w http.ResponseWriter, r *http.Request, d route.Denial) {
	switch d {
	case route.NoSession, route.NoRole:
		http.Redirect(w, r, "/host", http.StatusSeeOther)
	default:
		route.DenyAPI(w, r, d)
	}
}

func (s *Server) buildRoutes() *route.Table {
	t := route.New(s.h.Engine(), denyHost)
	const fest, game = "/host/fest/{fest}", "/host/fest/{fest}/game/{game}"
	page := func(f func(http.ResponseWriter, *http.Request, int64)) route.Handler {
		return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error { f(w, r, sc.FestID); return nil }
	}
	festHandler := func(f func(http.ResponseWriter, *http.Request, int64) error) route.Handler {
		return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error { return f(w, r, sc.FestID) }
	}
	gamePage := func(f func(http.ResponseWriter, *http.Request, int64, int64)) route.Handler {
		return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
			f(w, r, sc.FestID, sc.GameID)
			return nil
		}
	}
	t.Handle("POST /host/fest", route.Session, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		s.handleHostCreateFest(w, r, sc.User)
		return nil
	})
	t.Handle("POST /host/venue", route.Session, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		s.handleHostCreateVenue(w, r, sc.User.UserID)
		return nil
	})
	t.Handle("GET "+fest, route.Member, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		s.renderHostFestDashboard(w, r, sc.FestID, hostDashMessages{})
		return nil
	})
	t.Handle("POST "+fest, route.Manager, page(func(w http.ResponseWriter, r *http.Request, id int64) {
		if s.IsVenue(r.Context(), id) {
			s.handleHostUpdateVenue(w, r, id)
			return
		}
		s.handleHostUpdateFest(w, r, id)
	}))
	const slot = fest + "/slot/{slot}"
	t.Handle("POST "+fest+"/slot/new", route.Manager, page(s.handleHostCreateSlot))
	t.Handle("GET "+slot, route.Member, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		return s.renderSlotPage(w, r, sc.FestID, "", "")
	})
	t.Handle("POST "+slot, route.Manager, festHandler(s.handleSlotSave))
	t.Handle("POST "+slot+"/token", route.Manager, festHandler(s.handleSlotToken))
	t.Handle("POST "+slot+"/clone", route.Manager, festHandler(s.handleSlotClone))
	t.Handle("POST "+slot+"/application/{app}/status", route.Manager, festHandler(s.handleApplicationStatus))
	t.Handle("POST "+slot+"/application/{app}/edit", route.Manager, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		return s.handleApplicationEdit(w, r, sc.FestID, sc.User.UserID)
	})
	t.Handle("POST "+slot+"/application/{app}/revert", route.Manager, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		return s.handleApplicationRevert(w, r, sc.FestID, sc.User.UserID)
	})
	t.Handle("POST "+slot+"/contested/accept", route.Editor, festHandler(s.handleContestedAccept))
	t.Handle("POST "+slot+"/contested/delete", route.Editor, festHandler(s.handleContestedDelete))
	t.Handle("POST "+slot+"/voting", route.Manager, festHandler(s.handleVotingSave))
	t.Handle("POST "+slot+"/voting/ballot/{ballot}", route.Manager, festHandler(s.handleVotingBallot))
	t.Handle("GET "+slot+"/export/tours.xlsx", route.Member, festHandler(s.handleSlotToursExport))
	t.Handle("GET "+slot+"/export/players.xlsx", route.Member, festHandler(s.handleSlotPlayersExport))
	t.Handle("GET "+fest+"/teams", route.Manager, page(s.renderHostFestTeams))
	t.Handle("GET "+fest+"/players", route.Manager, page(s.renderHostFestPlayers))
	t.Handle("POST "+fest+"/players/overrides", route.Manager, page(s.handleHostAddPlayerOverride))
	t.Handle("GET "+fest+"/import", route.Manager, page(func(w http.ResponseWriter, r *http.Request, id int64) { s.renderHostSchemeImportPage(w, r, id, "", "") }))
	t.Handle("POST "+fest+"/import", route.Manager, page(s.handleHostImportScheme))
	t.Handle("POST "+fest+"/access", route.Manager, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		s.handleHostSaveAccess(w, r, sc.FestID, sc.User.UserID)
		return nil
	})
	t.Handle("POST "+fest+"/delete", route.Creator, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		s.handleHostDeleteFest(w, r, sc.FestID, sc.User.UserID)
		return nil
	})
	t.Handle("GET "+fest+"/game/new", route.Manager, page(func(w http.ResponseWriter, r *http.Request, id int64) { s.renderHostCreateGamePage(w, r, id, "", "") }))
	t.Handle("POST "+fest+"/game/new", route.Manager, page(s.handleHostCreateGame))
	t.Handle("POST "+game+"/delete", route.Manager, gamePage(s.handleHostDeleteGame))
	t.Handle("POST "+game+"/clear", route.Manager, gamePage(s.handleHostClearGame))
	t.Handle("GET "+game+"/settings", route.Manager, gamePage(func(w http.ResponseWriter, r *http.Request, id, gid int64) {
		s.renderHostGameSettings(w, r, id, gid, "")
	}))
	t.Handle("POST "+game+"/settings", route.Manager, gamePage(s.handleHostUpdateGameSettings))
	t.Handle("GET "+fest+"/numbers", route.Manager, page(func(w http.ResponseWriter, r *http.Request, id int64) {
		s.pages().RenderHostFestNumbers(w, r, id, "", "", nil)
	}))
	t.Handle("POST "+fest+"/numbers", route.Manager, page(s.pages().HandleHostSaveFestNumbers))
	t.Handle("POST "+fest+"/numbers/auto", route.Manager, page(s.pages().HandleHostAutoFestNumbers))
	t.Handle("POST "+fest+"/numbers/clear", route.Manager, page(s.pages().HandleHostClearFestNumbers))
	t.Handle("POST "+fest+"/numbers/import/match", route.Manager, page(s.pages().HandleHostFestNumbersImportMatch))
	t.Handle("POST "+fest+"/numbers/import/apply", route.Manager, page(s.pages().HandleHostFestNumbersImportApply))
	t.Handle("GET "+fest+"/rating/import", route.Manager, page(func(w http.ResponseWriter, r *http.Request, id int64) { s.renderHostRatingImportPage(w, r, id, "", "") }))
	t.Handle("POST "+fest+"/rating/import", route.Manager, page(s.handleHostImportRatingRoster))
	t.Handle("GET "+fest+"/audit", route.Manager, page(func(w http.ResponseWriter, r *http.Request, id int64) {
		s.pages().RenderHostFestAudit(w, r, id, "", "")
	}))
	t.Handle("GET "+fest+"/audit/{game}", route.Manager, gamePage(func(w http.ResponseWriter, r *http.Request, id, gid int64) {
		s.pages().RenderGameJournal(w, r, id, gid, "", "")
	}))
	t.Handle("POST "+fest+"/audit/{game}/revert", route.Manager, gamePage(s.pages().HandleGameRevert))
	// /host/fest/{id}/game/{gid}[/...] → the game page (ek/od/si/brain) for hosts.
	t.Handle("GET "+fest+"/game/{rest...}", route.Member, s.serveHostGamePage)
	return t
}

func (s *Server) serveHostGamePage(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	parts := append([]string{"game"}, strings.Split(strings.TrimSuffix(r.PathValue("rest"), "/"), "/")...)
	if !route.GamePagePath(parts, true) {
		return route.NotFound
	}
	gameID, err := s.h.ResolveGameID(r.Context(), sc.FestID, parts[1])
	if err != nil || gameID <= 0 {
		return route.NotFound
	}
	var gameType string
	if err := s.h.Engine().DB.QueryRowContext(r.Context(), `select game_type from games where id = ? and fest_id = ?`, gameID, sc.FestID).Scan(&gameType); err != nil {
		return err
	}
	scope := core.FestScope{FestID: sc.FestID, GameID: gameID}
	if def := games.Get(gameType); def.Init == games.InitEK {
		s.h.ServeEKHTMLWithInit(w, r, scope, parts, def.Page)
	} else {
		s.h.ServeGameHTMLWithInit(w, r, def.Page, scope)
	}
	return nil
}
