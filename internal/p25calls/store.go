package p25calls

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// timeLayout is how a time is written to the database: the layout the DMR
// record uses, so the two sort and compare alike.
const timeLayout = time.RFC3339Nano

// Store keeps finished P25 calls in the database.
type Store struct {
	db     *sql.DB
	retain time.Duration
}

// NewStore returns a store that keeps calls for retain. A retain of zero
// keeps nothing.
func NewStore(db *sql.DB, retain time.Duration) *Store {
	return &Store{db: db, retain: retain}
}

// Enabled reports whether calls are kept at all.
func (s *Store) Enabled() bool { return s != nil && s.db != nil && s.retain > 0 }

// Record stores one finished call.
func (s *Store) Record(ctx context.Context, c Call) error {
	if !s.Enabled() {
		return nil
	}
	if c.Ended.IsZero() {
		return fmt.Errorf("p25calls: refusing to store a call that has not ended")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO p25_calls
		   (started_at, ended_at, source, talkgroup, frames,
		    via_kind, via_name, carried, end_reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Started.UTC().Format(timeLayout),
		c.Ended.UTC().Format(timeLayout),
		c.Source, c.Talkgroup, c.Frames,
		c.ViaKind, c.Via, boolToInt(c.Carried), string(c.EndReason))
	if err != nil {
		return fmt.Errorf("p25calls: recording a call from %d: %w", c.Source, err)
	}
	return nil
}

// Since returns the calls that started at or after from, newest first, at
// most limit of them.
func (s *Store) Since(ctx context.Context, from time.Time, limit int) ([]Call, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT started_at, ended_at, source, talkgroup, frames,
		        via_kind, via_name, carried, end_reason
		   FROM p25_calls
		  WHERE started_at >= ?
		  ORDER BY started_at DESC
		  LIMIT ?`,
		from.UTC().Format(timeLayout), limit)
	if err != nil {
		return nil, fmt.Errorf("p25calls: reading history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Call
	for rows.Next() {
		var (
			c                      Call
			started, ended, reason string
			carried                int
		)
		if err := rows.Scan(&started, &ended, &c.Source, &c.Talkgroup, &c.Frames,
			&c.ViaKind, &c.Via, &carried, &reason); err != nil {
			return nil, fmt.Errorf("p25calls: reading a row: %w", err)
		}
		c.Started, _ = time.Parse(timeLayout, started)
		c.Ended, _ = time.Parse(timeLayout, ended)
		c.Carried = carried != 0
		c.EndReason = EndReason(reason)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("p25calls: reading history: %w", err)
	}
	return out, nil
}

// Prune deletes calls older than the retention period, or every call when
// nothing is kept, and reports how many went.
func (s *Store) Prune(ctx context.Context, now time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	query, args := `DELETE FROM p25_calls`, []any(nil)
	if s.retain > 0 {
		query += ` WHERE started_at < ?`
		args = []any{now.Add(-s.retain).UTC().Format(timeLayout)}
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("p25calls: pruning history: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
