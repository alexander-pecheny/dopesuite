// Package sqlitextest opens SQLite for tests the way sqlitex does, with one
// difference: synchronous is OFF, so a commit never waits for an fsync.
//
// A test database is thrown away when the test ends, so durability buys it
// nothing, and on a network-backed disk the fsyncs were most of the time a
// test spent: one trivial server test took 1.5 s of wall time for 0.4 s of
// CPU. Everything else stays as production has it, WAL included, because
// tests rely on WAL's snapshot reads.
//
// Only test code may import this package. Each app has a guard test that
// fails if a non-test file other than its test seam imports it.
package sqlitextest

import (
	"database/sql"
	"strings"

	"pecheny.me/dopecore/sqlitex"
)

const (
	durable   = "_pragma=synchronous(FULL)"
	undurable = "_pragma=synchronous(OFF)"
)

// DSN is sqlitex.BuildDSN with synchronous switched OFF. It panics if the
// production DSN no longer carries synchronous(FULL), because then this
// package would silently test something other than production.
func DSN(path string) string {
	dsn := sqlitex.BuildDSN(path)
	if !strings.Contains(dsn, durable) {
		panic("sqlitextest: the production DSN no longer carries " + durable + ": " + dsn)
	}
	return strings.Replace(dsn, durable, undurable, 1)
}

// Open is sqlitex.Open without the fsyncs.
func Open(path string, migrate func(*sql.DB) error) (*sql.DB, error) {
	return sqlitex.OpenDSN(DSN(path), migrate)
}
