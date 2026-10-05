package auth_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/database"
)

// The SQL adapter cannot be executed in this container: it registers no driver,
// by the design ADR-0005 chose. That leaves the statements themselves unrun,
// which is exactly where a typo hides — `last_login` for `last_login_at` reads
// perfectly and fails only when somebody logs in.
//
// So the statements are checked against the schema instead. It is not the same
// as running them and it catches the error most likely to be here.

// TestSQLRepositorySatisfiesRepository is a compile-time check written as a
// test, because the wiring that would otherwise catch it lives in cmd/qsp,
// which cannot be built here either.
func TestSQLRepositorySatisfiesRepository(t *testing.T) {
	var _ auth.Repository = (*auth.SQLRepository)(nil)
}

func TestNewSQLRepositoryRequiresAHandle(t *testing.T) {
	if _, err := auth.NewSQLRepository(nil); err == nil {
		t.Error("a repository was built with no database handle")
	}
}

// schemaColumns reads the column names the migration actually creates.
func schemaColumns(t *testing.T) map[string]map[string]bool {
	t.Helper()

	path := filepath.Join("..", "..", "migrations", "0003_users.sql")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	tables := make(map[string]map[string]bool)
	create := regexp.MustCompile(`(?is)CREATE TABLE (\w+)\s*\((.*?)\n\);`)
	for _, m := range create.FindAllStringSubmatch(string(body), -1) {
		cols := make(map[string]bool)
		for _, line := range strings.Split(m[2], "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "--") {
				continue
			}
			name := strings.Fields(line)[0]
			// Skip table-level clauses, which start with a keyword rather than
			// a column name.
			switch strings.ToUpper(name) {
			case "PRIMARY", "UNIQUE", "FOREIGN", "CHECK", "CONSTRAINT":
				continue
			}
			cols[strings.Trim(name, "`\"")] = true
		}
		tables[m[1]] = cols
	}

	if len(tables) != 2 {
		t.Fatalf("parsed %d tables from the migration, want users and sessions; "+
			"the schema's shape has changed and this check is now blind", len(tables))
	}
	return tables
}

// TestEveryColumnTheSQLNamesExists is the check that earns its place. A column
// that does not exist compiles, lints, and fails at the first login.
func TestEveryColumnTheSQLNamesExists(t *testing.T) {
	tables := schemaColumns(t)

	known := make(map[string]bool)
	for _, cols := range tables {
		for col := range cols {
			known[col] = true
		}
	}
	// Table aliases and SQL keywords the extraction will also see.
	ignore := map[string]bool{
		"s": true, "u": true, "users": true, "sessions": true,
		"set": true, "from": true, "where": true, "select": true, "values": true,
	}

	body, err := os.ReadFile(filepath.Join("sqlrepo.go"))
	if err != nil {
		t.Fatalf("reading sqlrepo.go: %v", err)
	}

	// Every identifier inside a backquoted SQL literal that looks like a
	// snake_case column name. Anything with an underscore is a column in this
	// schema and nothing else is.
	sqlLiteral := regexp.MustCompile("(?s)`([^`]*(?:SELECT|INSERT|UPDATE|DELETE)[^`]*)`")
	ident := regexp.MustCompile(`\b([a-z]+_[a-z_]+)\b`)

	var checked int
	for _, lit := range sqlLiteral.FindAllStringSubmatch(string(body), -1) {
		for _, m := range ident.FindAllStringSubmatch(lit[1], -1) {
			name := m[1]
			if ignore[name] {
				continue
			}
			checked++
			if !known[name] {
				t.Errorf("the SQL names column %q, which 0003_users.sql does not create", name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no column names were found in the SQL; this check is not checking anything")
	}
	t.Logf("checked %d column references against the schema", checked)
}

// TestPlaceholdersMatchTheColumnsInserted catches the other silent error: an
// INSERT whose value list is a different length from its column list, which is
// a runtime failure and not a compile one.
func TestPlaceholdersMatchTheColumnsInserted(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("sqlrepo.go"))
	if err != nil {
		t.Fatalf("reading sqlrepo.go: %v", err)
	}

	insert := regexp.MustCompile(`(?is)INSERT INTO \w+ \(([^)]*)\)\s*VALUES \(([^)]*)\)`)
	matches := insert.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		t.Fatal("no INSERT statements were found; this check is not checking anything")
	}

	for _, m := range matches {
		columns := len(strings.Split(m[1], ","))
		placeholders := strings.Count(m[2], "?")
		if columns != placeholders {
			t.Errorf("an INSERT names %d columns and supplies %d values:\n  (%s)\n  VALUES (%s)",
				columns, placeholders, strings.TrimSpace(m[1]), strings.TrimSpace(m[2]))
		}
	}
}

// notNullColumns reads which columns the migration declares NOT NULL.
func notNullColumns(t *testing.T) map[string]bool {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0003_users.sql"))
	if err != nil {
		t.Fatalf("reading the migration: %v", err)
	}
	decl := regexp.MustCompile(`(?m)^\s+(\w+)\s+(?:TEXT|INTEGER)\b[^\n]*\bNOT NULL\b`)
	out := make(map[string]bool)
	for _, m := range decl.FindAllStringSubmatch(string(body), -1) {
		out[m[1]] = true
	}
	return out
}

// TestNoStatementWritesNULLToAColumnThatRefusesIt is the check a password
// reset needed. `locked_until = NULL` named a real column, so every other
// check here passed, and SQLite refused the update the first time an operator
// reset a password — taking the new password with it.
//
// It reads `column = NULL` in the statements and nothing cleverer: a NULL
// arriving through a placeholder is beyond it, and is what the test below is
// for.
//
// Break it: write `locked_until = NULL` in SetPassword.
func TestNoStatementWritesNULLToAColumnThatRefusesIt(t *testing.T) {
	notNull := notNullColumns(t)
	for _, col := range []string{"locked_until", "last_login_at", "failed_count", "password_hash"} {
		if !notNull[col] {
			t.Fatalf("%q was not read as NOT NULL from the migration; "+
				"the schema's shape has changed and this check is now blind", col)
		}
	}

	body, err := os.ReadFile("sqlrepo.go")
	if err != nil {
		t.Fatalf("reading sqlrepo.go: %v", err)
	}
	sqlLiteral := regexp.MustCompile("(?s)`([^`]*(?:SELECT|INSERT|UPDATE|DELETE)[^`]*)`")
	assign := regexp.MustCompile(`(?i)\b(\w+)\s*=\s*(NULL\b|'[^']*'|\?|\w+)`)

	var checked int
	for _, lit := range sqlLiteral.FindAllStringSubmatch(string(body), -1) {
		for _, m := range assign.FindAllStringSubmatch(lit[1], -1) {
			if !notNull[m[1]] {
				continue
			}
			checked++
			if strings.EqualFold(m[2], "NULL") {
				t.Errorf("a statement writes NULL to %q, which 0003_users.sql declares NOT NULL", m[1])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no assignment to a NOT NULL column was found in the SQL; this check is not checking anything")
	}
}

// TestAResetReachesARealDatabase runs the reset where a SQLite driver is built
// in, which the project's own check script is. The service's reset test uses a
// repository with no schema, which is how this went unseen.
//
// Break it: write `locked_until = NULL` in SetPassword, or drop
// `failed_count = 0` from it.
func TestAResetReachesARealDatabase(t *testing.T) {
	if !database.DriverRegistered("sqlite") {
		t.Skip("no sqlite driver in this build; these tests run where one is")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, nil, database.Options{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "qsp.db"),
	})
	if err != nil {
		t.Fatalf("opening a database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	repo, err := auth.NewSQLRepository(db.SQL())
	if err != nil {
		t.Fatalf("NewSQLRepository: %v", err)
	}

	now := time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		failed int
		locked time.Time
	}{
		{name: "AD0MI", failed: 0},
		{name: "KD9EJA", failed: 5, locked: now.Add(15 * time.Minute)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fold := strings.ToLower(tc.name)
			made, err := repo.CreateAccount(ctx, auth.Account{
				Username: tc.name, PasswordHash: "the-old-hash", CreatedAt: now,
			}, fold)
			if err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}
			if err := repo.UpdateAttempts(ctx, made.ID, tc.failed, tc.locked, time.Time{}); err != nil {
				t.Fatalf("UpdateAttempts: %v", err)
			}

			if err := repo.SetPassword(ctx, made.ID, "the-new-hash"); err != nil {
				t.Fatalf("SetPassword: %v", err)
			}

			got, ok, err := repo.AccountByUsername(ctx, fold)
			if err != nil || !ok {
				t.Fatalf("AccountByUsername: found %v, %v", ok, err)
			}
			if got.PasswordHash != "the-new-hash" {
				t.Errorf("the hash is %q after a reset, want the new one", got.PasswordHash)
			}
			if got.FailedCount != 0 {
				t.Errorf("%d failed attempts survive a reset, want none", got.FailedCount)
			}
			if !got.LockedUntil.IsZero() {
				t.Errorf("the account is still locked until %s after a reset", got.LockedUntil)
			}
		})
	}
}
