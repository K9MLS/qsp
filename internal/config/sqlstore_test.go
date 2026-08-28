package config

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The version store's SQL cannot be executed here — this container registers no
// driver, by the design ADR-0005 chose — so the statements are checked against
// the migration instead. Same approach as internal/auth, same reason: a column
// that does not exist compiles, lints, and fails at the first save.

func TestVersionSQLNamesOnlyRealColumns(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/0001_configuration_versions.sql")
	if err != nil {
		t.Fatalf("reading the migration: %v", err)
	}

	create := regexp.MustCompile(`(?is)CREATE TABLE configuration_versions\s*\((.*?)\n\);`)
	m := create.FindStringSubmatch(string(migration))
	if m == nil {
		t.Fatal("the migration no longer creates configuration_versions; this check is blind")
	}

	known := make(map[string]bool)
	for _, line := range strings.Split(m[1], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		known[strings.Fields(line)[0]] = true
	}

	body, err := os.ReadFile("sqlstore.go")
	if err != nil {
		t.Fatalf("reading sqlstore.go: %v", err)
	}
	sqlLiteral := regexp.MustCompile("(?s)`([^`]*(?:SELECT|INSERT)[^`]*)`")
	// **Every lowercase identifier, not only the ones with an underscore.**
	// The first version of this required an underscore and so checked five
	// references out of eleven: number, author, summary, checksum and document
	// all went unexamined, which are most of the columns this file touches.
	ident := regexp.MustCompile(`\b([a-z][a-z_]*)\b`)
	keywords := map[string]bool{
		"select": true, "insert": true, "into": true, "values": true,
		"from": true, "where": true, "order": true, "by": true, "desc": true,
		"limit": true, "configuration_versions": true,
	}

	var checked int
	for _, lit := range sqlLiteral.FindAllStringSubmatch(string(body), -1) {
		for _, id := range ident.FindAllStringSubmatch(strings.ToLower(lit[1]), -1) {
			name := id[1]
			if keywords[name] {
				continue
			}
			checked++
			if !known[name] {
				t.Errorf("the SQL names column %q, which the migration does not create", name)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d column references were found; this file touches more than that, "+
			"so the check is missing some", checked)
	}
	t.Logf("checked %d column references", checked)
}

func TestVersionInsertMatchesItsPlaceholders(t *testing.T) {
	body, err := os.ReadFile("sqlstore.go")
	if err != nil {
		t.Fatalf("reading sqlstore.go: %v", err)
	}
	insert := regexp.MustCompile(`(?is)INSERT INTO \w+\s*\(([^)]*)\)\s*VALUES \(([^)]*)\)`)
	matches := insert.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		t.Fatal("no INSERT was found; this check is not checking anything")
	}
	for _, m := range matches {
		columns := len(strings.Split(m[1], ","))
		placeholders := strings.Count(m[2], "?")
		if columns != placeholders {
			t.Errorf("an INSERT names %d columns and supplies %d values", columns, placeholders)
		}
	}
}

func TestNewSQLVersionStoreRequiresAHandle(t *testing.T) {
	if _, err := NewSQLVersionStore(nil); err == nil {
		t.Error("a store was built with no database handle")
	}
}
