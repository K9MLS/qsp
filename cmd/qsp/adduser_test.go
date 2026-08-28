package main

import (
	"context"
	"os"
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

// TestATakenNameIsRefusedBeforeThePasswordIsAsked. Found on the first real run
// of the command: it prompted twice and then said the name was taken, which is
// the wrong order to discover that in.
//
// The check cannot be reached here without a database, so what is asserted is
// the order in the source — crude, and it catches the regression that matters,
// which is somebody moving the prompt back above the lookup.
func TestATakenNameIsRefusedBeforeThePasswordIsAsked(t *testing.T) {
	body, err := os.ReadFile("adduser.go")
	if err != nil {
		t.Fatalf("reading adduser.go: %v", err)
	}
	src := string(body)

	lookup := strings.Index(src, "repo.AccountByUsername(ctx")
	prompt := strings.Index(src, "readPassword()")
	if lookup < 0 {
		t.Fatal("adduser no longer looks the username up before creating it")
	}
	if prompt < 0 {
		t.Fatal("adduser no longer prompts for a password")
	}
	if lookup > prompt {
		t.Error("the password is asked for before the username is checked; an operator " +
			"types it twice to be told the name was taken")
	}
}
