// Package server is xy's HTTP server: routing, asset serving, auth, and the
// encrypted board/list/card/label/timeline/attachment API. It reuses dope's
// proven infrastructure patterns (SQLite WAL + pragmas, the conn-before-lock
// write-tx discipline, embedded assets with content-hash ETags) without
// importing dope.
package server

import (
	"context"
	"database/sql"
	"os"
	"sync"

	_ "modernc.org/sqlite"

	"pecheny.me/dopecore/blobstore"
	"pecheny.me/dopecore/sqlitex"
	"pecheny.me/dopecore/tgbot"
	"pecheny.me/dopecore/webassets"
	kit "pecheny.me/dopeuikit/kit"

	"xy/internal/chgk/handout"
)

const dbFile = "xy.db"

// server wires the DB, the global write lock, and the asset config.
type server struct {
	db    *sql.DB
	blobs *blobstore.Store
	mu    sync.Mutex // global write lock — serializes all write transactions

	assets *webassets.Assets

	pages *kit.PageSet // compiled ui/*.dopeui pages (see assets.go)

	staging *handoutStaging // staged handout images (see staging.go)

	// typst, compiled to wasm and run in-process (see typst.go). Built lazily and
	// shared: compiling the module is what costs, not using it. Tests inject a stub
	// so they neither compile the wasm nor need a real image to render.
	// The login bot, polling in this process (see bot.go). nil on an instance
	// that holds no token — staging, a dev checkout, a second prod binary that
	// lost the race for the poll lock.
	bot *tgbot.Client

	typstOnce sync.Once
	typst     handout.Typesetter
	typstErr  error

	// tgAPIBase is where the telegram export's own bot reaches the Bot API;
	// empty is Telegram's. Tests point it at a stub.
	tgAPIBase string
}

func openDB(path string) (*sql.DB, error) { return sqlitex.Open(path, migrate) }

func newServer() (*server, error) {
	path := os.Getenv("XY_DB")
	if path == "" {
		path = dbFile
	}
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	blobDir := os.Getenv("XY_BLOBS")
	if blobDir == "" {
		blobDir = "blobs"
	}
	blobs, err := blobstore.New(blobDir)
	if err != nil {
		return nil, err
	}
	return &server{db: db, blobs: blobs, staging: newHandoutStaging()}, nil
}

// withWriteTx runs fn in one serialised write transaction under the global
// write lock. The discipline (pool wait off the lock, a bounded transaction, the
// slow-write log) is dopecore's sqlitex.Writer, which dope runs on as well.
func (s *server) withWriteTx(reqCtx context.Context, label string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	return sqlitex.Writer{DB: s.db, Mu: &s.mu}.Tx(reqCtx, label, fn)
}
