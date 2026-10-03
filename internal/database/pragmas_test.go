package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"
)

// recordingDriver is a driver whose connections remember what was run on
// them, which is all this test needs of a database.
type recordingDriver struct {
	mu    sync.Mutex
	conns []*recordingConn
	// failOn makes a statement fail, to prove a connection that could not be
	// set up is not handed to the pool.
	failOn string
}

func (d *recordingDriver) Open(string) (driver.Conn, error) {
	c := &recordingConn{d: d}
	d.mu.Lock()
	d.conns = append(d.conns, c)
	d.mu.Unlock()
	return c, nil
}

func (d *recordingDriver) ran() [][]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]string, 0, len(d.conns))
	for _, c := range d.conns {
		out = append(out, slices.Clone(c.ran))
	}
	return out
}

type recordingConn struct {
	d      *recordingDriver
	ran    []string
	closed bool
}

func (c *recordingConn) Prepare(q string) (driver.Stmt, error) {
	return &recordingStmt{c: c, q: q}, nil
}
func (c *recordingConn) Close() error               { c.closed = true; return nil }
func (c *recordingConn) Begin() (driver.Tx, error)  { return nil, errors.New("no transactions") }
func (c *recordingConn) Ping(context.Context) error { return nil }

type recordingStmt struct {
	c *recordingConn
	q string
}

func (s *recordingStmt) Close() error  { return nil }
func (s *recordingStmt) NumInput() int { return 0 }
func (s *recordingStmt) Exec([]driver.Value) (driver.Result, error) {
	s.c.d.mu.Lock()
	defer s.c.d.mu.Unlock()
	if s.q == s.c.d.failOn {
		return nil, errors.New("refused")
	}
	s.c.ran = append(s.c.ran, s.q)
	return driver.RowsAffected(0), nil
}
func (s *recordingStmt) Query([]driver.Value) (driver.Rows, error) { return nil, io.EOF }

var registerRecording sync.Once
var theRecordingDriver = &recordingDriver{}

func openRecording(t *testing.T, opts Options) (*DB, *recordingDriver) {
	t.Helper()
	registerRecording.Do(func() { sql.Register("qsp-recording", theRecordingDriver) })
	theRecordingDriver.mu.Lock()
	theRecordingDriver.conns, theRecordingDriver.failOn = nil, ""
	theRecordingDriver.mu.Unlock()
	opts.Driver, opts.DSN = "qsp-recording", "test"
	db, err := Open(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, theRecordingDriver
}

// busy_timeout and foreign_keys belong to a connection, so every connection
// the pool makes must be given them: the four it opens under load, and the
// ones that replace them when their lifetime is up. They were run once, on
// one connection, and the rest of the pool — and after an hour all of it —
// failed at once on any lock.
//
// To see it fail: have pragmaConnector.Connect return the connection without
// running the pragmas, and run them once on the handle as before.
func TestEveryPooledConnectionGetsThePragmas(t *testing.T) {
	want := []string{"PRAGMA busy_timeout = 7000", "PRAGMA journal_mode = WAL", "PRAGMA foreign_keys = ON"}
	db, d := openRecording(t, Options{MaxOpenConns: 4, BusyTimeout: 7 * time.Second, ConnMaxLifetime: time.Hour})
	ctx := context.Background()

	// Four at once, as four console requests would hold them.
	var held []*sql.Conn
	for range 4 {
		c, err := db.SQL().Conn(ctx)
		if err != nil {
			t.Fatalf("Conn: %v", err)
		}
		held = append(held, c)
	}
	for _, c := range held {
		_ = c.Close()
	}
	// And the pool recycled, as it is every ConnMaxLifetime.
	db.SQL().SetConnMaxLifetime(time.Nanosecond)
	time.Sleep(5 * time.Millisecond)
	for range 3 {
		if err := db.SQL().PingContext(ctx); err != nil {
			t.Fatalf("Ping: %v", err)
		}
		time.Sleep(time.Millisecond)
	}

	ran := d.ran()
	if len(ran) < 5 {
		t.Fatalf("only %d connections were made; the test did not exercise the pool", len(ran))
	}
	for i, got := range ran {
		if !slices.Equal(got, want) {
			t.Errorf("connection %d ran %q, want %q", i+1, got, want)
		}
	}
}

// A connection that cannot be set up is closed and not used.
func TestAConnectionThatCannotBeSetUpIsNotUsed(t *testing.T) {
	registerRecording.Do(func() { sql.Register("qsp-recording", theRecordingDriver) })
	theRecordingDriver.mu.Lock()
	theRecordingDriver.conns, theRecordingDriver.failOn = nil, "PRAGMA foreign_keys = ON"
	theRecordingDriver.mu.Unlock()
	_, err := Open(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{Driver: "qsp-recording", DSN: "test"})
	if err == nil {
		t.Fatal("Open succeeded with a pragma refused")
	}
	theRecordingDriver.mu.Lock()
	defer theRecordingDriver.mu.Unlock()
	for i, c := range theRecordingDriver.conns {
		if !c.closed {
			t.Errorf("connection %d was left open", i+1)
		}
	}
}
