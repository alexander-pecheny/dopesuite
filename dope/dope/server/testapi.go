package dopeserver

// This file is the ONLY test-support surface of package dopeserver. It exists
// solely so the black-box test package (dope/dope/tests) — which cannot reach
// unexported identifiers — can construct a *Server, read/poke a few fields, and
// drive otherwise-unexported handlers/methods/funcs. Nothing here is used in
// production code; keeping it all in one file documents the full test seam.

import (
	"context"
	"io/fs"
	"net/http"

	"pecheny.me/dopecore/authcred"

	"dope/dope/domain/core"
	"dope/dope/platform/metrics"
	"dope/dope/storage/store"
	"dope/dope/web/assets"
	"dope/dope/web/hostpages"
	"dope/dope/web/pages"
	"dope/dope/web/telegrambridge"
)

// Server is the exported alias of the unexported server type so external tests
// can name it (e.g. *dopeserver.Server). Methods on *server are reachable.
type Server = server

// NewTestServer builds a Server the way tests used to inline (&server{eng: ...}).
// The engine is configured in place (so its embedded sync.RWMutex is never
// copied by value); pass nil for a zero engine. The metrics recorder and edit
// batcher initialize lazily on first use.
func NewTestServer(configure func(*core.Engine)) *Server {
	s := &Server{}
	if configure != nil {
		configure(&s.eng)
	}
	return s
}

// ----- field accessors -----

// Eng returns a pointer to the embedded engine (tests both read it and pass its
// address into leaf packages, e.g. imports.ImportSeedsFromKSI(srv.Eng(), ...)).
func (s *Server) Eng() *core.Engine { return &s.eng }

// Metrics returns a pointer to the edit-metrics recorder.
func (s *Server) Metrics() *metrics.Recorder { return &s.metrics }

// ----- exported type aliases (request/response + scope types) -----

type (
	FestScope          = festScope
	MatchScope         = matchScope
	UpdateRequest      = updateRequest
	MeResponse         = meResponse
	PasswordRequest    = passwordRequest
	UsernameRequest    = usernameRequest
	VenueUpdateRequest = venueUpdateRequest
)

// ----- exported const aliases -----

const (
	DefaultMatchCode          = defaultMatchCode
	DefaultGameCode           = defaultGameCode
	DefaultVenueTitle         = defaultVenueTitle
	ActionAddShootoutTheme    = actionAddShootoutTheme
	ActionRemoveShootoutTheme = actionRemoveShootoutTheme
	TrustedOriginHostsEnv     = trustedOriginHostsEnv
)

// ----- exported func/var aliases (free funcs + package vars) -----

var (
	StaticFiles = assets.FS

	OpenFestDB                = openFestDB
	MigrateDB                 = migrateDB
	ResolveGameID             = resolveGameID
	DefaultGameID             = defaultGameID
	DefaultMatch              = defaultMatch
	BackfillEKTeamNumbers     = backfillEKTeamNumbers
	BackfillFestTeamNumbers   = backfillFestTeamNumbers
	EnsureSystemUser          = ensureSystemUser
	RecalculateMatchResultsTx = recalculateMatchResultsTx
	SplitPlayerName           = splitPlayerName
	ApplyMatchEditTx          = applyMatchEditTx
	CreateInvite              = createInvite
	CreateSessionTx           = createSessionTx
	HashPassword              = authcred.HashPassword
	LegacySHA256Password      = authcred.LegacySHA256Password
	LoadActiveContext         = loadActiveContext
	NewInviteCode             = authcred.NewInviteCode
	NewSessionToken           = authcred.NewSessionToken
	SqliteTableExists         = sqliteTableExists
	VerifyPassword            = authcred.VerifyPasswordUpgrading
	EnvInt64                  = envInt64
	Contains                  = contains
)

// ----- exported method wrappers (one per unexported method tests invoke) -----

func (s *Server) ApplyMatchUpdate(festID int64, code string, req UpdateRequest) (store.MatchView, []byte, error) {
	return s.applyMatchUpdate(festID, code, req)
}

func (s *Server) ApplyScopedMatchUpdate(ctx context.Context, scope MatchScope, reqs []UpdateRequest) (store.MatchView, []byte, []byte, []store.MatchView, error) {
	return s.applyScopedMatchUpdate(ctx, scope, reqs)
}

func (s *Server) ApplyUpdate(req UpdateRequest) (store.MatchView, []byte, error) {
	return s.applyUpdate(req)
}

func (s *Server) CalculateScopedReseed(ctx context.Context, scope FestScope, stageCode string) ([]byte, []store.MatchView, int64, error) {
	return s.calculateScopedReseed(ctx, scope, stageCode)
}

func (s *Server) HandleAuthLoginPassword(w http.ResponseWriter, r *http.Request) {
	s.handleAuthLoginPassword(w, r)
}
func (s *Server) HandleAuthLogout(w http.ResponseWriter, r *http.Request) { s.handleAuthLogout(w, r) }
func (s *Server) HandleAuthMe(w http.ResponseWriter, r *http.Request)     { s.handleAuthMe(w, r) }
func (s *Server) HandleAuthPassword(w http.ResponseWriter, r *http.Request) {
	s.handleAuthPassword(w, r)
}
func (s *Server) HandleAuthTgStart(w http.ResponseWriter, r *http.Request) {
	s.handleAuthTgStart(w, r)
}
func (s *Server) HandleAuthTgStatus(w http.ResponseWriter, r *http.Request) {
	s.handleAuthTgStatus(w, r)
}
func (s *Server) HandleAuthTgClaim(w http.ResponseWriter, r *http.Request) {
	s.handleAuthTgClaim(w, r)
}
func (s *Server) HandleAuthUsername(w http.ResponseWriter, r *http.Request) {
	s.handleAuthUsername(w, r)
}
func (s *Server) HandleEvents(w http.ResponseWriter, r *http.Request)     { s.handleEvents(w, r) }
func (s *Server) HandleFestRouter(w http.ResponseWriter, r *http.Request) { s.handleFestRouter(w, r) }
func (s *Server) HandleImport(w http.ResponseWriter, r *http.Request)     { s.handleImport(w, r) }
func (s *Server) HandleScopedAPI(w http.ResponseWriter, r *http.Request)  { s.handleScopedAPI(w, r) }

func (s *Server) ImportScheme(scheme store.FestScheme) (store.FestView, error) {
	return s.importScheme(scheme)
}

func (s *Server) LoadFestViewLocked(festID, gameID int64) (store.FestView, error) {
	return s.loadFestViewLocked(festID, gameID)
}
func (s *Server) LoadFestViewSnapshot(festID, gameID int64) (store.FestView, error) {
	return s.loadFestViewSnapshot(festID, gameID)
}
func (s *Server) LoadMatchViewLocked(festID int64, code string) (store.MatchView, error) {
	return s.loadMatchViewLocked(festID, code)
}
func (s *Server) LoadScopedMatchViewLocked(scope MatchScope) (store.MatchView, error) {
	return s.loadScopedMatchViewLocked(scope)
}
func (s *Server) LoadScopedMatchViewSnapshot(scope MatchScope) (store.MatchView, error) {
	return s.loadScopedMatchViewSnapshot(scope)
}

func (s *Server) PageServer() *pages.Server { return s.pageServer() }

func (s *Server) HostPageServer() *hostpages.Server { return s.hostPageServer() }

func (s *Server) ServeStaticPage(source fs.FS, path string) http.HandlerFunc {
	return s.serveStaticPage(source, path)
}

func (s *Server) TgBridge() *telegrambridge.Server { return s.tgBridge() }

func (s *Server) UpdateVenue(reqCtx context.Context, festID int64, number int, title string) ([]store.VenueView, int64, error) {
	return s.updateVenue(reqCtx, festID, number, title)
}

func (s *Server) VerifyMatchInScope(ctx context.Context, scope FestScope, code string) (MatchScope, error) {
	return s.verifyMatchInScope(ctx, scope, code)
}

func (s *Server) VersionAssetRefs(body []byte) []byte { return s.versionAssetRefs(body) }
