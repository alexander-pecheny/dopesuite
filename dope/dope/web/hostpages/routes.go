package hostpages

import (
	"net/http"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/games"
	"dope/dope/domain/multifest"
	"dope/dope/storage/festaccess"
	"dope/dope/web/pages"
	"dope/dope/web/route"
	dopeui "dope/dope/web/ui"
	dopestrings "dope/i18nstrings"
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

func (s *Server) denyHost(w http.ResponseWriter, r *http.Request, d route.Denial) {
	switch d {
	case route.NoSession, route.NoRole:
		http.Redirect(w, r, "/host", http.StatusSeeOther)
	case route.Forbidden:
		s.renderForbidden(w, r, dopestrings.Default.Host.Forbidden.Note())
	default:
		route.DenyAPI(w, r, d)
	}
}

// isSynch says whether the request's fest is a synch fest, whose teams and
// numbers live on its venues' pages and which has no use for the regular
// fest's teams, numbers, players and troikas pages.
func (s *Server) isSynch(r *http.Request, sc route.Scope) (bool, error) {
	kind, err := multifest.FestKind(r.Context(), s.h.Engine().DB, sc.FestID)
	return kind == multifest.Synch, err
}

// toVenues sends a synch fest's visitor to the dashboard's venues.
func (s *Server) toVenues(w http.ResponseWriter, r *http.Request, err error) error {
	if err != nil {
		return err
	}
	http.Redirect(w, r, "/host/fest/"+r.PathValue("fest")+"#venues", http.StatusSeeOther)
	return nil
}

// notSynch serves a regular fest's page, and sends a synch fest's visitor to
// its venues.
func (s *Server) notSynch(f func(http.ResponseWriter, *http.Request, route.Scope)) route.Handler {
	return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		if synch, err := s.isSynch(r, sc); err != nil || synch {
			return s.toVenues(w, r, err)
		}
		f(w, r, sc)
		return nil
	}
}

// renderForbidden is a 403 a person can read, with the way back to the fest.
func (s *Server) renderForbidden(w http.ResponseWriter, r *http.Request, note string) {
	str := dopestrings.Default
	pages.RenderDocStatus(w, s.h.Engine().AssetETags, &dopeui.Doc{Nodes: []dopeui.Node{dopeui.Page(
		dopeui.Title(str.Host.Forbidden.Title()), dopeui.PagePublic,
		dopeui.Publictopbar(pages.Trail(pages.HostCrumbs(), str.Host.Forbidden.Title())),
		dopeui.Section(dopeui.Subhead(dopeui.Text(str.Host.Forbidden.Heading())), dopeui.Hint(dopeui.Text(note)),
			dopeui.Row(dopeui.Button(dopeui.Href("/host/fest/"+r.PathValue("fest")), dopeui.Text(str.Host.Forbidden.OpenFest())))),
	)}}, http.StatusForbidden)
}

func (s *Server) buildRoutes() *route.Table {
	t := route.New(s.h.Engine(), s.denyHost)
	const fest, game = "/host/fest/{fest}", "/host/fest/{fest}/game/{game}"
	page := func(f func(http.ResponseWriter, *http.Request, int64)) route.Handler {
		return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error { f(w, r, sc.FestID); return nil }
	}
	pageScoped := func(f func(http.ResponseWriter, *http.Request, int64)) func(http.ResponseWriter, *http.Request, route.Scope) {
		return func(w http.ResponseWriter, r *http.Request, sc route.Scope) { f(w, r, sc.FestID) }
	}
	scoped := func(f func(http.ResponseWriter, *http.Request, route.Scope)) route.Handler {
		return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error { f(w, r, sc); return nil }
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
	t.Handle("GET "+fest, route.Member, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		// A host of one venue works on that venue's page alone.
		if venues, all, err := festaccess.RosterVenues(r.Context(), s.h.Engine().DB, sc.FestID, sc.User.UserID, sc.Role); err != nil {
			return err
		} else if !all && len(venues) == 1 {
			http.Redirect(w, r, venuePageURL(r.PathValue("fest"), venues[0]), http.StatusSeeOther)
			return nil
		}
		s.renderHostFestDashboard(w, r, sc.FestID, hostDashMessages{})
		return nil
	})
	t.Handle("POST "+fest, route.Manager, page(s.handleHostUpdateFest))
	t.Handle("GET "+fest+"/teams", route.Roster, scoped(s.renderHostFestTeams))
	t.Handle("POST "+fest+"/teams", route.Roster, scoped(s.handleHostSaveFestTeamFlags))
	t.Handle("GET "+fest+"/venue/{venue}", route.Roster, s.renderHostVenuePage)
	t.Handle("GET "+fest+"/players", route.Manager, s.notSynch(pageScoped(s.renderHostFestPlayers)))
	t.Handle("GET "+fest+"/troikas", route.Manager, s.notSynch(pageScoped(s.renderHostFestTroikas)))
	t.Handle("POST "+fest+"/troikas", route.Manager, page(s.handleHostSaveTroikas))
	t.Handle("POST "+fest+"/players/overrides", route.Manager, page(s.handleHostAddPlayerOverride))
	t.Handle("POST "+fest+"/access", route.Manager, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		s.handleHostSaveAccess(w, r, sc.FestID, sc.User.UserID)
		return nil
	})
	t.Handle("POST "+fest+"/invites", route.Manager, s.handleHostCreateInvite)
	for _, kind := range inviteKinds {
		t.Handle("POST "+fest+"/"+kind.path+"/{id}/revoke", route.Manager, s.handleHostChangeInvite(kind, false))
		t.Handle("POST "+fest+"/"+kind.path+"/{id}/delete", route.Manager, s.handleHostChangeInvite(kind, true))
	}
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
	t.Handle("GET "+fest+"/numbers", route.Roster, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		// A venue numbers its teams on its own page.
		if venue, _, err := s.pages().NumbersVenue(r, sc); err != nil {
			return err
		} else if venue > 0 {
			http.Redirect(w, r, venuePageURL(r.PathValue("fest"), venue), http.StatusSeeOther)
			return nil
		}
		if synch, err := s.isSynch(r, sc); err != nil || synch {
			return s.toVenues(w, r, err)
		}
		s.pages().RenderHostFestNumbers(w, r, sc, "", "", nil)
		return nil
	})
	t.Handle("POST "+fest+"/numbers", route.Roster, s.notSynch(s.pages().HandleHostSaveFestNumbers))
	t.Handle("POST "+fest+"/numbers/auto", route.Roster, s.notSynch(s.pages().HandleHostAutoFestNumbers))
	t.Handle("POST "+fest+"/numbers/clear", route.Roster, s.notSynch(s.pages().HandleHostClearFestNumbers))
	t.Handle("POST "+fest+"/numbers/import/match", route.Roster, s.notSynch(s.pages().HandleHostFestNumbersImportMatch))
	t.Handle("POST "+fest+"/numbers/import/apply", route.Roster, s.notSynch(s.pages().HandleHostFestNumbersImportApply))
	t.Handle("GET "+fest+"/rating/import", route.Manager, page(func(w http.ResponseWriter, r *http.Request, id int64) { s.renderHostRatingImportPage(w, r, id, "", "") }))
	t.Handle("POST "+fest+"/rating/import", route.Manager, page(s.handleHostImportRatingRoster))
	t.Handle("POST "+fest+"/rating/undo", route.Manager, page(s.handleHostUndoRatingImport))
	t.Handle("GET "+fest+"/rating/export", route.Manager, scoped(s.renderRatingExport))
	t.Handle("POST "+fest+"/rating/export", route.Manager, scoped(s.renderRatingExport))
	t.Handle("POST "+fest+"/rating/export/identify", route.Manager, scoped(s.renderRatingIdentify))
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
	if route.RedirectToGamePage(w, r, "/host/fest/"+r.PathValue("fest"), parts, false) {
		return nil
	}
	gameID, err := s.h.ResolveGameID(r.Context(), sc.FestID, parts[1])
	if err != nil || gameID <= 0 {
		return route.NotFound
	}
	var gameType string
	if err := s.h.Engine().DB.QueryRowContext(r.Context(), `select game_type from games where id = ? and fest_id = ?`, gameID, sc.FestID).Scan(&gameType); err != nil {
		return err
	}
	// A host an admin limited to other Games watches this one as a viewer: the
	// same page under /fest, where nothing is editable, rather than an editor
	// whose every write would be refused.
	may, err := festaccess.MayRunGame(r.Context(), s.h.Engine().DB, sc.FestID, gameID, sc.User.UserID, sc.Role)
	if err != nil {
		return err
	}
	if !may {
		// A host of a venue enters its sheet of the whole fest's Game.
		sc.GameID = gameID
		sheets, err := route.SheetVenues(r.Context(), s.h.Engine().DB, sc)
		if err != nil {
			return err
		}
		may = len(sheets) > 0
	}
	if !may {
		http.Redirect(w, r, "/fest/"+r.PathValue("fest")+"/"+strings.Join(parts, "/"), http.StatusSeeOther)
		return nil
	}
	scope := core.FestScope{FestID: sc.FestID, GameID: gameID}
	s.h.ServeGameHTMLWithInit(w, r, games.Get(gameType).Page, scope)
	return nil
}
