package callsigns

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The cache's SQL cannot be executed here — the development container registers
// no driver, by the design ADR-0005 chose — so the statements are checked
// against the migration. Same approach as internal/auth, same reason: a column
// that does not exist compiles, lints, and fails at the first lookup.

func TestCacheSQLNamesOnlyRealColumns(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/0004_callsigns.sql")
	if err != nil {
		t.Fatalf("reading the migration: %v", err)
	}

	create := regexp.MustCompile(`(?is)CREATE TABLE callsigns\s*\((.*?)\n\);`)
	m := create.FindStringSubmatch(string(migration))
	if m == nil {
		t.Fatal("the migration no longer creates callsigns; this check is blind")
	}

	known := map[string]bool{}
	for _, line := range strings.Split(m[1], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		known[strings.Fields(line)[0]] = true
	}

	body, err := os.ReadFile("registry.go")
	if err != nil {
		t.Fatalf("reading registry.go: %v", err)
	}
	sqlLiteral := regexp.MustCompile("(?s)`([^`]*(?:SELECT|INSERT)[^`]*)`")
	ident := regexp.MustCompile(`\b([a-z][a-z_]*)\b`)
	keywords := map[string]bool{
		"select": true, "insert": true, "into": true, "values": true, "from": true,
		"on": true, "conflict": true, "do": true, "update": true, "set": true,
		"excluded": true, "callsigns": true,
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
		t.Fatalf("only %d column references were found; this file touches more", checked)
	}
	t.Logf("checked %d column references", checked)
}

func TestCacheInsertMatchesItsPlaceholders(t *testing.T) {
	body, err := os.ReadFile("registry.go")
	if err != nil {
		t.Fatalf("reading registry.go: %v", err)
	}
	insert := regexp.MustCompile(`(?is)INSERT INTO \w+ \(([^)]*)\)\s*VALUES \(([^)]*)\)`)
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

func TestNewSQLStoreRequiresAHandle(t *testing.T) {
	if _, err := NewSQLStore(nil); err == nil {
		t.Error("a store was built with no database handle")
	}
}
