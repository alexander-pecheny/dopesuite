// Package spliffserver is Spliff's HTTP server: the route table, the pages, the
// auth handshake, and the Group / Transaction / Photo API. It is the trunk —
// it wires the mux, the write-transaction discipline and the rate fetcher, and
// imports the leaf groups (domain, storage, platform, web) directly.
package spliffserver

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"pecheny.me/dopecore/blobstore"
	"pecheny.me/dopecore/sqlitex"
	"pecheny.me/dopecore/tgbot"
	"pecheny.me/dopecore/webassets"
	kit "pecheny.me/dopeuikit/kit"

	"spliff/spliff/storage/store"
)

const dbFile = "spliff.db"

// server wires the database, the blob store, the global write lock, the assets
// and the login bot.
type server struct {
	db    *sql.DB
	blobs *blobstore.Store
	mu    sync.Mutex // global write lock — serialises every write transaction

	assets *webassets.Assets
	pages  *kit.PageSet

	// The login bot, polling in this process (root ADR-0005). nil on an
	// instance holding no token — staging, a dev checkout, or a second binary
	// that lost the race for the poll lock.
	bot *tgbot.Client

	// rates is the Rate table service: the fetch-on-first-need, the daily
	// ticker, and the read the ledger goes through.
	rates *rateService
}

func openDB(path string) (*sql.DB, error) { return sqlitex.Open(path, store.Migrate) }

func newServer() (*server, error) {
	path := os.Getenv("SPLIFF_DB")
	if path == "" {
		path = dbFile
	}
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	blobDir := os.Getenv("SPLIFF_BLOBS")
	if blobDir == "" {
		blobDir = "blobs"
	}
	blobs, err := blobstore.New(blobDir)
	if err != nil {
		return nil, err
	}
	s := &server{db: db, blobs: blobs}
	s.rates = newRateService(s, nil)
	return s, nil
}

// withWriteTx runs fn in one serialised write transaction under the global
// write lock. The discipline (pool wait off the lock, a bounded transaction, the
// slow-write log) is dopecore's sqlitex.Writer, which dope runs on as well.
func (s *server) withWriteTx(reqCtx context.Context, label string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	return sqlitex.Writer{DB: s.db, Mu: &s.mu}.Tx(reqCtx, label, fn)
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }
