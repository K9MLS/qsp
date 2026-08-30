package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// timeLayout matches the format the other stores use, so a timestamp written
// here sorts and compares against one written by the session store.
const timeLayout = time.RFC3339Nano

// SQLRecorder writes audit events to the database.
//
// **This did not exist.** Migration 0002 created `audit_events` with its
// indexes, the schema reached version 4 carrying it, SECURITY.md described an
// audit trail that settles arguments between administrators, and `LogRecorder`
// was the only implementation of Recorder in the program. On an instance that
// had been running for weeks the table held zero rows, and could not have held
// any.
//
// The shape is familiar by now: a table, a migration, a redactor and an
// interface, all present and correct, connected to nothing.
type SQLRecorder struct {
	db  *sql.DB
	log *slog.Logger
}

// NewSQLRecorder returns a recorder writing to db.
func NewSQLRecorder(db *sql.DB, log *slog.Logger) *SQLRecorder {
	return &SQLRecorder{db: db, log: log}
}

// Record appends one event.
//
// Detail is redacted on the way in rather than on the way out. A secret written
// to an append-only table is a secret in every backup of it, and the table's own
// comment makes redaction the recorder's job.
func (r *SQLRecorder) Record(ctx context.Context, e Event) error {
	if r == nil || r.db == nil {
		return nil
	}
	if err := e.Validate(); err != nil {
		return err
	}

	detail := "{}"
	if redacted := Redact(e.Detail); len(redacted) > 0 {
		encoded, err := json.Marshal(redacted)
		if err != nil {
			return fmt.Errorf("audit: encoding detail: %w", err)
		}
		detail = string(encoded)
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO audit_events
		   (occurred_at, actor, action, subject, outcome, source_ip, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.OccurredAt.UTC().Format(timeLayout),
		e.Actor, string(e.Action), e.Subject, string(e.Outcome), e.SourceIP, detail)
	if err != nil {
		return fmt.Errorf("audit: recording %q: %w", e.Action, err)
	}
	return nil
}

// Multi writes one event to several recorders.
//
// The log and the database both, deliberately. `LogRecorder`'s own
// documentation says it remains useful alongside persistent storage because an
// operator shipping logs off the box gets the trail too — and the two fail
// independently, so a database that is full or locked does not take the trail
// with it.
type Multi struct {
	mu        sync.RWMutex
	recorders []Recorder
	log       *slog.Logger
}

// NewMulti returns a recorder writing to each of recorders in turn.
func NewMulti(log *slog.Logger, recorders ...Recorder) *Multi {
	return &Multi{recorders: recorders, log: log}
}

// Record writes to every recorder and returns the first failure.
//
// **Every recorder is attempted even after one fails.** Stopping at the first
// error would mean a locked database silently costing the log copy as well,
// which is the copy most likely to be shipped somewhere durable.
func (m *Multi) Record(ctx context.Context, e Event) error {
	m.mu.RLock()
	recorders := m.recorders
	m.mu.RUnlock()

	var first error
	for _, r := range recorders {
		if r == nil {
			continue
		}
		if err := r.Record(ctx, e); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Add appends a recorder.
//
// The database opens after the log exists, so the trail starts as a log and
// gains persistence a moment later. Events recorded in between reach the log and
// not the table, which is the honest outcome and better than holding them.
func (m *Multi) Add(r Recorder) {
	if r == nil {
		return
	}
	m.mu.Lock()
	m.recorders = append(m.recorders, r)
	m.mu.Unlock()
}
