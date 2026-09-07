package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/database"
	"github.com/k9mls/qsp/internal/logging"
)

// adduser creates or re-passwords an administrator.
//
// **It is the only way an account comes into existence.** ADR-0026 decided
// that: a setup page open until the first account exists is a race an instance
// loses silently, and a default password is the same failure with a longer
// fuse. This needs shell access on the host, which whoever installed QSP has
// and nobody else should — so the web surface never has an unauthenticated path
// that writes anything, at any point in the instance's life.
//
// It runs the migrations, because an operator creating the first account on a
// fresh install has not started the server yet and should not have to.
func adduser(ctx context.Context, cfg config.Config, username string) error {
	if err := auth.ValidateUsername(username); err != nil {
		return err
	}

	// Errors go to stderr like the server's, and nothing here is worth an
	// entry in the operator's journal.
	log := logging.Discard()

	db, err := database.Open(ctx, log, database.Options{
		Driver:          cfg.Database.Driver,
		DSN:             cfg.Database.DSN,
		MaxOpenConns:    cfg.Database.MaxOpenConns,
		ConnMaxLifetime: cfg.Database.ConnMaxLifetime.AsDuration(),
	})
	if err != nil {
		// Refusing here rather than falling back to somewhere writable: an
		// account created in an unexpected database is one the server will
		// never see, and the operator would have no way to tell.
		return fmt.Errorf("cannot open the database this instance uses: %w", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("cannot prepare the database: %w", err)
	}

	repo, err := auth.NewSQLRepository(db.SQL())
	if err != nil {
		return err
	}
	svc, err := auth.NewService(repo, auth.Policy{}, nil)
	if err != nil {
		return err
	}

	// **Checked before the password is asked for.** CreateAccount refuses a
	// duplicate anyway, but by then the operator has typed a password twice to
	// be told the name was taken — which is how this read on the first real
	// run of the command.
	//
	// It is not a substitute for the check inside CreateAccount: two of these
	// running at once would both pass here, and the folded unique index is
	// what actually decides. This exists to fail early and politely, not to
	// fail correctly.
	if _, taken, err := repo.AccountByUsername(ctx, auth.NormaliseUsername(username)); err != nil {
		return err
	} else if taken {
		// Resetting an existing account is the documented recovery path, so it
		// should not read as a mistake — but it is not silently implied
		// either, because typing a name that already exists is just as often a
		// typo.
		return fmt.Errorf("%w: %s (to reset it, remove the account first; "+
			"see docs/adr/ADR-0026-authentication.md)", auth.ErrUsernameTaken, username)
	}

	password, err := readPassword()
	if err != nil {
		return err
	}

	account, err := svc.CreateAccount(ctx, username, password)
	if err != nil {
		if errors.Is(err, auth.ErrUsernameTaken) {
			return fmt.Errorf("%w (created by something else while this was running)", err)
		}
		return err
	}

	fmt.Fprintf(os.Stderr, "Created administrator %q in %s\n", account.Username, cfg.Database.DSN)
	return nil
}

// unlock clears an account's failed attempts.
//
// **Fifteen minutes is a short wait for somebody guessing and a long one for an
// operator who fat-fingered their own passphrase five times.** The recovery
// path is the host, as it is for the password itself, and whoever has shell
// access there is already trusted with more than this.
func unlock(ctx context.Context, cfg config.Config, username string) error {
	if err := auth.ValidateUsername(username); err != nil {
		return err
	}

	db, err := database.Open(ctx, logging.Discard(), database.Options{
		Driver:          cfg.Database.Driver,
		DSN:             cfg.Database.DSN,
		MaxOpenConns:    cfg.Database.MaxOpenConns,
		ConnMaxLifetime: cfg.Database.ConnMaxLifetime.AsDuration(),
	})
	if err != nil {
		return fmt.Errorf("cannot open the database this instance uses: %w", err)
	}
	defer func() { _ = db.Close() }()

	repo, err := auth.NewSQLRepository(db.SQL())
	if err != nil {
		return err
	}
	svc, err := auth.NewService(repo, auth.Policy{}, nil)
	if err != nil {
		return err
	}

	found, err := svc.Unlock(ctx, username)
	if err != nil {
		return err
	}
	if !found {
		// Named rather than silently succeeding: unlocking a typo would
		// otherwise report success and leave the real account still locked.
		return fmt.Errorf("no administrator named %q", username)
	}

	fmt.Fprintf(os.Stderr, "Unlocked %q\n", username)
	return nil
}

// readPassword prompts twice without echoing.
//
// Twice, because a mistyped password on an account nobody has logged into yet
// is discovered at the worst possible moment and the shell is the only place it
// can be fixed. Without echo, because the alternative leaves it in a terminal
// scrollback and, on a shared machine, on the screen.
//
// **Echo is turned off with a terminal ioctl**, in this process — see
// hideinput_linux.go. It used to run `stty -echo`, which worked everywhere the
// author had tried it and nowhere the container runs: a `scratch` image holds
// one static binary and has no stty, no shell and no /bin at all, so the first
// thing an operator did after a successful install was fail to create an
// account.
//
// Turning echo off is also the terminal check, and a better one than inspecting
// the mode of standard input: /dev/null is a character device too, so a password
// piped from it would have looked like a person typing. The ioctl fails with
// ENOTTY on anything that is not a terminal, which is exactly the question being
// asked.
func readPassword() (string, error) {
	restore, err := hideInput()
	if err != nil {
		return "", err
	}
	// Restored on every path, including a signal that kills the read, or the
	// operator is left with a shell that does not echo anything.
	defer restore()

	first, err := promptOnce("Password: ")
	if err != nil {
		return "", err
	}
	second, err := promptOnce("Repeat: ")
	if err != nil {
		return "", err
	}

	if first != second {
		return "", errors.New("the two passwords do not match")
	}
	// Checked here as well as in CreateAccount, so that a password which is
	// too short is refused before the database is touched.
	if err := auth.ValidatePassword(first); err != nil {
		return "", err
	}
	return first, nil
}

func promptOnce(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("cannot read the password: %w", err)
	}
	// Only the line ending is trimmed. A password is allowed to begin or end
	// with a space, and quietly removing one would make it unenterable later.
	return strings.TrimRight(line, "\r\n"), nil
}
