package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/weather"
)

type fakeWeather struct {
	status  weather.Status
	codes   []string
	contact string
	tests   int
	testErr error
}

func (f *fakeWeather) SendTest(context.Context) error {
	f.tests++
	return f.testErr
}

func (f *fakeWeather) Status() weather.Status { return f.status }

func (f *fakeWeather) CheckZones(_ context.Context, codes []string, contact string) []weather.ZoneCheck {
	f.codes, f.contact = codes, contact
	out := make([]weather.ZoneCheck, 0, len(codes))
	for _, c := range codes {
		out = append(out, weather.ZoneCheck{Code: c, OK: true, Zone: &weather.Zone{Code: c, Name: "Denton"}})
	}
	return out
}

func newWeatherServer(t *testing.T, w WeatherSource) (*Server, *stubAuth) {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	a := newStubAuth()
	opts := Options{ListenAddress: "127.0.0.1:0", Auth: a}
	if w != nil {
		opts.Weather = w
	}
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, a
}

// Both endpoints are behind a session: the status names the operator's area
// and the check sends their email to NWS.
//
// To see it fail: register either route without requireSession.
func TestWeatherEndpointsNeedASession(t *testing.T) {
	srv, _ := newWeatherServer(t, &fakeWeather{})
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/weather", ""},
		{http.MethodPost, "/api/weather/zones", `{"codes":["TXC121"]}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestTheWeatherPageIsToldWhatItNeeds(t *testing.T) {
	t.Run("with a service", func(t *testing.T) {
		f := &fakeWeather{status: weather.Status{Enabled: true, Mode: weather.ModePreview}}
		srv, a := newWeatherServer(t, f)
		resp := authed(t, srv, a, http.MethodGet, "/api/weather", "")
		if resp.Code != http.StatusOK {
			t.Fatalf("status %d: %s", resp.Code, resp.Body)
		}
		var body weatherResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("%v", err)
		}
		if !body.Available || body.Status == nil || !body.Status.Enabled || body.Status.Mode != "preview" {
			t.Errorf("body %+v", body)
		}
		if len(body.DefaultEvents) == 0 || body.MaxCharacters != weather.MaxText {
			t.Errorf("the page is not told its defaults: %+v", body)
		}
	})
	t.Run("without one", func(t *testing.T) {
		srv, a := newWeatherServer(t, nil)
		resp := authed(t, srv, a, http.MethodGet, "/api/weather", "")
		var body weatherResponse
		_ = json.Unmarshal(resp.Body.Bytes(), &body)
		if resp.Code != http.StatusOK || body.Available || body.Status != nil {
			t.Errorf("status %d body %s", resp.Code, resp.Body)
		}
	})
}

// Codes are normalised before NWS is asked, the contact is passed through,
// and a request that would hammer NWS is refused.
//
// To see it fail: pass req.Codes rather than the normalised list, and the
// service is asked for " txc121".
func TestCheckingCodes(t *testing.T) {
	f := &fakeWeather{}
	srv, a := newWeatherServer(t, f)
	resp := authed(t, srv, a, http.MethodPost, "/api/weather/zones",
		`{"codes":[" txc121, TXZ103 "],"contact":"k9mls@example.org"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body)
	}
	if strings.Join(f.codes, ",") != "TXC121,TXZ103" || f.contact != "k9mls@example.org" {
		t.Errorf("service asked for %v with contact %q", f.codes, f.contact)
	}

	many := make([]string, 0, maxZoneChecks+1)
	for i := range maxZoneChecks + 1 {
		many = append(many, fmt.Sprintf("TXC%03d", i+1))
	}
	for name, body := range map[string]string{
		"no codes": `{"codes":[]}`,
		"not JSON": `codes=TXC121`,
		"too many": `{"codes":["` + strings.Join(many, ",") + `"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			f.codes = nil
			resp := authed(t, srv, a, http.MethodPost, "/api/weather/zones", body)
			if resp.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400: %s", resp.Code, resp.Body)
			}
			if f.codes != nil {
				t.Error("NWS was asked anyway")
			}
		})
	}
}

// Send test is a session-only write, says what happened in words, and is
// audited whether or not it went out: it puts something on the air.
//
// To see it fail: drop recordWeatherTest from handleWeatherTest.
func TestSendTestIsAuditedAndAnswered(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		outcome audit.Outcome
	}{
		{"sent", nil, http.StatusAccepted, audit.OutcomeSuccess},
		{"refused", weather.ErrCannotTransmit, http.StatusConflict, audit.OutcomeFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeWeather{testErr: tc.err, status: weather.Status{Talkgroup: 2, Timeslot: 2, SenderID: 9990}}
			rec := &recordingAudit{}
			bus := events.NewBus(nil, events.Options{})
			t.Cleanup(bus.Close)
			a := newStubAuth()
			srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus,
				Options{ListenAddress: "127.0.0.1:0", Auth: a, Weather: f, Audit: rec})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			resp := authed(t, srv, a, http.MethodPost, "/api/weather/test", "")
			if resp.Code != tc.status || f.tests != 1 {
				t.Fatalf("status %d after %d tests: %s", resp.Code, f.tests, resp.Body)
			}
			var body map[string]string
			_ = json.Unmarshal(resp.Body.Bytes(), &body)
			if body["note"] == "" && body["error"] == "" {
				t.Errorf("the page is told nothing: %s", resp.Body)
			}
			if strings.HasPrefix(body["error"], "weather:") {
				t.Errorf("the page is shown the package name: %q", body["error"])
			}
			if len(rec.events) == 0 {
				t.Fatal("not audited")
			}
			ev := rec.events[len(rec.events)-1]
			if ev.Action != audit.ActionWeatherTest || ev.Outcome != tc.outcome || ev.Subject != "talkgroup 2" {
				t.Errorf("audited as %+v", ev)
			}
			if err := ev.Validate(); err != nil {
				t.Errorf("the audit trail would refuse it: %v", err)
			}
		})
	}

	srv, _ := newWeatherServer(t, &fakeWeather{})
	req := httptest.NewRequest(http.MethodPost, "/api/weather/test", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("without a session: %d, want 401", rec.Code)
	}
}
