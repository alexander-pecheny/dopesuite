package dopeserver

import (
	"net/http"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/export/gameexport"
	"dope/dope/web/route"

	"pecheny.me/dopecore/session"
)

// handleFestRouter serves /fest/…: the public fest page and the viewer game
// pages, as the table below.
func (s *server) handleFestRouter(w http.ResponseWriter, r *http.Request) {
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
	if _, ok := s.fest().Admit(w, r, route.PublicFest, sc.FestID, 0); !ok {
		return nil
	}
	gameID, err := resolveGameID(r.Context(), s.eng.DB, sc.FestID, parts[1])
	if err != nil || gameID <= 0 {
		s.serveEKHTML(w, r)
		return nil
	}
	var gameType string
	if err := s.eng.DB.QueryRowContext(r.Context(), `select game_type from games where id = ? and fest_id = ?`, gameID, sc.FestID).Scan(&gameType); err != nil {
		s.serveEKHTML(w, r)
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
		s.serveEKHTMLWithInit(w, r, scope, parts)
	} else {
		s.serveGameHTMLWithInit(w, r, def.Page, scope)
	}
	return nil
}
