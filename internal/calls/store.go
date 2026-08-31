package calls

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// timeLayout matches the other stores, so a timestamp written here sorts and
// compares against one written by the audit trail.
const timeLayout = time.RFC3339Nano

// Store keeps completed calls beyond the life of the process.
//
// **The ring buffer is a display and this is the record.** They answer different
// questions: the ring answers what is happening now, at memory speed, polled
// every few seconds; this answers what happened, which nobody asks sixty times a
// minute. See docs/adr/ADR-0033.
type Store struct {
	db     *sql.DB
	log    *slog.Logger
	retain time.Duration
}

// NewStore returns a store writing to db, retaining calls for the given
// duration. Zero retention keeps nothing, which is a club's answer to whether
// it wants a log of who transmitted when.
func NewStore(db *sql.DB, log *slog.Logger, retain time.Duration) *Store {
	return &Store{db: db, log: log, retain: retain}
}

// Enabled reports whether anything will be kept.
func (s *Store) Enabled() bool { return s != nil && s.db != nil && s.retain > 0 }

// Record appends one completed call.
//
// A failure is returned rather than swallowed, but the caller keeps the call in
// the ring regardless: a full or locked database costs the record and not the
// display, and a member transmitting is not the moment to fail loudly at
// somebody who cannot act on it.
func (s *Store) Record(ctx context.Context, c Call) error {
	if !s.Enabled() {
		return nil
	}
	if c.Ended.IsZero() {
		return fmt.Errorf("calls: refusing to store a call that has not ended")
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO calls
		   (started_at, ended_at, peer_id, stream_id, timeslot,
		    source, target, is_group, voice, frames, end_reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Started.UTC().Format(timeLayout),
		c.Ended.UTC().Format(timeLayout),
		uint32(c.Key.Peer), uint32(c.Key.Stream), int(c.Key.Timeslot),
		c.Source, c.Target, boolToInt(c.Group), boolToInt(c.Voice),
		c.Frames, string(c.EndReason))
	if err != nil {
		return fmt.Errorf("calls: recording a call from %d: %w", c.Source, err)
	}
	return nil
}

// Since returns completed calls that started at or after from, newest first,
// capped at limit.
//
// **Newest first because that is how somebody reads a net back**: the last
// check-in they remember is the one they scroll from.
func (s *Store) Since(ctx context.Context, from time.Time, limit int) ([]Call, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT started_at, ended_at, peer_id, stream_id, timeslot,
		        source, target, is_group, voice, frames, end_reason
		   FROM calls
		  WHERE started_at >= ?
		  ORDER BY started_at DESC
		  LIMIT ?`,
		from.UTC().Format(timeLayout), limit)
	if err != nil {
		return nil, fmt.Errorf("calls: reading history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Call
	for rows.Next() {
		var (
			c                      Call
			started, ended, reason string
			peer, stream, slot     int64
			group, voice           int
		)
		if err := rows.Scan(&started, &ended, &peer, &stream, &slot,
			&c.Source, &c.Target, &group, &voice, &c.Frames, &reason); err != nil {
			return nil, fmt.Errorf("calls: reading a row: %w", err)
		}
		c.Started, _ = time.Parse(timeLayout, started)
		c.Ended, _ = time.Parse(timeLayout, ended)
		c.Key = Key{
			Peer:     hbp.RepeaterID(peer),
			Stream:   hbp.StreamID(stream),
			Timeslot: hbp.Timeslot(slot),
		}
		c.Group = group != 0
		c.Voice = voice != 0
		c.EndReason = EndReason(reason)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Prune deletes calls older than the retention window and reports how many.
//
// **Scheduled rather than done on every write.** Deleting on each insert makes
// every transmission pay for the retention policy, and a club keeping a year
// would pay a scan per keyup. A row outliving its retention by an hour matters
// to nobody.
func (s *Store) Prune(ctx context.Context, now time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	if s.retain <= 0 {
		// Zero retention means keep nothing, so everything is stale.
		res, err := s.db.ExecContext(ctx, `DELETE FROM calls`)
		if err != nil {
			return 0, fmt.Errorf("calls: clearing history: %w", err)
		}
		n, _ := res.RowsAffected()
		return n, nil
	}

	cutoff := now.Add(-s.retain).UTC().Format(timeLayout)
	res, err := s.db.ExecContext(ctx, `DELETE FROM calls WHERE started_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("calls: pruning history: %w", err)
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
