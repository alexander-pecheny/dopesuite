package dopeserver

import (
	"context"
	"database/sql"
	"net/http"

	"pecheny.me/dopecore/authcred"

	"dope/dope/domain/core"
	"dope/dope/domain/view"
	"dope/dope/export/gameexport"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	"dope/dope/web/hostpages"
	"dope/dope/web/pages"
	"dope/dope/web/telegrambridge"

	"pecheny.me/dopecore/session"
)

// host_accessors.go consolidates the thin exported accessors that adapt *server
// to the leaf/handler packages' Host interfaces (gameexport, pages,
// telegrambridge). Keeping them in one place documents the full server→package
// surface and keeps the per-package wiring out of the core logic files.

// gameexport_host.go adapts *server to the gameexport.Host interface: the export
// handlers live in the leaf gameexport package and reach back into the server
// only through these thin, exported accessors. Keeping them here (rather than
// exporting the underlying fields/methods) preserves the server's encapsulation
// while letting the export logic be a cycle-free leaf.

var _ gameexport.Host = (*server)(nil)

// DB returns the shared database handle.
func (s *server) DB() *sql.DB { return s.eng.DB }

// Epoch returns the per-process SSE epoch token.
func (s *server) Epoch() string { return s.eng.Epoch }

// AuthorizeFestRead writes an error response and returns false unless the caller
// may read the fest.
func (s *server) AuthorizeFestRead(w http.ResponseWriter, r *http.Request, festID int64) bool {
	return s.authorizeFestRead(w, r, festID)
}

// RequireFestTableEditor writes an error response and returns false unless the
// caller holds the table-editor role on the fest.
func (s *server) RequireFestTableEditor(w http.ResponseWriter, r *http.Request, festID int64) bool {
	_, ok := s.requireFestTableEditor(w, r, festID)
	return ok
}

// CurrentStateSeq returns the current per-scope SSE sequence number.
func (s *server) CurrentStateSeq(scope string) uint64 { return s.eng.CurrentStateSeq(scope) }

// LoadAllStageMatchViews returns every stage's match views for an EK game.
func (s *server) LoadAllStageMatchViews(ctx context.Context, festID, gameID int64) ([]store.StageMatches, error) {
	return s.loadAllStageMatchViews(ctx, festScope{FestID: festID, GameID: gameID})
}

// pages_accessor.go wires *server to the pages package (the server-rendered HTML
// handlers). *server already exposes DB() (see gameexport_host.go); this assertion
// pins the satisfaction and the pageServer below gives the rest of the server a
// single constructed handle to dispatch page renders through.
var _ pages.Host = (*server)(nil)

// pageServer returns a pages.Server bound to this server, for dispatching into
// the extracted page handlers.
func (s *server) pageServer() *pages.Server { return pages.New(s) }

// hostPageServer returns a hostpages.Server bound to this server, for
// dispatching into the extracted host-UI page handlers. *server satisfies
// pages.Host (see the assertion above), which hostpages reuses.
func (s *server) hostPageServer() *hostpages.Server { return hostpages.New(s) }

// BroadcastFestView invalidates and re-broadcasts a fest/game's FestView.
func (s *server) BroadcastFestView(festID, gameID, revision int64) {
	s.broadcastFestView(festScope{FestID: festID, GameID: gameID}, revision)
}

// RevertGameToPoint reverts a game's journal to the given entry id.
func (s *server) RevertGameToPoint(reqCtx context.Context, festID, gameID, targetID int64) (int64, error) {
	return s.eng.RevertGameToPoint(reqCtx, festID, gameID, targetID)
}

// BeginWriteTx begins a bounded write transaction.
func (s *server) BeginWriteTx(ctx context.Context) (*sql.Tx, error) { return s.eng.BeginWriteTx(ctx) }

// LookupSession resolves the request's session identity.
func (s *server) LookupSession(r *http.Request) (session.User, bool) { return s.eng.LookupSession(r) }

// HashPassword hashes a plaintext password for storage.
func (s *server) HashPassword(password string) (string, error) {
	return authcred.HashPassword(password)
}

// RequireSameOrigin enforces the CSRF same-origin check on unsafe methods.
func (s *server) RequireSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	return RequireSameOriginUnsafe(w, r)
}

// WriteExec runs a single audited write statement in an implicit transaction.
func (s *server) WriteExec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.eng.WriteExec(ctx, query, args...)
}

// WithWriteTx runs fn inside a bounded, audited write transaction.
func (s *server) WithWriteTx(reqCtx context.Context, festID int64, label string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	return s.eng.WithWriteTx(reqCtx, festID, label, fn)
}

// LoadHostFestHeader loads the fest-header view model for the host pages.
func (s *server) LoadHostFestHeader(ctx context.Context, festID int64) (view.HostFest, error) {
	return s.loadHostFestHeader(ctx, festID)
}

// BroadcastState fans out a full-state snapshot for a fest/scope after a commit.
func (s *server) BroadcastState(festID int64, scope string, revision int64, payload []byte) uint64 {
	return s.eng.BroadcastState(festID, scope, revision, payload)
}

// WriteJSONValue marshals value and writes it as a JSON response.
func (s *server) WriteJSONValue(w http.ResponseWriter, value any) { writeJSONValue(w, value) }

// Engine returns the shared server runtime for the page handlers.
func (s *server) Engine() *core.Engine { return &s.eng }

// ResolveGameID resolves a game ref (id or slug) within a fest to its id.
func (s *server) ResolveGameID(ctx context.Context, festID int64, ref string) (int64, error) {
	return resolveGameID(ctx, s.eng.DB, festID, ref)
}

// ImportSchemeIntoFest rebuilds a fest's game from a parsed JSON scheme.
func (s *server) ImportSchemeIntoFest(ctx context.Context, festID int64, scheme store.FestScheme) error {
	return s.importSchemeIntoFest(ctx, festID, scheme)
}

// LogoutSession invalidates the request's session server-side.
func (s *server) LogoutSession(r *http.Request) { s.logoutSession(r) }

// ServeGameHTMLWithInit serves a game page (od/si) HTML with init payload.
func (s *server) ServeGameHTMLWithInit(w http.ResponseWriter, r *http.Request, htmlPath string, scope core.FestScope) {
	s.serveGameHTMLWithInit(w, r, htmlPath, scope)
}

// ServeHostHTMLWithInit serves the EK host.html page with init payload.
func (s *server) ServeHostHTMLWithInit(w http.ResponseWriter, r *http.Request, scope core.FestScope, parts []string) {
	s.serveHostHTMLWithInit(w, r, scope, parts)
}

// loadHostFestHeader loads the fest-header view model for the host pages. It
// lives here (rather than in the moved pages cluster) so the LoadHostFestHeader
// shim above can keep delegating to it after the host UI handlers moved into the
// pages package.
func (s *server) loadHostFestHeader(ctx context.Context, festID int64) (view.HostFest, error) {
	var t view.HostFest
	var pub int
	if err := s.eng.DB.QueryRowContext(ctx, `
select id, coalesce(slug, ''), title, coalesce(start_date, ''), coalesce(end_date, ''), is_public
from fests where id = ?`, festID).Scan(&t.ID, &t.Slug, &t.Title, &t.StartDate, &t.EndDate, &pub); err != nil {
		return view.HostFest{}, err
	}
	t.IsPublic = pub == 1
	t.Dates = util.FormatFestDates(t.StartDate, t.EndDate)
	return t, nil
}

// telegrambridge_host.go adapts *server to telegrambridge.Host. DB and
// WriteJSONValue are already provided (pages accessors); the rest are thin
// wrappers over the write lock, the bot secret and login-code generation.
var _ telegrambridge.Host = (*server)(nil)

// tgBridge returns a telegrambridge.Server bound to this server.
func (s *server) tgBridge() *telegrambridge.Server { return telegrambridge.New(s) }

// Lock / Unlock expose the global write mutex.
func (s *server) Lock()   { s.eng.Mu.Lock() }
func (s *server) Unlock() { s.eng.Mu.Unlock() }

// BotSecret returns the configured Telegram bridge shared secret.
func (s *server) BotSecret() string { return s.eng.BotSecret }
