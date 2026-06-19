package dopeserver

import (
	"bytes"
	"context"
	"dope/dope/domain/imports"
	"dope/dope/domain/numbering"
	"dope/dope/platform/roles"
	"dope/dope/storage/festaccess"
	"dope/dope/storage/store"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

func (s *server) serveViewerHTML(w http.ResponseWriter, r *http.Request) {
	s.serveAppHTML(w, r, "static/viewer.html")
}

func (s *server) serveHostHTML(w http.ResponseWriter, r *http.Request) {
	s.serveAppHTML(w, r, "static/host.html")
}

// hostInitMarker is the placeholder string inside static/host.html that is
// replaced with the actual init JSON. Keep in sync with the file.
const (
	hostInitMarker   = "null;/*__HOST_INIT__*/"
	viewerInitMarker = "null;/*__VIEWER_INIT__*/"
	gameInitMarker   = "null;/*__GAME_INIT__*/"
)

type hostInitPayload struct {
	Route      hostInitRoute           `json:"route"`
	Fest       json.RawMessage         `json:"fest,omitempty"`
	Match      *store.MatchView        `json:"match,omitempty"`
	SeedImport *imports.SeedImportView `json:"seedImport,omitempty"`
	// TeamsUnnumbered mirrors gameInitPayload.TeamsUnnumbered for the EK host
	// surface: editing is blocked server-side until every team has a number.
	TeamsUnnumbered bool `json:"teamsUnnumbered,omitempty"`
	// CanEdit reflects table-editor rights, so the page can show host-only
	// actions (e.g. the .json.gz archive download) without a probe round trip.
	CanEdit bool `json:"canEdit,omitempty"`
}

type gameInitPayload struct {
	// FestID/GameID are the resolved numeric ids. The client needs the numeric
	// game id to build the SSE scope (`game-state:<id>`); the URL only carries
	// the slug, which does not match the numeric scope the server broadcasts.
	FestID int64           `json:"festID,omitempty"`
	GameID int64           `json:"gameID,omitempty"`
	Scheme json.RawMessage `json:"scheme,omitempty"`
	State  json.RawMessage `json:"state,omitempty"`
	Fest   json.RawMessage `json:"fest,omitempty"`
	// Seq is the game-state scope's seq at render time, so the SSE client seeds
	// its lastSeq to exactly the state it was handed. Without it every viewer
	// would start at 0 and the first remote edit would gap-resync them all at
	// once (a thundering-herd full-state refetch — the very thing deltas avoid).
	Seq uint64 `json:"seq"`
	// Epoch seeds the SSE client's epoch so it can detect a later server restart
	// (seq reset) and resync instead of silently dropping post-restart deltas.
	Epoch   string `json:"epoch,omitempty"`
	CanEdit bool   `json:"canEdit,omitempty"`
	// TeamsUnnumbered is true when the fest has active teams that lack a number.
	// Team number is the universal team identity, so editing is blocked server-
	// side (see requireNumberedTeams); the client uses this to show a banner
	// pointing the host at the numbers page.
	TeamsUnnumbered bool `json:"teamsUnnumbered,omitempty"`
	// Static marks a snapshot served in static (lockdown) mode: the client skips
	// the SSE connection and self-reloads on a jitter instead. See static_mode.go.
	Static bool `json:"static,omitempty"`
}

type viewerInitPayload struct {
	Route   hostInitRoute    `json:"route"`
	Fest    json.RawMessage  `json:"fest,omitempty"`
	Match   *store.MatchView `json:"match,omitempty"`
	Venues  json.RawMessage  `json:"venues,omitempty"`
	CanEdit bool             `json:"canEdit,omitempty"`
	// Static marks a snapshot served in static (lockdown) mode: the client skips
	// the SSE connection and self-reloads on a jitter instead. See static_mode.go.
	Static bool `json:"static,omitempty"`
}

type hostInitRoute struct {
	Mode      string `json:"mode"`
	FestID    int64  `json:"festID"`
	GameID    int64  `json:"gameID"`
	StageCode string `json:"stageCode,omitempty"`
	MatchCode string `json:"matchCode,omitempty"`
}

// serveHostHTMLWithInit renders static/host.html with window.__HOST_INIT__
// pre-populated for the current route, eliminating the cold API round trips
// the SPA would otherwise make immediately after parsing host.js. Falls back
// to plain serveHostHTML on any error so a payload bug never breaks the page.
func (s *server) serveHostHTMLWithInit(w http.ResponseWriter, r *http.Request, scope festScope, parts []string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	route := parseHostInitRoute(parts, scope)
	payload, err := s.buildHostInit(r.Context(), route)
	if err != nil {
		s.serveHostHTML(w, r)
		return
	}
	if user, ok := s.eng.LookupSession(r); ok {
		if role, err := festaccess.FestUserRoleFromQuery(r.Context(), s.eng.DB, scope.FestID, user.UserID); err == nil && roles.CanEditGameTables(role) {
			payload.CanEdit = true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		s.serveHostHTML(w, r)
		return
	}
	s.serveInjectedHTML(w, r, "static/host.html", hostInitMarker, data)
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
	if user, ok := s.eng.LookupSession(r); ok {
		if role, err := festaccess.FestUserRoleFromQuery(r.Context(), s.eng.DB, scope.FestID, user.UserID); err == nil && roles.CanEditGameTables(role) {
			payload.CanEdit = true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		s.serveAppHTML(w, r, htmlPath)
		return
	}
	s.serveInjectedHTML(w, r, htmlPath, gameInitMarker, data)
}

// serveViewerHTMLWithInit serves static/viewer.html with the relevant
// per-route data already populated.
func (s *server) serveViewerHTMLWithInit(w http.ResponseWriter, r *http.Request, scope festScope, parts []string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	route := parseHostInitRoute(parts, scope)
	payload, err := s.buildViewerInit(r.Context(), route)
	if err != nil {
		s.serveViewerHTML(w, r)
		return
	}
	if user, ok := s.eng.LookupSession(r); ok {
		if role, err := festaccess.FestUserRoleFromQuery(r.Context(), s.eng.DB, scope.FestID, user.UserID); err == nil && roles.CanEditGameTables(role) {
			payload.CanEdit = true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		s.serveViewerHTML(w, r)
		return
	}
	s.serveInjectedHTML(w, r, "static/viewer.html", viewerInitMarker, data)
}

// assetRefRe matches a local /static .js/.css reference in an HTML attribute
// (src=/href=), capturing the attribute name and the path. The [^"?]+ guard
// skips URLs that already carry a query string.
var assetRefRe = regexp.MustCompile(`(src|href)="(/static/[^"?]+\.(?:js|css))"`)

// versionAssetRefs appends a "?v=<content-hash>" cache-buster to every local
// /static .js/.css URL in an HTML body. The hash changes when the file's bytes
// change, so a deploy busts the browser cache the instant the (no-cache) HTML
// shell is re-fetched — without it the stable URL keeps serving the cached copy
// until its max-age expires. URLs whose asset has no known hash (disk/dev mode,
// where assets are already served no-cache) are left untouched.
func (s *server) versionAssetRefs(body []byte) []byte {
	if len(s.eng.AssetETags) == 0 {
		return body
	}
	return assetRefRe.ReplaceAllFunc(body, func(m []byte) []byte {
		sub := assetRefRe.FindSubmatch(m)
		path := string(sub[2])
		tag := strings.Trim(s.eng.AssetETags[path], `"`)
		if tag == "" {
			return m
		}
		return []byte(fmt.Sprintf(`%s="%s?v=%s"`, sub[1], path, tag))
	})
}

// writeAppHTML cache-busts the body's asset URLs, marks the shell no-cache (it
// embeds per-request init JSON and deploy-specific version pointers, so it must
// never be served stale), and writes it. HEAD returns headers only.
func (s *server) writeAppHTML(w http.ResponseWriter, r *http.Request, body []byte) {
	body = s.versionAssetRefs(body)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

// serveInjectedHTML reads an HTML file from the embedded asset FS, splices
// the JSON payload over the marker token, and writes it as the response. The
// caller is responsible for pre-marshaling and for ensuring the marker is
// present in the HTML. On any I/O or marker-mismatch error the function
// silently falls back to serving the file unchanged.
func (s *server) serveInjectedHTML(w http.ResponseWriter, r *http.Request, htmlPath, marker string, payload []byte) {
	body, err := fs.ReadFile(s.eng.Assets, htmlPath)
	if err != nil {
		s.serveAppHTML(w, r, htmlPath)
		return
	}
	markerBytes := []byte(marker)
	idx := bytes.Index(body, markerBytes)
	if idx < 0 {
		s.serveAppHTML(w, r, htmlPath)
		return
	}
	out := make([]byte, 0, len(body)+len(payload))
	out = append(out, body[:idx]...)
	out = append(out, payload...)
	out = append(out, body[idx+len(markerBytes):]...)
	s.writeAppHTML(w, r, out)
}

func (s *server) buildGameInit(ctx context.Context, scope festScope) (gameInitPayload, error) {
	payload := gameInitPayload{FestID: scope.FestID, GameID: scope.GameID}
	var schemeJSON, stateJSON string
	if err := s.eng.DB.QueryRowContext(ctx, `
select coalesce(scheme_json, ''), coalesce(state_json, '')
from games where fest_id = ? and id = ?`, scope.FestID, scope.GameID).Scan(&schemeJSON, &stateJSON); err != nil {
		return payload, err
	}
	if schemeJSON == "" {
		schemeJSON = "{}"
	}
	if stateJSON == "" {
		stateJSON = "{}"
	}
	payload.Scheme = json.RawMessage(schemeJSON)
	payload.State = json.RawMessage(stateJSON)
	payload.Seq = s.eng.CurrentStateSeq(fmt.Sprintf("game-state:%d", scope.GameID))
	payload.Epoch = s.eng.Epoch
	if festBytes, err := s.festViewBytes(scope.FestID, scope.GameID); err == nil {
		payload.Fest = festBytes
	}
	if unnumbered, err := numbering.HasUnnumbered(ctx, s.eng.DB, scope.FestID); err == nil {
		payload.TeamsUnnumbered = unnumbered
	}
	return payload, nil
}

func (s *server) buildViewerInit(ctx context.Context, route hostInitRoute) (viewerInitPayload, error) {
	payload := viewerInitPayload{Route: route}

	festBytes, err := s.festViewBytes(route.FestID, route.GameID)
	if err != nil {
		return payload, err
	}
	payload.Fest = festBytes

	switch route.Mode {
	case "match":
		mscope, err := s.verifyMatchInScope(ctx, festScope{FestID: route.FestID, GameID: route.GameID}, route.MatchCode)
		if err != nil {
			return payload, nil
		}
		match, err := s.loadScopedMatchViewSnapshot(mscope)
		if err != nil {
			return payload, nil
		}
		payload.Match = &match
	case "venues":
		venues, err := s.loadVenuesLocked(route.FestID)
		if err == nil {
			if venuesBytes, err := json.Marshal(venues); err == nil {
				payload.Venues = venuesBytes
			}
		}
	}
	return payload, nil
}

func parseHostInitRoute(parts []string, scope festScope) hostInitRoute {
	route := hostInitRoute{Mode: "grid", FestID: scope.FestID, GameID: scope.GameID}
	if len(parts) <= 2 {
		return route
	}
	switch parts[2] {
	case "venues":
		route.Mode = "venues"
	case "seed-import":
		route.Mode = "seedImport"
	case "matches":
		if len(parts) >= 4 {
			route.Mode = "match"
			route.MatchCode = parts[3]
		}
	case "stage":
		if len(parts) >= 4 {
			route.Mode = "stage"
			route.StageCode = parts[3]
		}
	}
	return route
}

func (s *server) buildHostInit(ctx context.Context, route hostInitRoute) (hostInitPayload, error) {
	payload := hostInitPayload{Route: route}

	festBytes, err := s.festViewBytes(route.FestID, route.GameID)
	if err != nil {
		return payload, err
	}
	payload.Fest = festBytes
	if unnumbered, err := numbering.HasUnnumbered(ctx, s.eng.DB, route.FestID); err == nil {
		payload.TeamsUnnumbered = unnumbered
	}

	switch route.Mode {
	case "match":
		mscope, err := s.verifyMatchInScope(ctx, festScope{FestID: route.FestID, GameID: route.GameID}, route.MatchCode)
		if err != nil {
			return payload, nil
		}
		match, err := s.loadScopedMatchViewSnapshot(mscope)
		if err != nil {
			return payload, nil
		}
		payload.Match = &match
	case "seedImport":
		view, err := imports.LoadSeedImportView(&s.eng, ctx, festScope{FestID: route.FestID, GameID: route.GameID})
		if err != nil {
			return payload, nil
		}
		payload.SeedImport = &view
	}
	return payload, nil
}

func (s *server) serveAppHTML(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := fs.ReadFile(s.eng.Assets, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.writeAppHTML(w, r, body)
}
