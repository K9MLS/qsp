package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/dongle"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

type fakeDongle struct {
	err  error
	verb string
}

func (f *fakeDongle) Status(context.Context) dongle.Status {
	return dongle.Status{Managed: true, Installed: true, Active: "active", Sub: "running"}
}

func (f *fakeDongle) Control(_ context.Context, verb string) error {
	f.verb = verb
	return f.err
}

func newDongleServer(t *testing.T, d DongleControl, rec *recordingAudit) (*Server, *stubAuth) {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	a := newStubAuth()
	opts := Options{ListenAddress: "127.0.0.1:0", Auth: a, Dongle: d}
	if rec != nil {
		opts.Audit = rec
	}
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, a
}

// TestDongleControlIsAuditedWhateverTheOutcome: stopping the vocoder takes
// Zello off the air, so who did it — or tried — is recorded.
//
// To see a row fail: skip recordDongle on the error path, and the refused
// attempt leaves no record.
func TestDongleControlIsAuditedWhateverTheOutcome(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantCode    int
		wantOutcome audit.Outcome
	}{
		{"a restart", nil, http.StatusOK, audit.OutcomeSuccess},
		{"refused by polkit", dongle.ErrNotAuthorized, http.StatusForbidden, audit.OutcomeDenied},
		{"no systemd here", dongle.ErrNotManaged, http.StatusConflict, audit.OutcomeFailure},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingAudit{}
			d := &fakeDongle{err: tc.err}
			srv, a := newDongleServer(t, d, rec)
			resp := authed(t, srv, a, http.MethodPost, "/api/dongle/restart", "")
			if resp.Code != tc.wantCode {
				t.Fatalf("status %d, want %d: %s", resp.Code, tc.wantCode, resp.Body)
			}
			if d.verb != "restart" {
				t.Errorf("the control received %q", d.verb)
			}
			var found *audit.Event
			for i := range rec.events {
				if rec.events[i].Action == audit.ActionDongleControlled {
					found = &rec.events[i]
				}
			}
			if found == nil {
				t.Fatal("no dongle.controlled event was recorded")
			}
			if found.Outcome != tc.wantOutcome || found.Subject != "restart" || found.Actor != "K9MLS" {
				t.Errorf("event %+v", *found)
			}
			if err := found.Validate(); err != nil {
				t.Errorf("the event would be refused by the audit trail: %v", err)
			}
		})
	}
}

// TestTheDongleNeedsASession: neither the status nor the buttons are public.
func TestTheDongleNeedsASession(t *testing.T) {
	srv, _ := newDongleServer(t, &fakeDongle{}, nil)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/dongle", nil),
		httptest.NewRequest(http.MethodPost, "/api/dongle/stop", nil),
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d, want 401", req.Method, req.URL.Path, rec.Code)
		}
	}
}

// TestAnInstanceWithNoTranscoderHasNoDongle.
func TestAnInstanceWithNoTranscoderHasNoDongle(t *testing.T) {
	srv, a := newDongleServer(t, nil, nil)
	resp := authed(t, srv, a, http.MethodGet, "/api/dongle", "")
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", resp.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(resp.Body.Bytes(), &body)
	if body["error"] == "" {
		t.Error("no sentence saying why")
	}
}
