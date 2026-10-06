package spliffserver

import (
	"testing"

	"pecheny.me/dopecore/sqlitex/sqlitextest"
)

// Production opens with synchronous(FULL), so an acknowledged write survives a
// crash; only the test seam skips the fsyncs.
func TestProductionDBIsDurable(t *testing.T) { sqlitextest.RequireDurable(t, openDB) }

// sqlitextest turns the fsyncs off, so no production file may import it. The
// test seam, testapi.go, is the one non-test file allowed to.
func TestOnlyTestCodeOpensWithoutFsync(t *testing.T) {
	sqlitextest.Guard(t, "../..", "../../spliff/server/testapi.go")
}
