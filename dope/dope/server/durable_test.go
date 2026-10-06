package dopeserver

import (
	"testing"

	"pecheny.me/dopecore/sqlitex/sqlitextest"
)

// Production opens the fest database with synchronous(FULL), so an
// acknowledged write survives a crash. Tests open it through OpenFestDB, which
// skips the fsyncs; this holds the production path to the durable setting.
func TestProductionFestDBIsDurable(t *testing.T) { sqlitextest.RequireDurable(t, openFestDB) }

// sqlitextest turns the fsyncs off, so no production file may import it. The
// test seam, testapi.go, is the one non-test file allowed to.
func TestOnlyTestCodeOpensWithoutFsync(t *testing.T) {
	sqlitextest.Guard(t, "../..", "../../dope/server/testapi.go")
}
