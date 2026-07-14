// Package sqlitex holds the SQLite pool conventions both apps run on: the
// pragma set every connection ships with, and the open-migrate-then-widen
// bootstrap.
package sqlitex

import (
	"database/sql"
	"strings"
	"time"
)

// MaxOpenConns sizes the read connection pool. SQLite under WAL handles many
// concurrent readers against a single writer; this lets read traffic proceed in
// parallel with writes.
const MaxOpenConns = 8

// pragmas ship with every new pool connection. journal_mode is database-wide and
// only takes effect once, but resetting it per connection is harmless and lets a
// freshly-deleted/recreated DB land in WAL without a separate Exec.
//
// synchronous=FULL fsyncs the WAL on every commit, so an acknowledged (200 OK)
// write can never be rolled back by a crash or restart — WAL + NORMAL only
// guarantees durability across an app crash in theory, and a crash-loop once
// silently reverted ~3 min of committed edits to the last checkpoint. Measured
// cost on prod's disk is ~0.8 ms/commit vs an observed peak of ~10 writes/s.
//
// journal_size_limit caps the WAL so it is truncated back down after a
// checkpoint instead of growing without bound (prod's WAL had once ballooned
// past 500 MB). 64 MB comfortably spans a write burst.
var pragmas = []string{
	"_pragma=busy_timeout(5000)",
	"_pragma=foreign_keys(1)",
	"_pragma=journal_mode(WAL)",
	"_pragma=synchronous(FULL)",
	"_pragma=journal_size_limit(67108864)",
	"_pragma=cache_size(-65536)",
	"_pragma=temp_store(MEMORY)",
}

// BuildDSN turns a bare file path into a modernc.org/sqlite URI carrying the
// pragmas above.
func BuildDSN(path string) string {
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

// Open opens the database pinned to a single connection, runs migrate on it,
// then widens the pool. Migrating on one connection keeps concurrent schema
// changes impossible; widening afterwards restores read concurrency.
func Open(path string, migrate func(*sql.DB) error) (*sql.DB, error) {
	db, err := sql.Open("sqlite", BuildDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if migrate != nil {
		if err := migrate(db); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	db.SetMaxOpenConns(MaxOpenConns)
	db.SetMaxIdleConns(MaxOpenConns)
	db.SetConnMaxIdleTime(30 * time.Minute)
	return db, nil
}
