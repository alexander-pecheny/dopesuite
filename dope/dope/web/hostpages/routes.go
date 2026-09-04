package hostpages

import (
	"net/http"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/domain/venues"
	"dope/dope/storage/store"
	"dope/dope/web/route"
)

// HandleHostRouter serves /host/…: every page as one row of the table below.
func (s *Server) HandleHostRouter(w http.ResponseWriter, r *http.Request) {
	if strings.TrimPrefix(r.URL.Path, "/host/") == "" {
		http.Redirect(w, r, "/host", http.StatusSeeOther)
		return
	}
	if target, found := s.hostTreeFor(r); found {
		switch {
		case target == "":
			http.NotFound(w, r)
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		default:
			// A 301 turns a write into a GET, which drops it on the floor
			// silently; a form must post to the tree it was rendered under.
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	s.routes().Mux.ServeHTTP(w, r)
}

// hostTreeFor keeps each kind of Fest in its own tree: a Venue under /host/fest
// is moved to /host/venue, an ordinary fest under /host/venue is not there at
// all. It answers the redirect target, "" for a 404, and found=false to let the
// table serve the request.
func (s *Server) hostTreeFor(r *http.Request) (string, bool) {
	from, to := "/host/fest/", "/host/venue/"
	wantVenue := true
	if strings.HasPrefix(r.URL.Path, to) {
		from, to, wantVenue = to, from, false
	} else if !strings.HasPrefix(r.URL.Path, from) {
		return "", false
	}
	rest := strings.TrimPrefix(r.URL.Path, from)
	ref, tail, _ := strings.Cut(rest, "/")
	if ref == "" {
		return "", false
	}
	festID, err := store.ResolveFestID(r.Context(), s.h.Engine().DB, ref)
	if err != nil || festID <= 0 {
		return "", false
	}
	if venues.IsVenue(r.Context(), s.h.Engine().DB, festID) != wantVenue {
		return "", false
	}
	if !wantVenue {
		return "", true
	}
	target := to + ref
	if tail != "" {
		target += "/" + tail
	}
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	return target, true
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
	t.Handle("POST /host/fest", route.Session, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		s.handleHostCreateFest(w, r, sc.User)
		return nil
	})
	t.Handle("POST /host/venue", route.Session, s.handleHostCreateVenue)
	// A Venue is a Fest of its own kind, so it answers to every per-fest page
	// under a tree of its own; the handlers are the same, only the patterns
	// differ, and the router keeps each kind on its own prefix.
	s.handleFestRoutes(t, "/host/fest/{fest}", false)
	s.handleFestRoutes(t, "/host/venue/{venue}", true)
	s.handleSlotRoutes(t, "/host/venue/{venue}")
	return t
}

func (s *Server) handleFestRoutes(t *route.Table, fest string, venue bool) {
	game := fest + "/game/{game}"
	page := func(f func(http.ResponseWriter, *http.Request, int64)) route.Handler {
		return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error { f(w, r, sc.FestID); return nil }
	}
	gamePage := func(f func(http.ResponseWriter, *http.Request, int64, int64)) route.Handler {
		return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
			f(w, r, sc.FestID, sc.GameID)
			return nil
		}
	}
	t.Handle("GET "+fest, route.Member, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		if venues.IsVenue(r.Context(), s.h.Engine().DB, sc.FestID) {
			return s.renderVenueDashboard(w, r, sc, "", "")
		}
		s.renderHostFestDashboard(w, r, sc.FestID, hostDashMessages{})
		return nil
	})
	t.Handle("POST "+fest, route.Manager, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		if venues.IsVenue(r.Context(), s.h.Engine().DB, sc.FestID) {
			return s.handleHostUpdateVenue(w, r, sc)
		}
		s.handleHostUpdateFest(w, r, sc.FestID)
		return nil
	})
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
	if !venue {
		t.Handle("GET "+fest+"/game/new", route.Manager, page(func(w http.ResponseWriter, r *http.Request, id int64) { s.renderHostCreateGamePage(w, r, id, "", "") }))
		t.Handle("POST "+fest+"/game/new", route.Manager, page(s.handleHostCreateGame))
	}
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
	// {fest}/game/{gid}[/...] → the game page (ek/od/si/brain) for hosts.
	t.Handle("GET "+fest+"/game/{rest...}", route.Member, s.serveHostGamePage)
}

// handleSlotRoutes hangs a Venue's Slot off its Game: /host/venue/{v}/game/{g}
// is the Slot — its date, registration, applications, poll — and the table it
// is played on is /table under it. A Venue's Games are made here and not by
// the fest's /game/new: every one of them is a Slot.
func (s *Server) handleSlotRoutes(t *route.Table, venue string) {
	slot := venue + "/game/{game}"
	t.Handle("POST "+venue+"/game/new", route.Manager, s.handleHostCreateSlot)
	t.Handle("GET "+slot, route.Member, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		return s.renderSlotPage(w, r, sc, "", "")
	})
	t.Handle("POST "+slot, route.Manager, s.handleSlotSave)
	t.Handle("GET "+slot+"/tournaments", route.Manager, s.renderTournamentPicker)
	t.Handle("POST "+slot+"/tournament", route.Manager, s.handleSlotTournament)
	t.Handle("POST "+slot+"/reg", route.Manager, s.handleSlotReg)
	t.Handle("POST "+slot+"/token", route.Manager, s.handleSlotToken)
	t.Handle("POST "+slot+"/clone", route.Manager, s.handleSlotClone)
	t.Handle("POST "+slot+"/application/{app}/status", route.Manager, s.handleApplicationStatus)
	t.Handle("POST "+slot+"/application/{app}/edit", route.Manager, s.handleApplicationEdit)
	t.Handle("POST "+slot+"/application/{app}/revert", route.Manager, s.handleApplicationRevert)
	t.Handle("POST "+slot+"/contested/accept", route.Editor, s.handleContestedAccept)
	t.Handle("POST "+slot+"/contested/delete", route.Editor, s.handleContestedDelete)
	t.Handle("POST "+slot+"/voting", route.Manager, s.handleVotingSave)
	t.Handle("POST "+slot+"/voting/ballot/{ballot}", route.Manager, s.handleVotingBallot)
	t.Handle("GET "+slot+"/export/tours.xlsx", route.Member, s.handleSlotToursExport)
	t.Handle("GET "+slot+"/export/players.xlsx", route.Member, s.handleSlotPlayersExport)
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
