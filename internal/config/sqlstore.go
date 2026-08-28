package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// versionTimeLayout is how instants are stored: RFC 3339 with nanoseconds, UTC.
//
// Text that sorts chronologically, for the same reason the sessions table uses
// it — a format where "2026-9-30" sorts after "2026-10-01" makes an ordered
// query quietly wrong rather than failing.
const versionTimeLayout = time.RFC3339Nano

// SQLVersionStore keeps configuration history in a database.
//
// Three statements. Everything worth deciding about versioning — what a
// rollback means, what is recorded before what — is decided elsewhere and
// tested without a database.
type SQLVersionStore struct{ db *sql.DB }

// NewSQLVersionStore wraps a database handle.
func NewSQLVersionStore(db *sql.DB) (*SQLVersionStore, error) {
	if db == nil {
		return nil, errors.New("config: a database handle is required")
	}
	return &SQLVersionStore{db: db}, nil
}

// Append implements VersionStore.
//
// The number is assigned by the database rather than by the caller, so two
// administrators saving at once cannot be given the same one.
func (s *SQLVersionStore) Append(ctx context.Context, v Version) (Version, error) {
	document, err := json.Marshal(v.Config)
	if err != nil {
		return Version{}, fmt.Errorf("config: cannot encode the configuration: %w", err)
	}

	const q = `
		INSERT INTO configuration_versions
			(created_at, author, summary, checksum, document, schema_ver)
		VALUES (?, ?, ?, ?, ?, ?)`

	res, err := s.db.ExecContext(ctx, q,
		v.CreatedAt.UTC().Format(versionTimeLayout),
		v.Author, v.Summary, v.Checksum, string(document), v.Config.Version)
	if err != nil {
		return Version{}, fmt.Errorf("config: cannot record a configuration version: %w", err)
	}
	number, err := res.LastInsertId()
	if err != nil {
		return Version{}, fmt.Errorf("config: cannot record a configuration version: %w", err)
	}
	v.Number = number
	return v, nil
}

// List implements VersionStore.
func (s *SQLVersionStore) List(ctx context.Context, limit int) ([]Version, error) {
	if limit <= 0 {
		limit = 50
	}
	const q = `
		SELECT number, created_at, author, summary, checksum, document
		FROM configuration_versions ORDER BY number DESC LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("config: cannot read configuration history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Version, 0, limit)
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("config: cannot read configuration history: %w", err)
	}
	return out, nil
}

// Get implements VersionStore.
func (s *SQLVersionStore) Get(ctx context.Context, number int64) (Version, bool, error) {
	const q = `
		SELECT number, created_at, author, summary, checksum, document
		FROM configuration_versions WHERE number = ?`

	v, err := scanVersion(s.db.QueryRowContext(ctx, q, number))
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, false, nil
	}
	if err != nil {
		return Version{}, false, err
	}
	return v, true, nil
}

// Latest implements VersionStore.
func (s *SQLVersionStore) Latest(ctx context.Context) (Version, bool, error) {
	const q = `
		SELECT number, created_at, author, summary, checksum, document
		FROM configuration_versions ORDER BY number DESC LIMIT 1`

	v, err := scanVersion(s.db.QueryRowContext(ctx, q))
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, false, nil
	}
	if err != nil {
		return Version{}, false, err
	}
	return v, true, nil
}

// scanner is what both QueryRow and Rows provide.
type scanner interface{ Scan(dest ...any) error }

// scanVersion reads one row.
//
// A stored document that no longer parses is returned as an error rather than
// skipped: a version an operator can see in the list and not restore is worse
// than one they are told is unreadable.
func scanVersion(row scanner) (Version, error) {
	var (
		v        Version
		created  string
		document string
	)
	if err := row.Scan(&v.Number, &created, &v.Author, &v.Summary, &v.Checksum, &document); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Version{}, err
		}
		return Version{}, fmt.Errorf("config: cannot read a configuration version: %w", err)
	}

	if t, err := time.Parse(versionTimeLayout, created); err == nil {
		v.CreatedAt = t.UTC()
	}
	if err := json.Unmarshal([]byte(document), &v.Config); err != nil {
		return Version{}, fmt.Errorf("config: version %d does not parse: %w", v.Number, err)
	}
	return v, nil
}
