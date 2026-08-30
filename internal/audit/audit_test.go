package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
)

func validEvent() Event {
	return Event{
		OccurredAt: time.Date(2026, 8, 23, 20, 5, 0, 0, time.UTC),
		Actor:      "K9MLS",
		Action:     ActionConfigChanged,
		Subject:    "version 7",
		Outcome:    OutcomeSuccess,
	}
}

func TestValidateAcceptsWellFormedEvent(t *testing.T) {
	if err := validEvent().Validate(); err != nil {
		t.Errorf("a well formed event was rejected: %v", err)
	}
}

func TestValidateRejectsMalformedEvents(t *testing.T) {
	cases := map[string]func(*Event){
		"no timestamp": func(e *Event) { e.OccurredAt = time.Time{} },
		"no actor":     func(e *Event) { e.Actor = "  " },
		"bad action":   func(e *Event) { e.Action = Action("config.vibed") },
		"bad outcome":  func(e *Event) { e.Outcome = Outcome("maybe") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := validEvent()
			mutate(&e)
			if err := e.Validate(); err == nil {
				t.Errorf("expected %s to be rejected", name)
			}
		})
	}
}

func TestIsSensitiveKey(t *testing.T) {
	sensitive := []string{
		"password", "Password", "user_password", "api_token", "TOKEN",
		"private_key", "zello_key", "secret", "Authorization", "session_id",
		"cookie", "signature", "passphrase", "credential",
	}
	for _, k := range sensitive {
		if !IsSensitiveKey(k) {
			t.Errorf("IsSensitiveKey(%q) = false, want true", k)
		}
	}
	benign := []string{"talkgroup", "peer_id", "callsign", "version", "listen_address"}
	for _, k := range benign {
		if IsSensitiveKey(k) {
			t.Errorf("IsSensitiveKey(%q) = true, want false", k)
		}
	}
}

func TestRedactReplacesValuesButKeepsKeys(t *testing.T) {
	got := Redact(map[string]string{
		"talkgroup":   "3100",
		"password":    "hunter2",
		"private_key": "-----BEGIN PRIVATE KEY-----",
	})
	if got["talkgroup"] != "3100" {
		t.Errorf("a benign value was altered: %q", got["talkgroup"])
	}
	if got["password"] != logging.RedactedPlaceholder {
		t.Errorf("password value = %q, want the redaction placeholder", got["password"])
	}
	if _, present := got["private_key"]; !present {
		t.Error("the sensitive key was dropped; it should be retained with a redacted value")
	}
	if got["private_key"] != logging.RedactedPlaceholder {
		t.Errorf("private_key value = %q, want the redaction placeholder", got["private_key"])
	}
}

func TestRedactDoesNotMutateInput(t *testing.T) {
	in := map[string]string{"password": "hunter2"}
	Redact(in)
	if in["password"] != "hunter2" {
		t.Error("Redact mutated its input")
	}
}

func TestRedactOnEmpty(t *testing.T) {
	if got := Redact(nil); got != nil {
		t.Errorf("Redact(nil) = %v, want nil", got)
	}
	if got := Redact(map[string]string{}); got != nil {
		t.Errorf("Redact(empty) = %v, want nil", got)
	}
}

func TestLogRecorderNeverWritesSecrets(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRecorder(logging.New(&buf, logging.Options{Level: slog.LevelInfo, Format: logging.FormatJSON}))

	e := validEvent()
	e.Detail = map[string]string{
		"talkgroup":    "3100",
		"api_token":    "super-secret-token-value",
		"zello_secret": "another-secret",
	}
	if err := r.Record(context.Background(), e); err != nil {
		t.Fatalf("Record: %v", err)
	}

	out := buf.String()
	for _, secret := range []string{"super-secret-token-value", "another-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("the audit log contains a secret value: %q", out)
		}
	}
	if !strings.Contains(out, "3100") {
		t.Errorf("a benign detail value was lost: %q", out)
	}
}

func TestLogRecorderEmitsStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRecorder(logging.New(&buf, logging.Options{Level: slog.LevelInfo, Format: logging.FormatJSON}))

	if err := r.Record(context.Background(), validEvent()); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("audit output is not valid JSON: %v", err)
	}
	if rec["actor"] != "K9MLS" {
		t.Errorf("actor = %v, want K9MLS", rec["actor"])
	}
	if rec["action"] != string(ActionConfigChanged) {
		t.Errorf("action = %v, want %v", rec["action"], ActionConfigChanged)
	}
	if rec["outcome"] != string(OutcomeSuccess) {
		t.Errorf("outcome = %v, want %v", rec["outcome"], OutcomeSuccess)
	}
	if rec[logging.KeySubsystem] != "audit" {
		t.Errorf("subsystem = %v, want audit", rec[logging.KeySubsystem])
	}
}

func TestLogRecorderRejectsInvalidEvent(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRecorder(logging.New(&buf, logging.Options{Level: slog.LevelInfo, Format: logging.FormatJSON}))

	e := validEvent()
	e.Action = Action("nonsense")
	if err := r.Record(context.Background(), e); err == nil {
		t.Fatal("an invalid audit event was recorded")
	}
	if buf.Len() != 0 {
		t.Errorf("a rejected event still produced output: %q", buf.String())
	}
}

func TestLogRecorderImplementsRecorder(t *testing.T) {
	var _ Recorder = (*LogRecorder)(nil)
}

// TestMultiWritesToEveryRecorder.
//
// **The database recorder did not exist and nothing noticed.** Migration 0002
// created audit_events with its indexes, the schema reached version 4 carrying
// it, SECURITY.md described a trail that settles arguments between
// administrators, and LogRecorder was the only implementation of Recorder in
// the program. A production instance held zero rows and could not have held
// any.
func TestMultiWritesToEveryRecorder(t *testing.T) {
	a, b := &countingRecorder{}, &countingRecorder{}
	m := NewMulti(slog.New(slog.NewTextHandler(io.Discard, nil)), a, b)

	ev := Event{
		OccurredAt: time.Now().UTC(),
		Actor:      "K9MLS",
		Action:     ActionUserLogin,
		Outcome:    OutcomeSuccess,
	}
	if err := m.Record(context.Background(), ev); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if a.n != 1 || b.n != 1 {
		t.Errorf("recorders saw %d and %d events", a.n, b.n)
	}

	// **A failure must not cost the others.** Stopping at the first error would
	// mean a locked database silently taking the log copy with it, which is the
	// copy most likely to be shipped somewhere durable.
	bad := &countingRecorder{err: errors.New("database is locked")}
	m = NewMulti(slog.New(slog.NewTextHandler(io.Discard, nil)), bad, a)

	before := a.n
	if err := m.Record(context.Background(), ev); err == nil {
		t.Error("a failing recorder was not reported")
	}
	if a.n != before+1 {
		t.Error("a recorder after the failing one was skipped")
	}
}

// TestAddedRecordersReceiveLaterEvents. The database opens after the log
// exists, so the trail starts as a log and gains persistence a moment later.
func TestAddedRecordersReceiveLaterEvents(t *testing.T) {
	m := NewMulti(slog.New(slog.NewTextHandler(io.Discard, nil)))
	late := &countingRecorder{}
	m.Add(late)

	if err := m.Record(context.Background(), Event{
		OccurredAt: time.Now().UTC(),
		Actor:      "system",
		Action:     ActionServiceStarted,
		Outcome:    OutcomeSuccess,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if late.n != 1 {
		t.Errorf("a recorder added after construction saw %d events", late.n)
	}
}

type countingRecorder struct {
	n   int
	err error
}

func (c *countingRecorder) Record(context.Context, Event) error {
	c.n++
	return c.err
}
