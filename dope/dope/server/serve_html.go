package dopeserver

import (
	"context"
	"dope/dope/domain/core"
	"dope/dope/domain/numbering"
	"dope/dope/domain/towns"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"
	"encoding/json"
	"net/http"
	"strconv"

	"pecheny.me/dopecore/webassets"
)

// gameInitMarker is the placeholder string inside every game page that is
// replaced with the actual init JSON. Keep in sync with the pages.
const gameInitMarker = "null;/*__GAME_INIT__*/"

type gameInitPayload struct {
	// FestID/GameID are the resolved numeric ids. The client needs the numeric
	// game id to build the SSE scope (`game-state:<id>`); the URL only carries
	// the slug, which does not match the numeric scope the server broadcasts.
	FestID int64           `json:"festID,omitempty"`
	GameID int64           `json:"gameID,omitempty"`
	Scheme json.RawMessage `json:"scheme,omitempty"`
	State  json.RawMessage `json:"state,omitempty"`
	Fest   json.RawMessage `json:"fest,omitempty"`
	// ScreenSettings is the per-game screen projector-board configuration
	// (colours, font scale, columns, city/country toggles). Shared by all hosts
	// of the game; the client seeds its settings panel from it on load.
	ScreenSettings json.RawMessage `json:"screenSettings,omitempty"`
	// CityCountry is the ISO-3166 code of each city on the fest's roster, so the
	// screen draws its flags from rating.chgk.info's answer rather than from the
	// list of cities the page carries as a fallback.
	CityCountry map[string]string `json:"cityCountry,omitempty"`
	// Seq is the game-state scope's seq at render time, so the SSE client seeds
	// its lastSeq to exactly the state it was handed. Without it every viewer
	// would start at 0 and the first remote edit would gap-resync them all at
	// once (a thundering-herd full-state refetch — the very thing deltas avoid).
	Seq uint64 `json:"seq"`
	// Epoch seeds the SSE client's epoch so it can detect a later server restart
	// (seq reset) and resync instead of silently dropping post-restart deltas.
	Epoch   string `json:"epoch,omitempty"`
	CanEdit bool   `json:"canEdit,omitempty"`
	// TeamsUnnumbered is true when the numbering guard blocks this game: the
	// teams it seats lack a number (numbering.GameHasUnnumbered). Team number
	// is the universal team identity, so editing is blocked server-side; the
	// client uses this to show a banner pointing the host at the numbers page.
	TeamsUnnumbered bool `json:"teamsUnnumbered,omitempty"`
	// Static marks a snapshot served in static (lockdown) mode: the client skips
	// the SSE connection and self-reloads on a jitter instead. See static_mode.go.
	Static bool `json:"static,omitempty"`
}

// staticRoute is what a lockdown snapshot is cached under: one per Game, since
// every game page draws the whole Game whatever its URL says.
type staticRoute struct {
	FestID int64
	GameID int64
}

// canEdit says whether the request's session may edit this Game's tables —
// what the init payload tells the page about its controls. A host an admin
// limited to other Games may not.
func (s *server) canEdit(r *http.Request, festID, gameID int64) bool {
	user, ok := s.eng.LookupSession(r)
	if !ok {
		return false
	}
	role, err := festaccess.FestUserRoleFromQuery(r.Context(), s.eng.DB, festID, user.UserID)
	if err != nil {
		return false
	}
	may, err := festaccess.MayRunGame(r.Context(), s.eng.DB, festID, gameID, user.UserID, role)
	return err == nil && may
}

// serveGameHTMLWithInit serves od.html or si.html with window.__GAME_INIT__
// populated with scheme/state/fest, sparing the JS three cold API round trips
// on first load. Falls back to the plain HTML on any error.
func (s *server) serveGameHTMLWithInit(w http.ResponseWriter, r *http.Request, htmlPath string, scope festScope) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	payload, err := s.buildGameInit(r.Context(), scope)
	if err != nil {
		s.serveAppHTML(w, r, htmlPath)
		return
	}
	payload.CanEdit = s.canEdit(r, scope.FestID, scope.GameID)
	data, err := json.Marshal(payload)
	if err != nil {
		s.serveAppHTML(w, r, htmlPath)
		return
	}
	s.serveInjectedHTML(w, r, htmlPath, gameInitMarker, data)
}

// versionAssetRefs appends "?v=<content-hash>" to every local /static .js/.css
// URL (webassets.VersionRefs), so a deploy busts the browser cache the instant
// the no-cache HTML shell is re-fetched.
func (s *server) versionAssetRefs(body []byte) []byte {
	return webassets.VersionRefs(s.eng.AssetETags, body)
}

// writeAppHTML cache-busts the body's asset URLs and writes it as a shell.
func (s *server) writeAppHTML(w http.ResponseWriter, r *http.Request, body []byte) {
	s.writeShell(w, r, s.versionAssetRefs(body))
}

// writeShell marks a shell no-cache (it embeds per-request init JSON and
// deploy-specific version pointers, so it must never be served stale) and
// writes it. HEAD returns headers only.
func (s *server) writeShell(w http.ResponseWriter, r *http.Request, body []byte) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

// serveInjectedHTML writes a shell with the init JSON spliced over its marker.
// On a missing page or marker it falls back to the file unchanged, so a
// payload bug never breaks the page.
func (s *server) serveInjectedHTML(w http.ResponseWriter, r *http.Request, htmlPath, marker string, payload []byte) {
	body, err := s.renderInjectedBytes(htmlPath, marker, payload)
	if err != nil {
		s.serveAppHTML(w, r, htmlPath)
		return
	}
	s.writeShell(w, r, body)
}

func (s *server) buildGameInit(ctx context.Context, scope festScope) (gameInitPayload, error) {
	payload := gameInitPayload{FestID: scope.FestID, GameID: scope.GameID}
	doc, err := store.LoadGameDoc(ctx, s.eng.DB, scope.FestID, scope.GameID)
	if err != nil {
		return payload, err
	}
	schemeJSON, stateJSON, screenSettingsJSON := doc.SchemeJSON, doc.State, doc.Screen
	if screenSettingsJSON == "" {
		screenSettingsJSON = "{}"
	}
	payload.Scheme = json.RawMessage(schemeJSON)
	payload.State = json.RawMessage(stateJSON)
	payload.ScreenSettings = json.RawMessage(screenSettingsJSON)
	payload.Seq = s.eng.CurrentStateSeq(core.GameStateScope(scope.GameID))
	payload.Epoch = s.eng.Epoch
	if festBytes, err := s.festViewBytes(scope.FestID, scope.GameID); err == nil {
		payload.Fest = festBytes
	}
	if unnumbered, err := numbering.GameHasUnnumbered(ctx, s.eng.DB, scope.FestID, scope.GameID); err == nil {
		payload.TeamsUnnumbered = unnumbered
	}
	payload.CityCountry = towns.FestCityCountries(ctx, s.eng.DB, s.eng.Buff, scope.FestID)
	return payload, nil
}

func (s *server) serveAppHTML(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := s.pageBytes(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.writeAppHTML(w, r, body)
}
