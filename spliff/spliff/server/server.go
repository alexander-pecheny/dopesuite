// Package spliffserver is Spliff's HTTP server: the route table, the pages, the
// auth handshake, and the Group / Transaction / Photo API. It is the trunk —
// it wires the mux, the write-transaction discipline and the rate fetcher, and
// imports the leaf groups (domain, storage, platform, web) directly.
package spliffserver

import (
	"context"
	"database/sql"
	"log"
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

const (
	dbFile = "spliff.db"
	// The write-tx discipline Spliff inherits from xy and dope: a write never
	// waits for a pooled connection while holding the lock, and the whole
	// transaction is bounded, so a starved pool can never pin the lock.
	slowWriteThreshold = time.Second
	writeTxTimeout     = 5 * time.Second
)

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

// withWriteTx runs fn in a bounded, serialised write transaction. It pulls a
// pooled connection BEFORE taking the write lock, so pool waits stay off-lock
// and can never pin it, and bounds the whole transaction with writeTxTimeout.
func (s *server) withWriteTx(reqCtx context.Context, label string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), writeTxTimeout)
	defer cancel()

	start := time.Now()
	conn, err := s.db.Conn(ctx)
	if waited := time.Since(start); waited >= slowWriteThreshold {
		log.Printf("slow write %s: pool-wait=%s err=%v", label, waited.Round(time.Millisecond), err)
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	waitStart := time.Now()
	s.mu.Lock()
	acquired := time.Now()
	defer func() {
		hold := time.Since(acquired)
		s.mu.Unlock()
		if wait := acquired.Sub(waitStart); wait >= slowWriteThreshold || hold >= slowWriteThreshold {
			log.Printf("slow write %s: lock-wait=%s lock-hold=%s",
				label, wait.Round(time.Millisecond), hold.Round(time.Millisecond))
		}
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }
