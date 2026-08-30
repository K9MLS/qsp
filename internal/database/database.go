// Package database owns QSP's persistent storage: connection lifecycle,
// schema migration, and shutdown.
//
// Two constraints shape this package.
//
// First, the routing hot path never touches the database. Routing decisions are
// made against in-memory state; this package serves configuration history,
// audit and reporting, all off the critical path. That constraint is what makes
// a pure-Go SQLite driver an acceptable trade, and it must be preserved.
//
// Second, this package does not import a driver. The driver is registered by
// the binary, which keeps cgo and licensing decisions at the edge of the
// program rather than in the middle of it. See docs/adr/ADR-0005. If the
// configured driver is not registered, Open says so plainly instead of failing
// obscurely.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/logging"
)

// ErrDriverNotRegistered is returned when the configured driver is absent from
// the binary.
var ErrDriverNotRegistered = errors.New("database driver is not registered in this build")

// Options configures a database connection.
type Options struct {
	// Driver is the database/sql driver name.
	Driver string
	// DSN is the data source name.
	DSN string
	// BusyTimeout is how long to wait for a lock before returning busy. Zero
	// takes a sensible default rather than SQLite's, which is not to wait.
	BusyTimeout time.Duration
	// MaxOpenConns bounds concurrent connections.
	MaxOpenConns int
	// ConnMaxLifetime bounds connection reuse.
	ConnMaxLifetime time.Duration
}

// DB wraps a *sql.DB with QSP's lifecycle and migration behaviour.
type DB struct {
	sql *sql.DB
	log *slog.Logger
}

// AvailableDrivers returns the driver names registered in this binary, sorted.
func AvailableDrivers() []string {
	drivers := sql.Drivers()
	sort.Strings(drivers)
	return drivers
}

// DriverRegistered reports whether name is available in this binary.
func DriverRegistered(name string) bool {
	for _, d := range sql.Drivers() {
		if d == name {
			return true
		}
	}
	return false
}

// Open connects to the database and verifies the connection.
//
// It does not migrate; call Migrate explicitly so that startup can decide
// whether migration is appropriate.
func Open(ctx context.Context, log *slog.Logger, opts Options) (*DB, error) {
	if !DriverRegistered(opts.Driver) {
		available := AvailableDrivers()
		var have string
		if len(available) == 0 {
			have = "no drivers are registered"
		} else {
			have = "registered drivers: " + strings.Join(available, ", ")
		}
		return nil, fmt.Errorf(
			"%w: %q (%s). The binary must import a driver that registers this name; see docs/adr/ADR-0005",
			ErrDriverNotRegistered, opts.Driver, have)
	}

	handle, err := sql.Open(opts.Driver, opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("cannot open database with driver %q: %w", opts.Driver, err)
	}

	if opts.MaxOpenConns > 0 {
		handle.SetMaxOpenConns(opts.MaxOpenConns)
	}
	if opts.ConnMaxLifetime > 0 {
		handle.SetConnMaxLifetime(opts.ConnMaxLifetime)
	}

	if err := applyPragmas(ctx, handle, opts); err != nil {
		if closeErr := handle.Close(); closeErr != nil {
			return nil, fmt.Errorf("%w (and closing the handle failed: %v)", err, closeErr)
		}
		return nil, err
	}

	if err := handle.PingContext(ctx); err != nil {
		// Close the handle rather than leaking it; the caller has no reference.
		if closeErr := handle.Close(); closeErr != nil {
			return nil, fmt.Errorf("cannot reach database: %w (and closing the handle failed: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("cannot reach database at %q: %w", opts.DSN, err)
	}

	return &DB{sql: handle, log: logging.Subsystem(log, "database")}, nil
}

// applyPragmas sets the three SQLite defaults this project cannot live with.
//
// **None of them were being set.** `sql.Open` was given a bare DSN and the
// connection took SQLite's defaults, which are chosen for a single-process
// command line tool rather than a server:
//
//   - **busy_timeout was 0.** Any lock contention returned SQLITE_BUSY
//     immediately rather than waiting. `database.busy_timeout` was documented,
//     defaulted to five seconds, validated on startup, and applied to nothing —
//     the same shape as the export lists and the target-size token. QSP writes
//     an audit event on every peer connecting and reads a session on every
//     console request, against a pool of four connections.
//
//   - **journal_mode was DELETE**, under which a writer blocks every reader for
//     the length of its transaction. WAL lets them proceed, which is what a
//     server wants and what costs nothing here.
//
//   - **foreign_keys was OFF**, SQLite's default for compatibility. Migration
//     0003 declares `sessions.user_id REFERENCES users(id) ON DELETE CASCADE`
//     and that cascade has never fired. Nothing deletes a user today, and the
//     session lookup is an inner join so an orphaned row cannot authenticate —
//     but a constraint the schema states and the database ignores is one
//     somebody will eventually rely on.
//
// Set with SQL rather than DSN parameters, because DSN syntax is the driver's
// and ADR-0005 keeps this package from knowing which driver it has.
func applyPragmas(ctx context.Context, handle *sql.DB, opts Options) error {
	ms := opts.BusyTimeout.Milliseconds()
	if ms <= 0 {
		ms = 5000
	}
	pragmas := []string{
		fmt.Sprintf("PRAGMA busy_timeout = %d", ms),
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
	}
	for _, p := range pragmas {
		if _, err := handle.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("cannot apply %q: %w", p, err)
		}
	}
	return nil
}

// SQL exposes the underlying handle for repository implementations.
//
// Callers must not change pool settings or close it; lifecycle belongs to DB.
func (d *DB) SQL() *sql.DB { return d.sql }

// Close releases the connection pool. It is safe to call on a nil DB so that
// shutdown paths need no nil checks.
func (d *DB) Close() error {
	if d == nil || d.sql == nil {
		return nil
	}
	return d.sql.Close()
}

// Ping verifies the connection is usable.
func (d *DB) Ping(ctx context.Context) error {
	if d == nil || d.sql == nil {
		return errors.New("database is not open")
	}
	return d.sql.PingContext(ctx)
}

// schemaMigrationsDDL creates the migration bookkeeping table.
//
// It is written inline rather than as migration 0000 because the migration
// runner needs it to exist before it can read any migration state.
const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL,
    checksum    TEXT    NOT NULL,
    applied_at  TEXT    NOT NULL
);`

// MigrateResult describes what Migrate did.
type MigrateResult struct {
	// AlreadyApplied is the number of migrations already present.
	AlreadyApplied int
	// Applied lists the migrations applied by this call, in order.
	Applied []Migration
	// SchemaVersion is the highest version present after migrating.
	SchemaVersion int
}

// Migrate brings the schema up to date.
//
// Each migration runs inside its own transaction together with the insert that
// records it, so a failure leaves the database at a known version rather than
// half-migrated. Migrations are applied in ascending order and stop at the
// first failure.
func (d *DB) Migrate(ctx context.Context) (MigrateResult, error) {
	if d == nil || d.sql == nil {
		return MigrateResult{}, errors.New("database is not open")
	}

	all, err := LoadMigrations()
	if err != nil {
		return MigrateResult{}, err
	}

	if _, err := d.sql.ExecContext(ctx, schemaMigrationsDDL); err != nil {
		return MigrateResult{}, fmt.Errorf("cannot create the schema_migrations table: %w", err)
	}

	applied, err := d.appliedMigrations(ctx)
	if err != nil {
		return MigrateResult{}, err
	}

	pending, err := Pending(all, applied)
	if err != nil {
		return MigrateResult{}, err
	}

	result := MigrateResult{AlreadyApplied: len(applied)}
	for _, m := range pending {
		if err := d.applyOne(ctx, m); err != nil {
			return result, err
		}
		result.Applied = append(result.Applied, m)
		d.log.Info("applied migration",
			slog.Int("version", m.Version),
			slog.String("name", m.Name),
		)
	}

	result.SchemaVersion = len(applied) + len(result.Applied)
	if len(all) > 0 {
		result.SchemaVersion = all[len(all)-1].Version
	}
	return result, nil
}

func (d *DB) appliedMigrations(ctx context.Context) (map[int]string, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("cannot read applied migrations: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			d.log.Warn("closing migration rows failed", slog.String("error", cerr.Error()))
		}
	}()

	applied := make(map[int]string)
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("cannot read an applied migration row: %w", err)
		}
		applied[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cannot iterate applied migrations: %w", err)
	}
	return applied, nil
}

func (d *DB) applyOne(ctx context.Context, m Migration) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cannot begin transaction for migration %q: %w", m.Filename(), err)
	}
	// Rollback is a no-op after a successful commit.
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("migration %q failed: %w", m.Filename(), err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`,
		m.Version, m.Name, m.Checksum, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("cannot record migration %q: %w", m.Filename(), err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cannot commit migration %q: %w", m.Filename(), err)
	}
	return nil
}

// HealthCheck reports on database connectivity.
//
// When db is nil the check reports StatusUnavailable rather than failing: a
// build without a registered driver has nothing to check, and saying so is more
// truthful than claiming a fault.
type HealthCheck struct {
	// DB is the database to check. It may be nil.
	DB *DB
	// UnavailableReason explains why DB is nil, and is shown to the operator.
	UnavailableReason string
}

// Name implements health.Checker.
func (h HealthCheck) Name() string { return "database" }

// Check implements health.Checker.
func (h HealthCheck) Check(ctx context.Context) health.Result {
	if h.DB == nil || h.DB.sql == nil {
		reason := h.UnavailableReason
		if reason == "" {
			reason = "no database is configured"
		}
		return health.Unavailable(reason)
	}
	if err := h.DB.Ping(ctx); err != nil {
		return health.Failing(
			"cannot reach the database: "+err.Error(),
			"check that the database file is present and writable, and that the disk is not full",
		)
	}

	stats := h.DB.sql.Stats()
	res := health.Healthy("connected")
	res.Detail = map[string]string{
		"open_connections": strconv.Itoa(stats.OpenConnections),
		"in_use":           strconv.Itoa(stats.InUse),
		"idle":             strconv.Itoa(stats.Idle),
	}
	return res
}
