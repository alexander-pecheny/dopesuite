// Package gameexport renders a single game's tables for download — the computed
// results view (JSON), the XLSX archive, and the gzipped JSON game archive.
//
// It depends on the server only through the narrow Host interface (DB access,
// read authorization, and a couple of view helpers), so it never imports the
// server package: the server constructs the handlers by passing itself as a
// Host. This keeps the export logic a leaf the core can dispatch into without an
// import cycle.
package gameexport

import (
	"context"
	"database/sql"

	"dope/dope/storage/store"
)

// Host is the slice of server capabilities the export handlers need. The server
// satisfies it via thin exported accessor methods (see gameexport_host.go in the
// dopeserver package).
type Host interface {
	// DB returns the shared database handle.
	DB() *sql.DB
	// Epoch returns the per-process SSE epoch token stamped on state responses.
	Epoch() string
	// CurrentStateSeq returns the current per-scope SSE sequence number.
	CurrentStateSeq(scope string) uint64
	// LoadAllStageMatchViews returns every stage's match views for an EK game.
	LoadAllStageMatchViews(ctx context.Context, festID, gameID int64) ([]store.StageMatches, error)
}
