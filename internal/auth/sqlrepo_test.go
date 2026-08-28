package auth_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/auth"
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
