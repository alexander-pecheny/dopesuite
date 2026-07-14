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
	"database/sql"
	"net/http"

	"dope/dope/domain/core"
	"dope/dope/domain/view"
	"dope/dope/storage/store"

	"pecheny.me/dopecore/session"
)

// Host is the slice of service-core capabilities the page handlers need. *server
// (package dopeserver) satisfies it via exported accessors (pages_host.go).
type Host interface {
	// DB returns the shared database handle.
	DB() *sql.DB
	// BroadcastFestView invalidates and re-broadcasts a fest/game's FestView
	// after a mutation (used by the journal revert).
	BroadcastFestView(festID, gameID, revision int64)
	// RevertGameToPoint reverts a game's journal to the given entry id and
	// returns the new revision.
	RevertGameToPoint(reqCtx context.Context, festID, gameID, targetID int64) (int64, error)
	// BeginWriteTx begins a bounded write transaction.
	BeginWriteTx(ctx context.Context) (*sql.Tx, error)
	// LookupSession resolves the request's session identity.
	LookupSession(r *http.Request) (session.User, bool)
	// HashPassword hashes a plaintext password for storage.
	HashPassword(password string) (string, error)
	// RequireSameOrigin enforces the CSRF same-origin check on unsafe methods,
	// writing 403 and returning false when it fails.
	RequireSameOrigin(w http.ResponseWriter, r *http.Request) bool
	// StartRegister begins a registration from an invite code.
	StartRegister(ctx context.Context, invite string) (session.StartRegisterResponse, error)
	// FinalizeRegister completes a pending registration, returning the new
	// session token.
	FinalizeRegister(ctx context.Context, code string) (session.RegisterStatusResponse, string, error)
	// WriteExec runs a single audited write statement in an implicit transaction.
	WriteExec(ctx context.Context, query string, args ...any) (sql.Result, error)
	// WithWriteTx runs fn inside a bounded, audited write transaction (conn
	// acquired off-lock, then the global write lock).
	WithWriteTx(reqCtx context.Context, festID int64, label string, fn func(ctx context.Context, tx *sql.Tx) error) error
	// LoadHostFestHeader loads the fest-header view model for the host pages.
	LoadHostFestHeader(ctx context.Context, festID int64) (view.HostFest, error)
	// BroadcastState fans out a full-state snapshot for a fest/scope after a commit.
	BroadcastState(festID int64, scope string, revision int64, payload []byte) uint64
	// WriteJSONValue marshals value and writes it as a JSON response.
	WriteJSONValue(w http.ResponseWriter, value any)
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
	// ServeHostHTMLWithInit serves the EK host.html page with init payload for
	// the given scope and path parts.
	ServeHostHTMLWithInit(w http.ResponseWriter, r *http.Request, scope core.FestScope, parts []string)
}

// Server binds the page handlers to a Host. Construct with New.
type Server struct {
	h Host
}

// New returns a page Server over the given Host.
func New(h Host) *Server { return &Server{h: h} }
