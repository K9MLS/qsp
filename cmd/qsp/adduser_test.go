package main

import (
	"context"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// adduser cannot be tested end to end here: it wants a terminal for the
// password and a database for the row. What is testable is that it refuses
// before reaching either, which is where the mistakes an operator makes live.

func TestAdduserRefusesABadUsernameBeforeTouchingAnything(t *testing.T) {
	cfg := config.Default()
	// A DSN that could not be opened, so a test that reached the database
	// would fail for that reason instead and the check would prove nothing.
	cfg.Database.DSN = "/proc/definitely-not-a-database"

	for _, name := range []string{"", "   ", "has space", "drop;table", strings.Repeat("a", 65)} {
		err := adduser(context.Background(), cfg, name)
		if err == nil {
			t.Errorf("username %q was accepted", name)
			continue
		}
		// The username check must come first. If the database error surfaces
		// instead, the order is wrong and an operator with a typo gets a
		// confusing message about storage.
		if strings.Contains(err.Error(), "database") {
			t.Errorf("username %q reached the database before being validated: %v", name, err)
		}
	}
}

// TestAdduserRefusesAnUnopenableDatabase covers the rule that matters more than
// it looks: an account created in an unexpected database is one the server will
// never see, and the operator has no way to tell.
func TestAdduserRefusesAnUnopenableDatabase(t *testing.T) {
	cfg := config.Default()
	cfg.Database.Driver = "no-such-driver"

	err := adduser(context.Background(), cfg, "K9MLS")
	if err == nil {
		t.Fatal("adduser succeeded with no usable database")
	}
	if !strings.Contains(err.Error(), "database") {
		t.Errorf("the error should name the database: %v", err)
	}
}

// TestPasswordsAreNotReadFromAPipe. A password taken from stdin appears in
// whatever produced it: a shell history, a script, a CI log.
func TestPasswordsAreNotReadFromAPipe(t *testing.T) {
	// Under `go test` stdin is a pipe, which is the condition being checked,
	// so this exercises the real path rather than a stub.
	//
	// The first version of this test asked os.Stdin.Stat() whether it was a
	// character device and skipped when it was — but /dev/null is one too, so
	// it would have skipped on exactly the input it exists to refuse. stty is
	// the accurate check and is now the only one.
	if _, err := readPassword(); err == nil {
		t.Fatal("a password was read from something that is not a terminal")
	} else if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("the refusal should say why: %v", err)
	}
}
