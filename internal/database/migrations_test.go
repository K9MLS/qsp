package database

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	// The migrations shipped in this binary must satisfy every ordering rule.
	// If this fails, the build is not deployable.
	all, err := LoadMigrations()
	if err != nil {
		t.Fatalf("embedded migrations are invalid: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no migrations are embedded")
	}
	for i, m := range all {
		if m.Version != i+1 {
			t.Errorf("migration %d has version %d, want %d", i, m.Version, i+1)
		}
		if strings.TrimSpace(m.SQL) == "" {
			t.Errorf("migration %q has empty SQL", m.Filename())
		}
		if len(m.Checksum) != 64 {
			t.Errorf("migration %q has checksum %q, want 64 hex characters", m.Filename(), m.Checksum)
		}
	}
}

func TestChecksumsAreStableAcrossLoads(t *testing.T) {
	a, err := LoadMigrations()
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	b, err := LoadMigrations()
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	for i := range a {
		if a[i].Checksum != b[i].Checksum {
			t.Errorf("checksum for %q is not stable", a[i].Filename())
		}
	}
}

func fsWith(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func TestLoadRejectsNonContiguousVersions(t *testing.T) {
	_, err := loadMigrationsFrom(fsWith(map[string]string{
		"0001_first.sql": "CREATE TABLE a (x INTEGER);",
		"0003_third.sql": "CREATE TABLE c (x INTEGER);",
	}), ".")
	if err == nil {
		t.Fatal("expected a gap in versions to be rejected")
	}
	if !strings.Contains(err.Error(), "contiguous") {
		t.Errorf("error should explain the contiguity rule, got: %v", err)
	}
}

func TestLoadRejectsDuplicateVersions(t *testing.T) {
	_, err := loadMigrationsFrom(fsWith(map[string]string{
		"0001_first.sql":  "CREATE TABLE a (x INTEGER);",
		"0001_second.sql": "CREATE TABLE b (x INTEGER);",
	}), ".")
	if err == nil {
		t.Fatal("expected duplicate versions to be rejected")
	}
	if !strings.Contains(err.Error(), "renumber") {
		t.Errorf("error should tell the operator to renumber, got: %v", err)
	}
}

func TestLoadRejectsVersionsNotStartingAtOne(t *testing.T) {
	_, err := loadMigrationsFrom(fsWith(map[string]string{
		"0002_second.sql": "CREATE TABLE b (x INTEGER);",
	}), ".")
	if err == nil {
		t.Fatal("expected migrations not starting at 1 to be rejected")
	}
}

func TestLoadRejectsMalformedFilenames(t *testing.T) {
	cases := map[string]map[string]string{
		"no underscore": {"0001.sql": "SELECT 1;"},
		"non-numeric":   {"first_thing.sql": "SELECT 1;"},
		"no name":       {"0001_.sql": "SELECT 1;"},
		"zero version":  {"0000_zero.sql": "SELECT 1;"},
		"not sql":       {"0001_first.txt": "SELECT 1;"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadMigrationsFrom(fsWith(files), "."); err == nil {
				t.Errorf("expected %s to be rejected", name)
			}
		})
	}
}

func TestLoadRejectsEmptyMigration(t *testing.T) {
	_, err := loadMigrationsFrom(fsWith(map[string]string{
		"0001_first.sql": "   \n\t\n",
	}), ".")
	if err == nil {
		t.Fatal("expected an empty migration to be rejected")
	}
}

func TestLoadOnEmptyDirectory(t *testing.T) {
	got, err := loadMigrationsFrom(fstest.MapFS{}, ".")
	if err != nil {
		t.Fatalf("an empty migration set should be valid, got: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d migrations from an empty directory", len(got))
	}
}

func TestPendingReturnsUnappliedInOrder(t *testing.T) {
	all := []Migration{
		{Version: 1, Name: "a", Checksum: "aa"},
		{Version: 2, Name: "b", Checksum: "bb"},
		{Version: 3, Name: "c", Checksum: "cc"},
	}
	pending, err := Pending(all, map[int]string{1: "aa"})
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("got %d pending, want 2", len(pending))
	}
	if pending[0].Version != 2 || pending[1].Version != 3 {
		t.Errorf("pending versions are %d, %d; want 2, 3", pending[0].Version, pending[1].Version)
	}
}

func TestPendingIsEmptyWhenFullyMigrated(t *testing.T) {
	all := []Migration{{Version: 1, Name: "a", Checksum: "aa"}}
	pending, err := Pending(all, map[int]string{1: "aa"})
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("got %d pending on a fully migrated database", len(pending))
	}
}

func TestPendingDetectsEditedAppliedMigration(t *testing.T) {
	// Editing an applied migration silently diverges databases. It must be an
	// error with clear instructions.
	all := []Migration{{Version: 1, Name: "a", Checksum: "newchecksum1234567890"}}
	_, err := Pending(all, map[int]string{1: "oldchecksum1234567890"})
	if err == nil {
		t.Fatal("expected an edited applied migration to be rejected")
	}
	if !strings.Contains(err.Error(), "add a new migration") {
		t.Errorf("error should tell the operator what to do instead, got: %v", err)
	}
}

func TestPendingRefusesDatabaseFromNewerBuild(t *testing.T) {
	// ADR-0007: refuse to run against a schema this binary does not understand
	// rather than corrupting it.
	all := []Migration{{Version: 1, Name: "a", Checksum: "aa"}}
	_, err := Pending(all, map[int]string{1: "aa", 2: "bb"})
	if err == nil {
		t.Fatal("expected a newer schema to be refused")
	}
	if !strings.Contains(err.Error(), "newer version of QSP") {
		t.Errorf("error should explain the downgrade situation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "backup") {
		t.Errorf("error should mention restoring a backup, got: %v", err)
	}
}

func TestFilenameRoundTrip(t *testing.T) {
	m := Migration{Version: 7, Name: "add_peers"}
	if got := m.Filename(); got != "0007_add_peers.sql" {
		t.Errorf("Filename() = %q, want 0007_add_peers.sql", got)
	}
	parsed, err := parseMigrationName("0007_add_peers.sql")
	if err != nil {
		t.Fatalf("parseMigrationName: %v", err)
	}
	if parsed.Version != 7 || parsed.Name != "add_peers" {
		t.Errorf("parsed = %+v, want version 7 name add_peers", parsed)
	}
}
