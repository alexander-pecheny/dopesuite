package dopeserver

import (
	"net/http"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/domain/venues"
	"dope/dope/export/gameexport"
	"dope/dope/storage/store"
	"dope/dope/web/route"

	"pecheny.me/dopecore/session"
)

// handleFestRouter serves /fest/…: the public fest page and the viewer game
// pages, as the table below.
func (s *server) handleFestRouter(w http.ResponseWriter, r *http.Request) {
	// A Venue's Games are watched under /venue/, the tree its page lives in.
	if strings.HasPrefix(r.URL.Path, "/fest/") {
		ref, tail, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/fest/"), "/")
		if ref != "" && tail != "" {
			if festID, err := store.ResolveFestID(r.Context(), s.eng.DB, ref); err == nil && festID > 0 &&
				venues.IsVenue(r.Context(), s.eng.DB, festID) {
				target := "/venue/" + ref + "/" + tail
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusMovedPermanently)
				return
			}
		}
	}
	s.fest().Mux.ServeHTTP(w, r)
}

// HandleVenueGameRouter serves /venue/{ref}/game/… — a Venue's Слот as anyone
// it seated watches it.
func (s *server) HandleVenueGameRouter(w http.ResponseWriter, r *http.Request) {
	// An ordinary fest is not in this tree at all: /venue/ is where a Venue's
	// Слот is watched, and its own /fest/ path is the one that serves it.
	ref, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/venue/"), "/")
	festID, err := store.ResolveFestID(r.Context(), s.eng.DB, ref)
	if err == nil && festID > 0 && !venues.IsVenue(r.Context(), s.eng.DB, festID) {
		http.NotFound(w, r)
		return
	}
	s.fest().Mux.ServeHTTP(w, r)
}

func (s *server) fest() *route.Table {
	s.festOnce.Do(func() {
		t := route.New(&s.eng, route.DenyAPI)
		t.Handle("GET /fest/{fest}", route.Public, func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
			s.renderPublicFestPage(w, r, sc.FestID)
			return nil
		})
		t.Handle("GET /fest/{fest}/game/{rest...}", route.Public, s.viewerGamePage)
		t.Handle("GET /venue/{venue}/game/{rest...}", route.Public, s.viewerGamePage)
		s.festTable = t
	})
	return s.festTable
}

// viewerGamePage is /fest/{fest}/game/{game}[/view…][/static] and the XLSX
// download /fest/{fest}/game/{game}.xlsx. A trailing /static segment forces
// the static snapshot (the always-on, edge-cacheable handle) whatever the load
// mode. The download is read-gated so hosts of a private fest can fetch it;
// the pages need the fest to be public.
func (s *server) viewerGamePage(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	parts := append([]string{"game"}, strings.Split(strings.TrimSuffix(r.PathValue("rest"), "/"), "/")...)
	forceStatic := false
	if len(parts) > 2 && parts[len(parts)-1] == "static" {
		forceStatic = true
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 2 && strings.HasSuffix(parts[1], ".xlsx") {
		gameID, err := resolveGameID(r.Context(), s.eng.DB, sc.FestID, strings.TrimSuffix(parts[1], ".xlsx"))
		if err != nil || gameID <= 0 {
			return route.NotFound
		}
		if _, ok := s.api().Admit(w, r, route.Read, sc.FestID, gameID); !ok {
			return nil
		}
		gameexport.HandleScopedGameExport(s, w, r, sc.FestID, gameID)
		return nil
	}
	if !route.GamePagePath(parts, false) {
		return route.NotFound
	}
	// The game is resolved before the access check: a Venue grants a team the
	// Слот it plays and no other, so the check needs to know which one.
	gameID, _ := resolveGameID(r.Context(), s.eng.DB, sc.FestID, parts[1])
	if _, ok := s.fest().Admit(w, r, route.PublicFest, sc.FestID, gameID); !ok {
		return nil
	}
	if gameID <= 0 {
		s.serveEKHTML(w, r, games.Get("").Page)
		return nil
	}
	var gameType string
	if err := s.eng.DB.QueryRowContext(r.Context(), `select game_type from games where id = ? and fest_id = ?`, gameID, sc.FestID).Scan(&gameType); err != nil {
		s.serveEKHTML(w, r, games.Get("").Page)
		return nil
	}
	scope := festScope{FestID: sc.FestID, GameID: gameID}
	def := games.Get(gameType)
	initRoute := parseEKInitRoute(parts, scope)
	if def.Init == games.InitGame {
		// A page on the flat game init renders the whole game regardless of
		// sub-route, so collapse to one snapshot cache key.
		initRoute = ekInitRoute{Mode: "grid", FestID: sc.FestID, GameID: gameID}
	}
	serveStatic, release := lockdownServes(forceStatic, s.eng.StaticMode.Load(), session.HasCookie(r), &s.eng.LiveFallthrough)
	defer release()
	if serveStatic {
		s.serveStaticSnapshot(w, r, initRoute)
		return nil
	}
	if def.Init == games.InitEK {
		s.serveEKHTMLWithInit(w, r, scope, parts, def.Page)
	} else {
		s.serveGameHTMLWithInit(w, r, def.Page, scope)
	}
	return nil
}
