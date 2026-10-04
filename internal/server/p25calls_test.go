package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/p25calls"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

func p25Server(t *testing.T, dmr, p25 PeerSource) *Server {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		Peers:         dmr,
		P25Gateways:   p25,
		Auth:          newStubAuth(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

// Which calls are in Last heard, and which of them say their mode, for each
// combination of modes a server can run.
//
// Break it: return before P25 is looked at when DMR is off, and a network
// running P25 alone has an empty Overview; tag the mode on a server with one
// mode, and every row carries a word that tells nobody anything.
func TestLastHeardForEachCombinationOfModes(t *testing.T) {
	now := time.Now().UTC()
	dmr := fixedPeers{
		active: []CallView{{Source: 1, Target: 2, Group: true, Timeslot: 2, Voice: true}},
		recent: []CallView{{Source: 3, Target: 9, Group: true, Timeslot: 1, Voice: true, EndedAt: now.Add(-time.Minute)}},
	}
	p25 := fixedPeers{
		active:  []CallView{{Source: 8080303, Target: 1, Group: true, Voice: true, Via: "Quantar, site 1"}},
		recent:  []CallView{{Source: 7, Target: 1, Group: true, Voice: true, Via: "N0CALL", EndedAt: now.Add(-time.Second)}},
		traffic: Traffic{P25: &P25Traffic{VoiceFrames: 5}},
	}
	tests := []struct {
		name       string
		dmr, p25   PeerSource
		enabled    bool
		active     map[uint32]string // source to mode
		recent     []uint32          // sources, newest first
		p25Traffic bool
	}{
		{"both modes: every call says which", dmr, p25, true,
			map[uint32]string{1: "DMR", 8080303: "P25"}, []uint32{7, 3}, true},
		{"P25 alone: its calls are there and say no mode", nil, p25, false,
			map[uint32]string{8080303: ""}, []uint32{7}, true},
		{"DMR alone: as it always was", dmr, nil, true,
			map[uint32]string{1: ""}, []uint32{3}, false},
		{"neither", nil, nil, false, map[uint32]string{}, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := peersBody(t, p25Server(t, tc.dmr, tc.p25))
			if body.Enabled != tc.enabled {
				t.Errorf("enabled is %v", body.Enabled)
			}
			if (body.Traffic.P25 != nil) != tc.p25Traffic {
				t.Errorf("P25 traffic present: %v", body.Traffic.P25 != nil)
			}
			if len(body.Active) != len(tc.active) {
				t.Fatalf("%d calls in progress, want %d", len(body.Active), len(tc.active))
			}
			for _, c := range body.Active {
				if mode, ok := tc.active[c.Source]; !ok || c.Mode != mode {
					t.Errorf("call from %d has mode %q", c.Source, c.Mode)
				}
			}
			var got []uint32
			for _, c := range body.Recent {
				got = append(got, c.Source)
				if want := tc.active[8080303]; c.Via != "" && c.Mode != want {
					t.Errorf("a finished P25 call has mode %q, want %q", c.Mode, want)
				}
			}
			if len(got) != len(tc.recent) {
				t.Fatalf("finished calls %v, want %v", got, tc.recent)
			}
			for i := range got {
				if got[i] != tc.recent[i] {
					t.Fatalf("finished calls %v, want %v", got, tc.recent)
				}
			}
		})
	}
}

// A P25 call has no timeslot, and the payload has to be able to say so.
//
// Break it: send the field as zero, and the console prints TS0 for a mode
// that has no timeslots.
func TestAP25CallCarriesNoTimeslotField(t *testing.T) {
	tests := []struct {
		name     string
		call     CallView
		wantSlot bool
	}{
		{"a DMR call on timeslot 2", CallView{Source: 1, Timeslot: 2}, true},
		{"a P25 call", CallView{Source: 8080303, Via: "Quantar, site 1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.call)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if _, has := fields["timeslot"]; has != tc.wantSlot {
				t.Errorf("timeslot present: %v in %s", has, raw)
			}
			if _, has := fields["mode"]; has {
				t.Errorf("a mode nobody set is in the payload: %s", raw)
			}
		})
	}
}

type fixedDMRHistory struct{ calls []calls.Call }

func (f fixedDMRHistory) Since(context.Context, time.Time, int) ([]calls.Call, error) {
	return f.calls, nil
}
func (fixedDMRHistory) Enabled() bool { return true }

type fixedP25History struct {
	calls   []p25calls.Call
	enabled bool
}

func (f fixedP25History) Since(context.Context, time.Time, int) ([]p25calls.Call, error) {
	return f.calls, nil
}
func (f fixedP25History) Enabled() bool { return f.enabled }

// The record page's list: both modes merged, newest first, cut to the limit.
//
// Break it: append one record after the other without sorting, cut before
// merging, or give a P25 row a timeslot, and a row fails.
func TestTheRecordHoldsBothModesNewestFirst(t *testing.T) {
	t0 := time.Now().UTC().Add(-time.Hour)
	dmrCall := func(source uint32, after time.Duration) calls.Call {
		return calls.Call{Key: calls.Key{Timeslot: hbp.Timeslot(2)}, Source: source, Target: 2, Group: true,
			Voice: true, Started: t0.Add(after), Ended: t0.Add(after + time.Second)}
	}
	p25Call := func(source uint32, after time.Duration, carried bool) p25calls.Call {
		return p25calls.Call{Source: source, Talkgroup: 1, Via: "Quantar, site 1", Carried: carried,
			Started: t0.Add(after), Ended: t0.Add(after + 2*time.Second), EndReason: p25calls.EndMarked}
	}
	tests := []struct {
		name  string
		dmr   CallHistory
		p25   P25CallHistory
		limit string
		want  []uint32 // sources, newest first
	}{
		{"both, interleaved by time",
			fixedDMRHistory{[]calls.Call{dmrCall(30, 3*time.Minute), dmrCall(10, time.Minute)}},
			fixedP25History{[]p25calls.Call{p25Call(40, 4*time.Minute, true), p25Call(20, 2*time.Minute, false)}, true},
			"", []uint32{40, 30, 20, 10}},
		{"cut to the limit after merging",
			fixedDMRHistory{[]calls.Call{dmrCall(30, 3*time.Minute), dmrCall(10, time.Minute)}},
			fixedP25History{[]p25calls.Call{p25Call(40, 4*time.Minute, true), p25Call(20, 2*time.Minute, false)}, true},
			"3", []uint32{40, 30, 20}},
		{"P25 alone", nil,
			fixedP25History{[]p25calls.Call{p25Call(40, 4*time.Minute, true)}, true}, "", []uint32{40}},
		{"DMR alone, with a P25 record that keeps nothing",
			fixedDMRHistory{[]calls.Call{dmrCall(10, time.Minute)}},
			fixedP25History{[]p25calls.Call{p25Call(40, 4*time.Minute, true)}, false}, "", []uint32{10}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bus := events.NewBus(nil, events.Options{})
			t.Cleanup(bus.Close)
			a := newStubAuth()
			a.sessions["signed-in"] = auth.Session{Token: "signed-in", Username: "operator",
				ExpiresAt: time.Now().Add(time.Hour).UTC()}
			srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
				ListenAddress: "127.0.0.1:0", Auth: a, Calls: tc.dmr, P25Calls: tc.p25,
				Callsign: func(id uint32) string {
					if id == 40 {
						return "K9MLS"
					}
					return ""
				},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			url := "/api/calls?hours=12"
			if tc.limit != "" {
				url += "&limit=" + tc.limit
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			req.AddCookie(&http.Cookie{Name: SessionCookie, Value: "signed-in"})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var body callsResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("not JSON: %v", err)
			}
			if len(body.Calls) != len(tc.want) {
				t.Fatalf("%d calls, want %d", len(body.Calls), len(tc.want))
			}
			for i, c := range body.Calls {
				if c.Source != tc.want[i] {
					t.Fatalf("call %d is from %d, want %d", i, c.Source, tc.want[i])
				}
				p25 := c.Source == 40 || c.Source == 20
				switch {
				case p25 && (c.Mode != "P25" || c.Timeslot != 0 || c.Via != "Quantar, site 1" || !c.Group || !c.Voice):
					t.Errorf("a P25 row reads %+v", c)
				case !p25 && (c.Mode != "" || c.Timeslot != 2 || c.Via != ""):
					t.Errorf("a DMR row reads %+v", c)
				}
				if c.Source == 40 && c.Callsign != "K9MLS" {
					t.Errorf("the callsign was not looked up: %+v", c)
				}
				// Call 20 lost its turn in every case here; call 40 never did.
				if c.Source == 20 && !c.NotCarried {
					t.Errorf("a call that lost its turn is not marked: %+v", c)
				}
				if c.Source == 40 && c.NotCarried {
					t.Errorf("a carried call is marked as not carried: %+v", c)
				}
				if c.EndReason != "" {
					t.Errorf("an ordinary end is remarked on: %q", c.EndReason)
				}
			}
		})
	}
}
