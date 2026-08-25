package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/health"
)

func TestOpenReportsMissingDriverClearly(t *testing.T) {
	// This build registers no SQL driver (see ADR-0005). Open must say so in
	// terms an operator can act on, not fail obscurely.
	_, err := Open(context.Background(), nil, Options{
		Driver: "sqlite",
		DSN:    "test.db",
	})
	if err == nil {
		t.Fatal("expected an error when the driver is not registered")
	}
	if !errors.Is(err, ErrDriverNotRegistered) {
		t.Errorf("got %v, want ErrDriverNotRegistered", err)
	}
	if !strings.Contains(err.Error(), "ADR-0005") {
		t.Errorf("error should point to the decision record, got: %v", err)
	}
	if !strings.Contains(err.Error(), "sqlite") {
		t.Errorf("error should name the driver that was requested, got: %v", err)
	}
}

func TestDriverRegisteredIsFalseForUnknown(t *testing.T) {
	if DriverRegistered("definitely-not-a-driver") {
		t.Error("DriverRegistered returned true for an unregistered name")
	}
}

func TestCloseOnNilIsSafe(t *testing.T) {
	var db *DB
	if err := db.Close(); err != nil {
		t.Errorf("Close on a nil DB returned %v, want nil", err)
	}
}

func TestPingOnNilReportsNotOpen(t *testing.T) {
	var db *DB
	if err := db.Ping(context.Background()); err == nil {
		t.Error("Ping on a nil DB returned nil")
	}
}

func TestMigrateOnNilReportsNotOpen(t *testing.T) {
	var db *DB
	if _, err := db.Migrate(context.Background()); err == nil {
		t.Error("Migrate on a nil DB returned nil")
	}
}

func TestHealthCheckReportsUnavailableWithoutDatabase(t *testing.T) {
	// Constitution §3: an absent subsystem says so; it never reports healthy
	// and is never silently omitted.
	c := HealthCheck{DB: nil, UnavailableReason: "no SQL driver is registered in this build"}
	res := c.Check(context.Background())

	if res.Status != health.StatusUnavailable {
		t.Errorf("Status = %q, want %q", res.Status, health.StatusUnavailable)
	}
	if !strings.Contains(res.Summary, "driver") {
		t.Errorf("summary should explain why, got %q", res.Summary)
	}
}

func TestHealthCheckHasStableName(t *testing.T) {
	if got := (HealthCheck{}).Name(); got != "database" {
		t.Errorf("Name() = %q, want database", got)
	}
}
