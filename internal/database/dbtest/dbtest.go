// Package dbtest is how a test outside cmd/qsp gets a real SQLite.
//
// The product registers its driver in one place, cmd/qsp/driver_sqlite.go,
// and that is a decision about the binary. A test in any other package is a
// binary of its own and had no driver, so every database test outside cmd/qsp
// skipped on every machine — the operator's and CI included — while its skip
// message said it ran "where one is". A skip reads as "ok" in a test run.
// A password reset that could never succeed shipped behind that in 0.1.312.
//
// Importing this package registers the driver for the test binary that imports
// it. **Only tests may import it**: TestOnlyTestsImportThisPackage holds that,
// so cmd/qsp stays the one place the product chooses a database.
package dbtest

import (
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/database"

	_ "modernc.org/sqlite"
)

// RequireEnv, when set to anything, turns a missing driver from a skip into a
// failure. scripts/check.sh and CI set it, so a database test that does not
// run there is red rather than quietly green.
const RequireEnv = "QSP_REQUIRE_SQLITE"

type action int

const (
	run action = iota
	skip
	fail
)

// decide is what NeedSQLite does, apart from doing it.
func decide(registered bool, require string) action {
	switch {
	case registered:
		return run
	case require != "":
		return fail
	default:
		return skip
	}
}

// NeedSQLite lets the test continue when a SQLite driver is registered.
// Without one it skips, or fails when RequireEnv is set.
//
// The development container has no driver even with this package imported:
// its module proxy is blocked and a stub stands in for the driver there.
func NeedSQLite(t testing.TB) {
	t.Helper()
	switch decide(database.DriverRegistered("sqlite"), os.Getenv(RequireEnv)) {
	case fail:
		t.Fatalf("no sqlite driver in this build, and %s is set: "+
			"this test must run here and cannot", RequireEnv)
	case skip:
		t.Skipf("no sqlite driver in this build; set %s to make that a failure", RequireEnv)
	}
}
