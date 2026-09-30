package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/k9mls/qsp/internal/weather"
)

// Weather alerts (ADR-0068). The settings are saved through /api/config like
// every other page's, so they are versioned and audited the same way; these
// two endpoints are what the Weather page needs besides: what the service is
// doing, and whether the codes an operator typed are ones NWS knows.

// WeatherSource reports on the weather service and checks codes.
// *weather.Service satisfies it.
type WeatherSource interface {
	Status() weather.Status
	CheckZones(ctx context.Context, codes []string, contact string) []weather.ZoneCheck
}

// weatherResponse is GET /api/weather.
type weatherResponse struct {
	// Available is false on a build or an instance with no weather service,
	// so the page can say so instead of showing a form that saves into
	// nothing.
	Available bool            `json:"available"`
	Status    *weather.Status `json:"status,omitempty"`
	// Events are the alert types a new page starts with ticked.
	DefaultEvents []string `json:"default_events"`
	// MaxCharacters is what one alert may be, so the page can say why a
	// long one is shortened.
	MaxCharacters int `json:"max_characters"`
}

func (s *Server) handleWeather(w http.ResponseWriter, _ *http.Request) {
	resp := weatherResponse{DefaultEvents: weather.DefaultEvents, MaxCharacters: weather.MaxText}
	if s.opts.Weather != nil {
		st := s.opts.Weather.Status()
		resp.Available, resp.Status = true, &st
	}
	writeJSON(w, s.log, http.StatusOK, resp)
}

// checkZonesRequest is POST /api/weather/zones.
//
// A POST rather than a query string because it carries the contact email NWS
// is sent, and an address in a URL ends up in logs.
type checkZonesRequest struct {
	Codes   []string `json:"codes"`
	Contact string   `json:"contact"`
}

// maxZoneChecks bounds one check. Each code is a request to NWS, and a county
// and its forecast zones are a handful; this is far more than an area needs
// and refuses turning the page into a way to hammer NWS.
const maxZoneChecks = 20

func (s *Server) handleCheckZones(w http.ResponseWriter, r *http.Request) {
	if s.opts.Weather == nil {
		writeJSON(w, s.log, http.StatusNotFound, map[string]string{
			"error": "this instance has no weather service",
		})
		return
	}
	var req checkZonesRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "the request body is not a list of codes",
		})
		return
	}
	codes := weather.NormalizeZones(req.Codes)
	if len(codes) == 0 {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "give at least one county or zone code, such as TXC121",
		})
		return
	}
	if len(codes) > maxZoneChecks {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "that is more codes than one area needs; check at most 20 at a time",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, s.log, http.StatusOK, map[string]any{
		"checks": s.opts.Weather.CheckZones(ctx, codes, req.Contact),
	})
}
