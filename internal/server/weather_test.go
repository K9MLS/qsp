package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/weather"
)

type fakeWeather struct {
	status  weather.Status
	codes   []string
	contact string
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
