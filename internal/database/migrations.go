package database

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/k9mls/qsp/migrations"
)

// Migration is one ordered schema change.
//
// Migrations are embedded in the binary rather than read from disk so that a
// deployed instance cannot drift from the schema its code expects.
type Migration struct {
	// Version is the ordering key, parsed from the filename prefix.
	Version int
	// Name is the human-readable remainder of the filename.
	Name string
	// SQL is the statement text.
	SQL string
	// Checksum is the SHA-256 of SQL, used to detect a migration that was
	// edited after having been applied.
	Checksum string
}

// Filename reconstructs the canonical filename for m.
func (m Migration) Filename() string {
	return fmt.Sprintf("%04d_%s.sql", m.Version, m.Name)
}

// LoadMigrations reads and validates the embedded migrations.
//
// It enforces the properties that make migration safe to automate: filenames
// parse, versions are unique, versions start at 1, and versions are contiguous.
// A gap or duplicate is a packaging mistake that must stop startup rather than
// produce a partially-migrated database.
func LoadMigrations() ([]Migration, error) {
	return loadMigrationsFrom(migrations.FS(), ".")
}

func loadMigrationsFrom(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("cannot read embedded migrations: %w", err)
	}

	migrations := make([]Migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("migration %q does not end in .sql: remove it or rename it", name)
		}
		m, err := parseMigrationName(name)
		if err != nil {
			return nil, err
		}
		path := name
		if dir != "." {
			path = dir + "/" + name
		}
		body, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, fmt.Errorf("cannot read migration %q: %w", name, err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return nil, fmt.Errorf("migration %q is empty: remove it or give it content", name)
		}
		m.SQL = string(body)
		sum := sha256.Sum256(body)
		m.Checksum = hex.EncodeToString(sum[:])
		migrations = append(migrations, m)
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })

	if err := validateSequence(migrations); err != nil {
		return nil, err
	}
	return migrations, nil
}

func parseMigrationName(filename string) (Migration, error) {
	base := strings.TrimSuffix(filename, ".sql")
	idx := strings.Index(base, "_")
	if idx <= 0 {
		return Migration{}, fmt.Errorf(
			"migration %q is not named correctly: use a four-digit version, an underscore, and a description, for example \"0003_add_peers.sql\"",
			filename)
	}
	versionPart, namePart := base[:idx], base[idx+1:]
	version, err := strconv.Atoi(versionPart)
	if err != nil {
		return Migration{}, fmt.Errorf(
			"migration %q has a non-numeric version %q: use a four-digit number such as \"0003\"",
			filename, versionPart)
	}
	if version <= 0 {
		return Migration{}, fmt.Errorf("migration %q has version %d: versions start at 1", filename, version)
	}
	if namePart == "" {
		return Migration{}, fmt.Errorf("migration %q has no description after the version number", filename)
	}
	return Migration{Version: version, Name: namePart}, nil
}

func validateSequence(migrations []Migration) error {
	if len(migrations) == 0 {
		return nil
	}
	if migrations[0].Version != 1 {
		return fmt.Errorf(
			"the first migration is version %d: migrations must start at 1",
			migrations[0].Version)
	}
	for i := 1; i < len(migrations); i++ {
		prev, cur := migrations[i-1], migrations[i]
		if cur.Version == prev.Version {
			return fmt.Errorf(
				"two migrations share version %d (%q and %q): renumber one of them",
				cur.Version, prev.Filename(), cur.Filename())
		}
		if cur.Version != prev.Version+1 {
			return fmt.Errorf(
				"migration versions jump from %d to %d: they must be contiguous, so add the missing version or renumber",
				prev.Version, cur.Version)
		}
	}
	return nil
}

// Pending returns the migrations in all that have not yet been applied.
//
// applied maps an applied version to the checksum recorded when it ran. A
// version whose checksum no longer matches indicates a migration file was
// edited after being applied, which would leave databases in divergent states;
// that is reported as an error rather than silently ignored.
func Pending(all []Migration, applied map[int]string) ([]Migration, error) {
	var pending []Migration
	for _, m := range all {
		recorded, ok := applied[m.Version]
		if !ok {
			pending = append(pending, m)
			continue
		}
		if recorded != m.Checksum {
			return nil, fmt.Errorf(
				"migration %q was modified after it was applied (recorded checksum %s, current %s): "+
					"revert the file and add a new migration instead of editing an applied one",
				m.Filename(), short(recorded), short(m.Checksum))
		}
	}

	// A database migrated by a newer binary must not be silently downgraded.
	highestKnown := 0
	if len(all) > 0 {
		highestKnown = all[len(all)-1].Version
	}
	for version := range applied {
		if version > highestKnown {
			return nil, fmt.Errorf(
				"the database has migration %d applied but this build only knows up to %d: "+
					"this database was created by a newer version of QSP; upgrade QSP or restore a backup taken before the upgrade",
				version, highestKnown)
		}
	}
	return pending, nil
}

func short(sum string) string {
	if len(sum) <= 12 {
		return sum
	}
	return sum[:12]
}
