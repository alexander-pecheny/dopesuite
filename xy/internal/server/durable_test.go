package server

import (
	"database/sql"
	"testing"

	"pecheny.me/dopecore/sqlitex/sqlitextest"
)

// openTestDB opens and migrates a database the way openDB does, but without
// the fsyncs: a test's database is thrown away when the test ends, and on a
// network disk the fsyncs were most of a test's time.
func openTestDB(path string) (*sql.DB, error) { return sqlitextest.Open(path, migrate) }

// Production opens with synchronous(FULL), so an acknowledged write survives a
// crash; only openTestDB skips the fsyncs.
func TestProductionDBIsDurable(t *testing.T) { sqlitextest.RequireDurable(t, openDB) }

// sqlitextest turns the fsyncs off, so no production file may import it.
func TestOnlyTestCodeOpensWithoutFsync(t *testing.T) { sqlitextest.Guard(t, "../..") }
