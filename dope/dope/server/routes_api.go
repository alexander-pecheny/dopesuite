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
	const fest, game = "/api/fest/{fest}", "/api/fest/{fest}/games/{game}"
	t.Handle("GET "+fest, route.Read, s.scopedFest)
	t.Handle("POST "+fest+"/presence", route.Editor, s.hostPresence)
	t.Handle("GET "+fest+"/venues", route.Read, s.scopedVenues)
	t.Handle("PUT "+fest+"/venues/{n}", route.Editor, s.scopedVenuePut)
	t.Handle("GET "+fest+"/roster", route.Read, s.scopedFestRoster)
	t.Handle("GET "+game, route.Read, s.scopedGame)
	t.Handle("GET "+game+"/matches/{code}", route.Read, s.scopedMatch)
	t.Handle("PATCH "+game+"/matches/{code}/state", route.Editor.Numbered(), s.scopedMatchPatch)
	t.Handle("POST "+game+"/matches/{code}/finish", route.Editor.Numbered(), s.scopedMatchFinish)
	t.Handle("POST "+game+"/matches/{code}/venue", route.Editor, s.scopedMatchVenue)
	t.Handle("GET "+game+"/stages/matches", route.Read, s.scopedAllStageMatches)
	t.Handle("GET "+game+"/stages/{stage}/matches", route.Read, s.scopedStageMatches)
	t.Handle("POST "+game+"/stages/{stage}/reseed", route.Editor.Numbered(), s.scopedReseed)
	t.Handle("GET "+game+"/state", route.Read, s.scopedGameState)
	t.Handle("PUT "+game+"/state", route.Editor.Numbered(), s.scopedGameStatePut)
	t.Handle("PATCH "+game+"/state", route.Editor.Numbered(), s.scopedGameStatePatch)
	t.Handle("GET "+game+"/scheme", route.Read, s.scopedGameScheme)
	t.Handle("GET "+game+"/screen-settings", route.Read, s.scopedScreenSettings)
	t.Handle("PUT "+game+"/screen-settings", route.Editor, s.scopedScreenSettingsPut)
	t.Handle("GET "+game+"/results", route.Read, s.gameexportRoute(gameexport.HandleScopedGameResults))
	t.Handle("GET "+game+"/export.xlsx", route.Read, s.gameexportRoute(gameexport.HandleScopedGameExport))
	t.Handle("GET "+game+"/export.json.gz", route.Editor, s.gameexportRoute(gameexport.HandleScopedGameArchive))
	t.Handle("GET "+game+"/seed-import", route.Editor, s.scopedSeedImportView)
	t.Handle("POST "+game+"/seed-import/ksi", route.Editor.Numbered(), s.seedImportRoute(func(*http.Request) (imports.SeedSource, error) { return imports.FromKSI(), nil }))
	t.Handle("POST "+game+"/seed-import/run", route.Editor.Numbered(), s.seedImportRoute(func(*http.Request) (imports.SeedSource, error) { return imports.FromScheme(), nil }))
	t.Handle("POST "+game+"/seed-import/xlsx", route.Editor.Numbered(), s.seedImportRoute(seedXLSXSource))
	t.Handle("POST "+game+"/seed-import/decline", route.Editor, s.scopedSeedDecline)
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
	data, _ := json.Marshal(venues)
	s.eng.BroadcastState(sc.FestID, fmt.Sprintf("venues:%d", sc.FestID), revision, data)
	return route.JSONBytes(w, data)
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

// ---- seed import ----

func (s *server) scopedSeedImportView(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	view, err := imports.LoadSeedImportView(&s.eng, r.Context(), sc.Fest())
	if err != nil {
		return err
	}
	return route.JSON(w, view)
}

// seedImportRoute runs one seed source through imports.ImportSeeds and
// broadcasts the new document; the three POST verbs differ only in the source.
func (s *server) seedImportRoute(source func(*http.Request) (imports.SeedSource, error)) route.Handler {
	return func(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
		src, err := source(r)
		if err != nil {
			return err
		}
		view, revision, stateJSON, err := imports.ImportSeeds(&s.eng, r.Context(), sc.Fest(), src)
		if err != nil {
			return route.BadUser(err)
		}
		s.eng.InvalidateFestViewCache(sc.FestID)
		s.eng.BroadcastState(sc.FestID, gameStateScopeKey(sc.GameID), revision, stateJSON)
		return route.JSON(w, view)
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
	view, revision, stateJSON, err := imports.SetSeedImportDeclined(&s.eng, r.Context(), sc.Fest(), req)
	if err != nil {
		return route.BadUser(err)
	}
	s.eng.BroadcastState(sc.FestID, gameStateScopeKey(sc.GameID), revision, stateJSON)
	return route.JSON(w, view)
}
