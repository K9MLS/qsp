package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// timeLayout is how instants are stored.
//
// RFC 3339 with nanoseconds, always UTC. SQLite has no date type and stores
// whatever it is given, so the format is this package's to choose and its to
// keep: text that sorts chronologically, which is what lets the expiry sweep be
// a comparison rather than a scan.
const timeLayout = time.RFC3339Nano

// SQLRepository stores accounts and sessions in a database.
//
// **It is deliberately thin.** Every decision the login flow makes — lockout,
// timing, expiry, revocation — lives in Service and is tested without a
// database. What is here is six statements, so that the part which cannot be
// exercised in a container with no SQL driver is also the part with the least
// in it.
type SQLRepository struct{ db *sql.DB }

// NewSQLRepository wraps a database handle.
func NewSQLRepository(db *sql.DB) (*SQLRepository, error) {
	if db == nil {
		return nil, errors.New("auth: a database handle is required")
	}
	return &SQLRepository{db: db}, nil
}

// storeTime renders an instant, or the empty string for the zero value.
//
// The zero value means "never", and it is stored as empty rather than as a
// timestamp in year one so that a query can ask for it without knowing which
// year Go's zero time falls in.
func storeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

// readTime parses a stored instant. Anything unparseable reads as never, which
// for a lockout means "not locked" and for a last login means "never used" —
// both of which fail towards letting the operator in rather than out.
func readTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// AccountByUsername implements Repository.
func (r *SQLRepository) AccountByUsername(ctx context.Context, fold string) (Account, bool, error) {
	const q = `
		SELECT id, username, password_hash, created_at, last_login_at, failed_count, locked_until
		FROM users WHERE username_fold = ?`

	var (
		a                             Account
		created, lastLogin, lockedTil string
	)
	err := r.db.QueryRowContext(ctx, q, fold).Scan(
		&a.ID, &a.Username, &a.PasswordHash, &created, &lastLogin, &a.FailedCount, &lockedTil)
	if errors.Is(err, sql.ErrNoRows) {
		// Not an error. A login for a name nobody holds is an ordinary event,
		// and Service answers it identically to a wrong password.
		return Account{}, false, nil
	}
	if err != nil {
		return Account{}, false, fmt.Errorf("auth: reading account: %w", err)
	}

	a.CreatedAt = readTime(created)
	a.LastLoginAt = readTime(lastLogin)
	a.LockedUntil = readTime(lockedTil)
	return a, true, nil
}

// CreateAccount implements Repository.
func (r *SQLRepository) CreateAccount(ctx context.Context, a Account, fold string) (Account, error) {
	const q = `
		INSERT INTO users (username, username_fold, password_hash, created_at)
		VALUES (?, ?, ?, ?)`

	res, err := r.db.ExecContext(ctx, q, a.Username, fold, a.PasswordHash, storeTime(a.CreatedAt))
	if err != nil {
		return Account{}, fmt.Errorf("auth: creating account %q: %w", a.Username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Account{}, fmt.Errorf("auth: creating account %q: %w", a.Username, err)
	}
	a.ID = id
	return a, nil
}

// UpdateAttempts implements Repository.
func (r *SQLRepository) UpdateAttempts(ctx context.Context, id int64, failed int, lockedUntil, lastLogin time.Time) error {
	const q = `
		UPDATE users SET failed_count = ?, locked_until = ?, last_login_at = ?
		WHERE id = ?`

	if _, err := r.db.ExecContext(ctx, q, failed, storeTime(lockedUntil), storeTime(lastLogin), id); err != nil {
		return fmt.Errorf("auth: recording a login attempt: %w", err)
	}
	return nil
}

// CreateSession implements Repository.
func (r *SQLRepository) CreateSession(ctx context.Context, s Session) error {
	const q = `
		INSERT INTO sessions (token, user_id, created_at, expires_at, source_ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?)`

	if _, err := r.db.ExecContext(ctx, q, s.Token, s.UserID,
		storeTime(s.CreatedAt), storeTime(s.ExpiresAt), s.SourceIP, s.UserAgent); err != nil {
		return fmt.Errorf("auth: storing a session: %w", err)
	}
	return nil
}

// SessionByToken implements Repository.
//
// The join is what makes an account's removal end its sessions in the same
// breath: the schema cascades the delete, and a session whose user is gone
// therefore has no row to find rather than a dangling one to check.
func (r *SQLRepository) SessionByToken(ctx context.Context, token string) (Session, bool, error) {
	const q = `
		SELECT s.token, s.user_id, u.username, s.created_at, s.expires_at, s.source_ip, s.user_agent
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ?`

	var (
		s                Session
		created, expires string
	)
	err := r.db.QueryRowContext(ctx, q, token).Scan(
		&s.Token, &s.UserID, &s.Username, &created, &expires, &s.SourceIP, &s.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("auth: reading session: %w", err)
	}

	s.CreatedAt = readTime(created)
	s.ExpiresAt = readTime(expires)
	return s, true, nil
}

// DeleteSession implements Repository.
func (r *SQLRepository) DeleteSession(ctx context.Context, token string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token); err != nil {
		return fmt.Errorf("auth: ending a session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions implements Repository.
//
// The comparison is on stored text, which works because timeLayout sorts
// chronologically and every value is UTC. Storing local times, or a format
// where "2026-9-1" precedes "2026-10-1", would make this quietly wrong rather
// than fail.
func (r *SQLRepository) DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`, storeTime(now))
	if err != nil {
		return 0, fmt.Errorf("auth: sweeping sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		// The rows are gone either way; a driver that will not count them is
		// not a reason to report a failure.
		return 0, nil
	}
	return int(n), nil
}

// Accounts implements Repository.
func (r *SQLRepository) Accounts(ctx context.Context) ([]Account, error) {
	const q = `
		SELECT id, username, password_hash, created_at, last_login_at,
		       failed_count, locked_until
		FROM users ORDER BY id`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("auth: listing accounts: %w", err)
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		var (
			a         Account
			created   string
			lastLogin sql.NullString
			lockedTil sql.NullString
		)
		if err := rows.Scan(&a.ID, &a.Username, &a.PasswordHash, &created,
			&lastLogin, &a.FailedCount, &lockedTil); err != nil {
			return nil, fmt.Errorf("auth: listing accounts: %w", err)
		}
		a.CreatedAt = readTime(created)
		a.LastLoginAt = readTime(lastLogin.String)
		a.LockedUntil = readTime(lockedTil.String)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: listing accounts: %w", err)
	}
	return out, nil
}

// SetPassword implements Repository.
func (r *SQLRepository) SetPassword(ctx context.Context, id int64, hash string) error {
	const q = `UPDATE users SET password_hash = ?, failed_count = 0, locked_until = NULL WHERE id = ?`

	// **A reset clears the lockout too.** An operator resetting a password for
	// somebody locked out has answered the question the lockout was asking, and
	// leaving it in place would make the new password appear not to work.
	if _, err := r.db.ExecContext(ctx, q, hash, id); err != nil {
		return fmt.Errorf("auth: setting a password: %w", err)
	}
	return nil
}

// DeleteAccount implements Repository.
//
// **In one transaction**, so an account cannot lose its sessions and survive,
// or be removed while its sessions remain usable.
func (r *SQLRepository) DeleteAccount(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: removing an account: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return fmt.Errorf("auth: removing an account's sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
		return fmt.Errorf("auth: removing an account: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("auth: removing an account: %w", err)
	}
	return nil
}
