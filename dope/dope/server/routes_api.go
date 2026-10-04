package dopeserver

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dope/dope/domain/core"
	"dope/dope/domain/edit"
	"dope/dope/domain/entrants"
	"dope/dope/domain/games"
	"dope/dope/domain/imports"
	"dope/dope/domain/resolver"
	"dope/dope/domain/roster"
	"dope/dope/export/gameexport"
	"dope/dope/platform/metrics"
	"dope/dope/platform/realtime"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	"dope/dope/web/route"
	dopestrings "dope/i18nstrings"
)

// api is the /api/fest/ table, built once; handleScopedAPI is its entry for
// the mux and the tests.
func (s *server) api() *route.Table {
	s.apiOnce.Do(func() { s.apiTable = s.apiRoutes() })
	return s.apiTable
}

func (s *server) handleScopedAPI(w http.ResponseWriter, r *http.Request) { s.api().Mux.ServeHTTP(w, r) }

// apiRoutes is the /api/fest/ table: every scoped endpoint as one row — its
// pattern, what it asks of the caller, its handler. The dispatcher resolves
// {fest} and {game}, checks the role and the numbering guard, and writes
// whatever error the handler returns.
func (s *server) apiRoutes() *route.Table {
	t := route.New(&s.eng, route.DenyAPI)
	t.Handle("GET /api/auth/tokens", route.Session, s.apiTokensList)
	t.Handle("POST /api/auth/tokens", route.Session, s.apiTokensCreate)
	t.Handle("DELETE /api/auth/tokens/{id}", route.Session, s.apiTokensRevoke)
	const fest, game = "/api/fest/{fest}", "/api/fest/{fest}/games/{game}"
	t.Handle("GET "+fest, route.Read, s.scopedFest)
	t.Handle("POST "+fest+"/presence", route.Editor, s.hostPresence)
	t.Handle("GET "+fest+"/venues", route.Read, s.scopedVenues)
	t.Handle("POST "+fest+"/venues", route.Editor, s.scopedVenueCreate)
	t.Handle("PUT "+fest+"/venues/{n}", route.Editor, s.scopedVenuePut)
	t.Handle("DELETE "+fest+"/venues/{n}", route.Editor, s.scopedVenueDelete)
	t.Handle("GET "+fest+"/roster", route.Read, s.scopedFestRoster)
	t.Handle("GET "+game, route.Read, s.scopedGame)
	t.Handle("GET "+game+"/roster", route.Read, s.scopedGameRoster)
	t.Handle("PUT "+game+"/rosters/{participant}", route.Editor, s.scopedGameRosterPut)
	t.Handle("DELETE "+game+"/rosters/{participant}", route.Editor, s.scopedGameRosterReset)
	t.Handle("GET "+game+"/matches/{code}", route.Read, s.scopedMatch)
	t.Handle("PATCH "+game+"/matches/{code}/state", route.Editor.Numbered(), s.scopedMatchPatch)
	t.Handle("POST "+game+"/matches/{code}/finish", route.Editor.Numbered(), s.scopedMatchFinish)
	t.Handle("POST "+game+"/matches/{code}/venue", route.Editor, s.scopedMatchVenue)
	t.Handle("POST "+game+"/matches/{code}/starts-at", route.Editor, s.scopedMatchStartsAt)
	t.Handle("GET "+game+"/stages/matches", route.Read, s.scopedAllStageMatches)
	t.Handle("GET "+game+"/stages/{stage}/matches", route.Read, s.scopedStageMatches)
	t.Handle("POST "+game+"/stages/{stage}/reseed", route.Editor.Numbered(), s.scopedReseed)
	t.Handle("PUT "+game+"/draw", route.Manager.Numbered(), s.scopedDraw)
	t.Handle("GET "+game+"/state", route.Read, s.scopedGameState)
	t.Handle("PUT "+game+"/state", route.Editor.Numbered(), s.scopedGameStatePut)
	t.Handle("PATCH "+game+"/state", route.Editor.Numbered(), s.scopedGameStatePatch)
	t.Handle("POST "+game+"/guests", route.Editor.Numbered(), s.scopedMultiGuestAdd)
	t.Handle("PUT "+game+"/guests/{n}", route.Editor.Numbered(), s.scopedMultiGuestRename)
	t.Handle("DELETE "+game+"/guests/{n}", route.Editor.Numbered(), s.scopedMultiGuestRemove)
	t.Handle("GET "+game+"/scheme", route.Read, s.scopedGameScheme)
	t.Handle("GET "+game+"/screen-settings", route.Read, s.scopedScreenSettings)
	t.Handle("PUT "+game+"/screen-settings", route.Editor, s.scopedScreenSettingsPut)
	t.Handle("GET "+game+"/results", route.Read, s.gameexportRoute(gameexport.HandleScopedGameResults))
	t.Handle("GET "+game+"/export.xlsx", route.Read, s.gameexportRoute(gameexport.HandleScopedGameExport))
	t.Handle("GET "+game+"/export.json.gz", route.Editor, s.gameexportRoute(gameexport.HandleScopedGameArchive))
	// The entrants tab (entrants): the list, its import, the hand edits.
	t.Handle("GET "+game+"/entrants", route.Editor, s.scopedEntrants)
	t.Handle("POST "+game+"/entrants/import", route.Editor.Numbered(), s.scopedEntrantsImport)
	t.Handle("POST "+game+"/entrants", route.Editor.Numbered(), s.scopedEntrantAdd)
	t.Handle("PATCH "+game+"/entrants/{participant}", route.Editor.Numbered(), s.scopedEntrantEdit)
	t.Handle("DELETE "+game+"/entrants/{participant}", route.Editor.Numbered(), s.scopedEntrantRemove)
	// The seed tab's routes from before, kept for the tests and any old page:
	// they answer the same view and run through the same list.
	t.Handle("GET "+game+"/seed-import", route.Editor, s.scopedEntrants)
	t.Handle("POST "+game+"/seed-import/ksi", route.Editor.Numbered(), s.seedImportRoute(func(*http.Request) (imports.SeedSource, error) { return imports.FromKSI(), nil }))
	t.Handle("POST "+game+"/seed-import/run", route.Editor.Numbered(), s.seedImportRoute(func(*http.Request) (imports.SeedSource, error) { return imports.FromScheme(), nil }))
	t.Handle("POST "+game+"/seed-import/xlsx", route.Editor.Numbered(), s.seedImportRoute(seedXLSXSource))
	t.Handle("POST "+game+"/seed-import/decline", route.Editor, s.scopedSeedDecline)
	t.Handle("POST "+fest+"/scheme-import", route.Manager, s.scopedSchemeImport)
	// The host pages' forms, as JSON (ADR-0021).
	s.hostPageServer().APIRoutes(t)
	return t
}

func (s *server) gameexportRoute(h func(gameexport.Host, http.ResponseWriter, *http.Request, int64, int64)) route.Handler {
	return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		h(s, w, r, sc.FestID, sc.GameID)
		return nil
	}
}

// gameStateScopeKey is the SSE scope a Game's whole document is broadcast on.
func gameStateScopeKey(gameID int64) string { return core.GameStateScope(gameID) }

func (s *server) scopedFest(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	gameID, err := defaultGameID(r.Context(), s.eng.DB, sc.FestID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	data, err := s.festViewBytes(sc.FestID, gameID)
	if err != nil {
		return err
	}
	return route.JSONBytes(w, data)
}

func (s *server) scopedGame(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	data, err := s.festViewBytes(sc.FestID, sc.GameID)
	if err != nil {
		return err
	}
	return route.JSONBytes(w, data)
}

// scopedFestRoster serves the fest's canonical team→players roster for the
// read-only rosters tab, visible to every visitor of a public fest.
func (s *server) scopedFestRoster(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	teams, err := roster.LoadFestRosterView(r.Context(), s.eng.DB, sc.FestID)
	if err != nil {
		return err
	}
	if teams == nil {
		teams = []roster.FestRosterTeamView{}
	}
	return route.JSON(w, map[string]any{"teams": teams})
}

func (s *server) hostPresence(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req hostPresenceRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	if active {
		if len(req.Cursor) == 0 || !json.Valid(req.Cursor) {
			return route.BadRequest("bad cursor")
		}
	} else {
		req.Cursor = nil
	}
	username := fmt.Sprintf("user-%d", sc.User.UserID)
	if sc.User.Username.Valid && strings.TrimSpace(sc.User.Username.String) != "" {
		username = sc.User.Username.String
	}
	data, err := json.Marshal(hostPresenceMessage{
		UserID:    sc.User.UserID,
		Username:  username,
		Color:     hostPresenceColor(sc.User.UserID),
		Active:    active,
		Cursor:    req.Cursor,
		UpdatedAt: util.UtcNow(),
	})
	if err != nil {
		return err
	}
	s.eng.RT.BroadcastHostPresence(realtime.HostPresenceEvent{FestID: sc.FestID, Data: data})
	return route.JSONBytes(w, data)
}

func (s *server) scopedVenues(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	venues, err := s.loadVenuesLocked(sc.FestID)
	if err != nil {
		return err
	}
	return route.JSON(w, venues)
}

func (s *server) scopedVenuePut(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	number, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || number <= 0 {
		return route.BadRequest("bad venue number")
	}
	var req venueUpdateRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	venues, revision, err := s.updateVenue(r.Context(), sc.FestID, number, req.Title)
	if err != nil {
		return route.BadUser(err)
	}
	return route.JSONBytes(w, s.broadcastVenues(sc.FestID, venueChange{Venues: venues, Revision: revision}))
}

// scopedVenueCreate adds a venue to the fest and seats there the bouts the
// Games' schemes put at its number. It answers with the fest's venues.
func (s *server) scopedVenueCreate(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req venueCreateRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Number < 0 {
		return route.BadRequest("bad venue number")
	}
	change, err := s.createVenue(r.Context(), sc.FestID, req.Number, req.Title)
	if err != nil {
		return route.BadUser(err)
	}
	return route.JSONBytes(w, s.broadcastVenues(sc.FestID, change))
}

// scopedVenueDelete removes a venue no bout plays at, and answers with the
// fest's venues.
func (s *server) scopedVenueDelete(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	number, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || number <= 0 {
		return route.BadRequest("bad venue number")
	}
	change, err := s.deleteVenue(r.Context(), sc.FestID, number)
	if err != nil {
		return route.BadUser(err)
	}
	return route.JSONBytes(w, s.broadcastVenues(sc.FestID, change))
}

// ---- matches ----

func (s *server) matchScopeOf(r *http.Request, sc route.Scope) (matchScope, error) {
	mscope, err := s.verifyMatchInScope(r.Context(), sc.Fest(), r.PathValue("code"))
	if errors.Is(err, errMatchNotFound) {
		return matchScope{}, route.NotFound
	}
	return mscope, err
}

func (s *server) scopedMatch(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	mscope, err := s.matchScopeOf(r, sc)
	if err != nil {
		return err
	}
	view, err := s.loadScopedMatchViewSnapshot(mscope)
	if err != nil {
		return route.Statusf(http.StatusNotFound, "%v", err)
	}
	view.Seq = s.eng.CurrentStateSeq(matchScopeKey(mscope))
	return route.JSON(w, view)
}

// scopedMatchPatch applies edit ops to one Match. Ops are coalesced with every
// other edit to this game into a 150ms window and applied in one locked
// transaction; the call returns once that window has committed, with the match
// view (and seq) the flusher broadcast.
func (s *server) scopedMatchPatch(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	mscope, err := s.matchScopeOf(r, sc)
	if err != nil {
		return err
	}
	var sample metrics.Sample
	tE2E := metrics.NowIf(s.metrics.On)
	raw, req, err := readPatch(r)
	if err != nil {
		return err
	}
	data, _, err := s.editor().SubmitMatchEdit(r.Context(), sc.Fest(), mscope.MatchID, mscope.Code, req, raw, &sample)
	if err != nil {
		return route.BadUser(err)
	}
	if err := route.JSONBytes(w, data); err != nil {
		return err
	}
	if s.metrics.On {
		sample.E2E = time.Since(tE2E)
		s.metrics.RecordEdit(sample)
	}
	return nil
}

func readPatch(r *http.Request) (string, edit.PatchRequest, error) {
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return "", edit.PatchRequest{}, route.BadUser(err)
	}
	var req edit.PatchRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return "", req, route.BadRequest("bad json")
	}
	if len(req.Ops) == 0 {
		return "", req, route.BadRequest("missing patch ops")
	}
	return string(raw), req, nil
}

func (s *server) scopedMatchFinish(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	mscope, err := s.matchScopeOf(r, sc)
	if err != nil {
		return err
	}
	var req updateRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Finished == nil {
		return route.BadRequest("missing finished")
	}
	data, _, err := s.editor().SubmitMatchFinish(r.Context(), sc.Fest(), mscope.MatchID, mscope.Code, *req.Finished)
	if err != nil {
		return route.BadUser(err)
	}
	return route.JSONBytes(w, data)
}

func (s *server) scopedMatchVenue(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	mscope, err := s.matchScopeOf(r, sc)
	if err != nil {
		return err
	}
	var req matchVenueRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	number := req.Number
	if number == 0 {
		number = req.VenueNumber
	}
	data, _, err := s.editor().SubmitMatchVenue(r.Context(), sc.Fest(), mscope.MatchID, mscope.Code, number)
	if err != nil {
		return route.BadUser(err)
	}
	return route.JSONBytes(w, data)
}

// ---- stages ----

// scopedAllStageMatches is every stage's full MatchViews in one response, so
// the bracket page prefetches the whole game in one request.
func (s *server) scopedAllStageMatches(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	stages, err := s.loadAllStageMatchViews(r.Context(), sc.Fest())
	if err != nil {
		return err
	}
	return route.JSON(w, stages)
}

func (s *server) scopedStageMatches(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	matches, err := s.loadStageMatchViews(r.Context(), sc.Fest(), r.PathValue("stage"))
	if err != nil {
		return err
	}
	return route.JSON(w, matches)
}

func (s *server) scopedReseed(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	data, cascaded, revision, err := s.calculateScopedReseed(r.Context(), sc.Fest(), r.PathValue("stage"))
	switch {
	case errors.Is(err, resolver.ErrReseedStageNotFound):
		return route.NotFound
	case errors.Is(err, resolver.ErrReseedNotReady):
		return route.BadUser(err)
	case err != nil:
		return err
	}
	s.broadcastMatchCascade(sc.FestID, sc.GameID, cascaded)
	s.eng.BroadcastState(sc.FestID, festViewScopeKey(sc.Fest()), revision, data)
	return route.JSONBytes(w, data)
}

// scopedDraw seats a Draw Slot: Hamsa's three fourth places are drawn by lot
// before Game 2, and until the host enters the draw those seats stand empty.
func (s *server) scopedDraw(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req struct {
		Slot        string `json:"slot"`
		Participant int64  `json:"participant"`
	}
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	data, cascaded, revision, err := s.applyScopedDraw(r.Context(), sc.Fest(), req.Slot, req.Participant)
	switch {
	case errors.Is(err, resolver.ErrDrawSlotNotFound):
		return route.NotFound
	case err != nil:
		return err
	}
	s.broadcastMatchCascade(sc.FestID, sc.GameID, cascaded)
	s.eng.BroadcastState(sc.FestID, festViewScopeKey(sc.Fest()), revision, data)
	return route.JSONBytes(w, data)
}

// ---- the game document ----

func (s *server) scopedGameState(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	// Read the scope's seq BEFORE the state, not after. A write commits the new
	// state_json (under s.eng.Mu) and only then bumps the seq (under seqMu), so
	// reading seq first guarantees the state we read next is at least as new as
	// that seq — the returned X-State-Seq is never AHEAD of the returned body.
	// Erring low means at worst an already-applied delta re-applies
	// (idempotent) or one extra resync; erring high would make the client skip
	// the next delta and diverge permanently.
	seq := s.eng.CurrentStateSeq(gameStateScopeKey(sc.GameID))
	doc, err := store.LoadGameDoc(r.Context(), s.eng.DB, sc.FestID, sc.GameID)
	if err != nil {
		return err
	}
	// X-State-Seq lets a resyncing SSE client align its lastSeq with the state
	// it just fetched; X-State-Epoch says whether the seq space was reset by a
	// restart, so a low post-restart seq is adopted rather than treated as stale.
	w.Header().Set("X-State-Seq", strconv.FormatUint(seq, 10))
	w.Header().Set("X-State-Epoch", s.eng.Epoch)
	return route.JSONBytes(w, []byte(doc.State))
}

func (s *server) scopedGameStatePut(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return route.BadUser(err)
	}
	if !json.Valid(raw) {
		return route.BadRequest("bad json")
	}
	// Canonicalize so a wholesale PUT stores the same byte representation a
	// PATCH would: the stored state, the SSE payload and the response stay
	// identical whichever path produced them, which replay/diff rely on.
	if canon, err := core.CanonicalJSON(raw); err == nil {
		raw = canon
	}
	revision, err := s.replaceGameState(r.Context(), sc.Fest(), raw)
	if errors.Is(err, edit.ErrRatingRosterImmutable) {
		return route.BadUser(err)
	}
	if err != nil {
		return err
	}
	s.eng.BroadcastState(sc.FestID, gameStateScopeKey(sc.GameID), revision, raw)
	return route.JSONBytes(w, raw)
}

// scopedGameStatePatch applies edit ops to the whole document; like a Match
// patch, the ops ride the 150ms batch and the call returns the committed state.
func (s *server) scopedGameStatePatch(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var sample metrics.Sample
	tE2E := metrics.NowIf(s.metrics.On)
	raw, req, err := readPatch(r)
	if err != nil {
		return err
	}
	next, _, err := s.editor().SubmitEdit(r.Context(), sc.Fest(), req, raw, &sample)
	if errors.Is(err, sql.ErrNoRows) {
		return route.NotFound
	}
	if err != nil {
		return route.BadUser(err)
	}
	if err := route.JSONBytes(w, next); err != nil {
		return err
	}
	if s.metrics.On {
		sample.E2E = time.Since(tE2E)
		s.metrics.RecordEdit(sample)
	}
	return nil
}

// A Multi game's guest teams (games.AddMultiGuest): the host adds one by
// name, renames it, or removes it while nothing is entered for it. Each call
// returns the new state, which also goes out on the game-state scope.
type multiGuestRequest struct {
	Name string `json:"name"`
}

func (s *server) scopedMultiGuestAdd(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req multiGuestRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.editMultiGuests(w, r, sc, func(scheme, state string) ([]byte, []byte, error) {
		return games.AddMultiGuest(scheme, state, req.Name)
	})
}

func (s *server) scopedMultiGuestRename(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	number, err := multiGuestNumber(r)
	if err != nil {
		return err
	}
	var req multiGuestRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	return s.editMultiGuests(w, r, sc, func(scheme, state string) ([]byte, []byte, error) {
		return games.RenameMultiGuest(scheme, state, number, req.Name)
	})
}

func (s *server) scopedMultiGuestRemove(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	number, err := multiGuestNumber(r)
	if err != nil {
		return err
	}
	return s.editMultiGuests(w, r, sc, func(scheme, state string) ([]byte, []byte, error) {
		return games.RemoveMultiGuest(scheme, state, number)
	})
}

// multiGuestNumber reads the {n} of a guest team's URL: its Number, below zero.
func multiGuestNumber(r *http.Request) (int, error) {
	number, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || number >= 0 {
		return 0, route.BadRequest("bad guest number")
	}
	return number, nil
}

func (s *server) editMultiGuests(w http.ResponseWriter, r *http.Request, sc route.Scope, apply func(scheme, state string) ([]byte, []byte, error)) error {
	state, revision, err := s.rewriteMultiGuests(r.Context(), sc.Fest(), apply)
	if err != nil {
		return route.BadUser(err)
	}
	s.eng.BroadcastState(sc.FestID, gameStateScopeKey(sc.GameID), revision, state)
	return route.JSONBytes(w, state)
}

func (s *server) scopedGameScheme(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var schemeJSON string
	err := s.eng.DB.QueryRowContext(r.Context(), `
	select scheme_json from games where fest_id = ? and id = ?`, sc.FestID, sc.GameID).Scan(&schemeJSON)
	if err != nil {
		return err
	}
	if schemeJSON == "" {
		schemeJSON = "{}"
	}
	return route.JSONBytes(w, []byte(schemeJSON))
}

// The per-game screen projector-board configuration is an opaque blob the
// client owns; the server only checks that it is JSON.
func (s *server) scopedScreenSettings(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var settingsJSON string
	err := s.eng.DB.QueryRowContext(r.Context(), `
	select coalesce(screen_settings_json, '') from games where fest_id = ? and id = ?`, sc.FestID, sc.GameID).Scan(&settingsJSON)
	if err != nil {
		return err
	}
	if settingsJSON == "" {
		settingsJSON = "{}"
	}
	return route.JSONBytes(w, []byte(settingsJSON))
}

func (s *server) scopedScreenSettingsPut(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		return route.BadUser(err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	if !json.Valid(raw) {
		return route.BadRequest("bad json")
	}
	if err := s.updateGameScreenSettings(r.Context(), sc.Fest(), raw); err != nil {
		return err
	}
	return route.JSONBytes(w, raw)
}

// ---- the entrants tab (entrants) ----

func (s *server) scopedEntrants(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	view, err := entrants.Load(r.Context(), s.eng.DB, sc.Fest())
	if err != nil {
		return err
	}
	return route.JSON(w, view)
}

// answerEntrants tells everybody what a write to the list changed — the game
// document with the list in it, and the fest view whose seats moved — and
// answers the tab afresh. A rebuilt Structure also tells a Troika page, which
// resyncs its bouts on a fest event.
func (s *server) answerEntrants(w http.ResponseWriter, sc route.Scope, result entrants.Result, err error) error {
	if err != nil {
		return route.BadUser(err)
	}
	s.eng.InvalidateFestViewCache(sc.FestID)
	s.eng.BroadcastState(sc.FestID, gameStateScopeKey(sc.GameID), result.Revision, result.StateJSON)
	s.broadcastFestView(festScope{FestID: sc.FestID, GameID: sc.GameID}, result.Revision)
	return route.JSON(w, struct {
		entrants.View
		Rebuilt bool `json:"rebuilt,omitempty"`
	}{result.View, result.Rebuilt})
}

// scopedEntrantsImport takes the source as JSON, or, for an uploaded sheet, as
// a multipart form with the file under "file".
func (s *server) scopedEntrantsImport(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var source entrants.Source
	var file io.Reader
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			return route.BadRequest("bad form")
		}
		source = entrants.Source{Kind: entrants.SourceXLSX, Fresh: r.FormValue("fresh") == "1"}
		if upload, _, err := r.FormFile("file"); err == nil {
			defer upload.Close()
			file = upload
		}
	} else if err := route.DecodeJSON(r, &source); err != nil {
		return err
	}
	result, err := entrants.Import(&s.eng, r.Context(), sc.Fest(), source, file)
	return s.answerEntrants(w, sc, result, err)
}

func (s *server) scopedEntrantAdd(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req entrants.AddRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	result, err := entrants.Add(&s.eng, r.Context(), sc.Fest(), req)
	return s.answerEntrants(w, sc, result, err)
}

// entrantEdit is one change to an entrant: a new place in the list, a decline
// set or taken back, a one-off's new name, or another entrant in its place.
type entrantEdit struct {
	Position    *int                 `json:"position,omitempty"`
	Declined    *bool                `json:"declined,omitempty"`
	Name        *string              `json:"name,omitempty"`
	ReplaceWith *entrants.AddRequest `json:"replaceWith,omitempty"`
}

func (s *server) scopedEntrantEdit(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	participantID, err := strconv.ParseInt(r.PathValue("participant"), 10, 64)
	if err != nil || participantID <= 0 {
		return route.BadRequest("bad participant")
	}
	var req entrantEdit
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	var result entrants.Result
	switch {
	case req.Position != nil:
		result, err = entrants.Move(&s.eng, r.Context(), sc.Fest(), participantID, *req.Position)
	case req.Declined != nil:
		result, err = entrants.Decline(&s.eng, r.Context(), sc.Fest(), participantID, *req.Declined)
	case req.Name != nil:
		result, err = entrants.Rename(&s.eng, r.Context(), sc.Fest(), participantID, *req.Name)
	case req.ReplaceWith != nil:
		result, err = entrants.Replace(&s.eng, r.Context(), sc.Fest(), participantID, *req.ReplaceWith)
	default:
		return route.BadRequest("nothing to change")
	}
	return s.answerEntrants(w, sc, result, err)
}

func (s *server) scopedEntrantRemove(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	participantID, err := strconv.ParseInt(r.PathValue("participant"), 10, 64)
	if err != nil || participantID <= 0 {
		return route.BadRequest("bad participant")
	}
	result, err := entrants.Remove(&s.eng, r.Context(), sc.Fest(), participantID)
	return s.answerEntrants(w, sc, result, err)
}

// seedImportRoute runs one of the seed tab's old sources through the list;
// the three POST verbs differ only in the source.
func (s *server) seedImportRoute(source func(*http.Request) (imports.SeedSource, error)) route.Handler {
	return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		src, err := source(r)
		if err != nil {
			return err
		}
		result, err := entrants.ImportLegacy(&s.eng, r.Context(), sc.Fest(), src)
		return s.answerEntrants(w, sc, result, err)
	}
}

func seedXLSXSource(r *http.Request) (imports.SeedSource, error) {
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		return nil, route.BadRequest("bad form")
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, route.BadRequest(dopestrings.Default.Server.SeedImport.FileMissing())
	}
	return imports.FromXLSX(file), nil
}

func (s *server) scopedSeedDecline(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	var req imports.SeedDeclineRequest
	if err := route.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.TeamID <= 0 {
		return route.BadRequest("bad team id")
	}
	result, err := entrants.Decline(&s.eng, r.Context(), sc.Fest(), req.TeamID, req.Declined)
	return s.answerEntrants(w, sc, result, err)
}
