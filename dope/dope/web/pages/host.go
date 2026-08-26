// Package pages holds the server-rendered HTML page handlers (the host/admin/
// public pages). Handlers reach the service core only through the Host interface
// — DB access plus the render/session/write capabilities — so this presentation
// layer never imports the server package. The server constructs a *pages.Server
// by wrapping itself (pages.New(s)) and dispatches into it; pages never dispatches
// back, so there is no import cycle.
//
// This package is being grown incrementally: page handlers move here one cohesive
// file at a time as their service-core dependencies are exposed through Host.
package pages

import (
	"context"
	"net/http"

	"dope/dope/domain/core"
	"dope/dope/domain/view"
	"dope/dope/storage/store"
)

// Host is what the page handlers need of the server: the Engine (the DB, the
// write discipline, sessions, broadcasts — reached directly, not through
// shims) and the few things only the server knows how to do.
type Host interface {
	// BroadcastFestView invalidates and re-broadcasts a fest/game's FestView
	// after a mutation (used by the journal revert).
	BroadcastFestView(festID, gameID, revision int64)
	// RevertGameToPoint reverts a game's journal to the given entry id and
	// returns the new revision.
	RevertGameToPoint(reqCtx context.Context, festID, gameID, targetID int64) (int64, error)
	// LoadHostFestHeader loads the fest-header view model for the host pages.
	LoadHostFestHeader(ctx context.Context, festID int64) (view.HostFest, error)
	// Engine returns the shared server runtime (DB handle, write lock, active
	// fest/game pointers, broadcast and write-tx helpers). The host pages reach
	// the runtime through this single accessor rather than dozens of shims.
	Engine() *core.Engine
	// ResolveGameID resolves a game ref (numeric id or slug) within a fest to
	// its id, returning sql.ErrNoRows when absent.
	ResolveGameID(ctx context.Context, festID int64, ref string) (int64, error)
	// ImportSchemeIntoFest rebuilds a fest's game from a parsed JSON scheme.
	ImportSchemeIntoFest(ctx context.Context, festID int64, scheme store.FestScheme) error
	// LogoutSession invalidates the request's session server-side.
	LogoutSession(r *http.Request)
	// ServeGameHTMLWithInit serves a game page (od/si) HTML with the bootstrap
	// init payload for the given scope.
	ServeGameHTMLWithInit(w http.ResponseWriter, r *http.Request, htmlPath string, scope core.FestScope)
	// ServeEKHTMLWithInit serves a bracket game's page with init payload for
	// the given scope and path parts.
	ServeEKHTMLWithInit(w http.ResponseWriter, r *http.Request, scope core.FestScope, parts []string, page string)
}

// Server binds the page handlers to a Host. Construct with New.
type Server struct {
	h Host
}

// New returns a page Server over the given Host.
func New(h Host) *Server { return &Server{h: h} }
