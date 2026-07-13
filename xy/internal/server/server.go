// Package server is xy's HTTP server: routing, asset serving, auth, and the
// encrypted board/list/card/label/timeline/attachment API. It reuses dope's
// proven infrastructure patterns (SQLite WAL + pragmas, the conn-before-lock
// write-tx discipline, embedded assets with content-hash ETags) without
// importing dope.
package server

import (
	"context"
	"database/sql"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"xy/internal/blobstore"

	"xy/internal/chgk/handout"
)

const (
	dbFile             = "xy.db"
	maxOpenConns       = 8
	slowWriteThreshold = time.Second
	writeTxTimeout     = 5 * time.Second
)

// server wires the DB, the global write lock, and the asset config.
type server struct {
	db    *sql.DB
	blobs *blobstore.Store
	mu    sync.Mutex // global write lock — serializes all write transactions

	assetSource  assetFS
	assetNoCache bool
	assetETags   map[string]string
	stylesheet   []byte // core.css + "\n" + xy layer, served at /static/styles.css

	pageMu    sync.Mutex
	pageCache map[string][]byte // compiled ui/*.dopeui pages (embed mode only; see assets.go)

	staging *handoutStaging // staged handout images (see staging.go)

	// typst, compiled to wasm and run in-process (see typst.go). Built lazily and
	// shared: compiling the module is what costs, not using it. Tests inject a stub
	// so they neither compile the wasm nor need a real image to render.
	typstOnce sync.Once
	typst     handout.Typesetter
	typstErr  error
}

// buildDSN assembles a modernc.org/sqlite DSN with WAL + durability pragmas,
// mirroring dope's storage/store.BuildDSN.
func buildDSN(path string) string {
	pragmas := []string{
		"_pragma=busy_timeout(5000)",
		"_pragma=foreign_keys(1)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(FULL)",
		"_pragma=journal_size_limit(67108864)",
		"_pragma=cache_size(-65536)",
		"_pragma=temp_store(MEMORY)",
	}
	params := strings.Join(pragmas, "&")
	if strings.HasPrefix(path, "file:") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		return path + sep + params
	}
	return "file:" + path + "?" + params
}

// openDB opens the SQLite database, runs migrations on a single connection, then
// opens up the pool.
func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", buildDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns)
	db.SetConnMaxIdleTime(30 * time.Minute)
	return db, nil
}

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

// withWriteTx runs fn in a bounded, serialized write transaction. It pulls a
// pooled connection BEFORE taking the write lock (so pool waits stay off-lock
// and can never pin the lock), bounds the whole tx with writeTxTimeout, then
// commits or rolls back. This is dope's write-tx discipline, ported.
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
