package weather

import (
	"strings"
	"testing"
	"time"
)

// The filter chain of ADR-0068, one rule per case, in the order it runs.
//
// To see these fail, break Decide deliberately:
//   - make covers look at UGC only: "a zone-issued alert for the chosen
//     county" is held, which is every winter storm and heat warning for an
//     operator who gave only their county;
//   - delete the status case: "an NWS test" is sent, which is the monthly
//     required test reaching somebody's radio as a real warning;
//   - delete the references loop: "an update" is sent, the same storm on the
//     air every few minutes;
//   - delete the Cancel case: "a cancellation" is sent;
//   - compare events with == instead of EqualFold: "event in other case" is
//     held.
func TestDecideFollowsTheFilterChain(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 30, 0, 0, time.UTC)
	settings := Settings{Zones: []string{"TXC121", "TXZ103"}, Events: DefaultEvents}
	base := Alert{
		ID: "urn:oid:new", Status: "Actual", MessageType: "Alert",
		Event: "Tornado Warning", UGC: []string{"TXC121"},
		Expires: now.Add(30 * time.Minute),
	}
	sent := map[string]bool{"urn:oid:old": true}

	cases := []struct {
		name   string
		edit   func(a *Alert)
		want   Verdict
		reason string
	}{
		{"a real warning for a chosen county", func(*Alert) {}, Send, ""},
		{"a real warning for a chosen zone", func(a *Alert) { a.UGC = []string{"TXZ103"} }, Send, ""},
		{"event in other case", func(a *Alert) { a.Event = "TORNADO WARNING" }, Send, ""},
		{"an NWS test", func(a *Alert) { a.Status = "Test" }, Hold, ReasonNotReal},
		{"an NWS exercise", func(a *Alert) { a.Status = "Exercise" }, Hold, ReasonNotReal},
		{"a cancellation", func(a *Alert) { a.MessageType = "Cancel" }, Hold, ReasonCancel},
		{"another county", func(a *Alert) { a.UGC = []string{"TXC085"} }, Hold, ReasonNotHere},
		{"a type not chosen", func(a *Alert) { a.Event = "Special Weather Statement" }, Hold, ReasonNotChosen},
		{"expired", func(a *Alert) { a.Expires = now.Add(-time.Minute) }, Hold, ReasonExpired},
		{"ends already passed though expires has not", func(a *Alert) { a.Ends = now.Add(-time.Minute) }, Hold, ReasonExpired},
		{"already sent", func(a *Alert) { a.ID = "urn:oid:old" }, Hold, ReasonAlreadySent},
		{"an update", func(a *Alert) { a.References = []string{"urn:oid:old"} }, Hold, ReasonUpdate},
		{"a reference to something never sent", func(a *Alert) { a.References = []string{"urn:oid:other"} }, Send, ""},
		// Issued by forecast zone, found through the county's SAME code.
		{"a zone-issued alert for the chosen county", func(a *Alert) {
			a.UGC, a.SAME = []string{"TXZ104"}, []string{"048121"}
		}, Send, ""},
		{"a zone-issued alert for another county", func(a *Alert) {
			a.UGC, a.SAME = []string{"TXZ104"}, []string{"048085"}
		}, Hold, ReasonNotHere},
		{"the same county number in another state", func(a *Alert) {
			a.UGC, a.SAME = []string{"OKZ001"}, []string{"040121"}
		}, Hold, ReasonNotHere},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := base
			tc.edit(&a)
			got, reason := Decide(a, settings, sent, now)
			if got != tc.want || reason != tc.reason {
				t.Errorf("Decide = %s %q, want %s %q", got, reason, tc.want, tc.reason)
			}
		})
	}
}

// What a radio shows.
//
// To see it fail: format the expiry in time.UTC rather than the zone's time
// zone, and the Denton case reads 12:45AM UTC.
func TestFormatIsWhatARadioShows(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 30, 0, 0, time.UTC) // 7:30PM CDT on the 29th
	zones := []Zone{
		{Code: "TXC121", Name: "Denton", TimeZone: "America/Chicago"},
		{Code: "TXZ103", Name: "Denton", TimeZone: "America/Chicago"},
	}
	cases := []struct {
		name string
		a    Alert
		want string
	}{
		{
			name: "a warning, in the area's own time",
			a:    Alert{Event: "Tornado Warning", UGC: []string{"TXC121"}, Expires: now.Add(15 * time.Minute)},
			want: "TORNADO WARNING Denton until 7:45PM CDT",
		},
		{
			name: "ends preferred to expires",
			a: Alert{Event: "Severe Thunderstorm Warning", UGC: []string{"TXC121"},
				Expires: now.Add(time.Hour), Ends: now.Add(30 * time.Minute)},
			want: "SEVERE THUNDERSTORM WARNING Denton until 8:00PM CDT",
		},
		{
			name: "tomorrow says which day",
			a:    Alert{Event: "Tornado Watch", UGC: []string{"TXZ103"}, Expires: now.Add(12 * time.Hour)},
			want: "TORNADO WATCH Denton until Wed 7:30AM CDT",
		},
		{
			name: "the operator's area named, not the first NWS lists",
			a: Alert{Event: "Flash Flood Warning", UGC: []string{"TXC085", "TXC121"},
				AreaDesc: "Collin, TX; Denton, TX", Expires: now.Add(15 * time.Minute)},
			want: "FLASH FLOOD WARNING Denton until 7:45PM CDT",
		},
		{
			name: "no expiry",
			a:    Alert{Event: "Tornado Warning", UGC: []string{"TXC121"}},
			want: "TORNADO WARNING Denton",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Format(tc.a, zones, now); got != tc.want {
				t.Errorf("Format = %q, want %q", got, tc.want)
			}
		})
	}
}

// Whatever NWS sends, the text fits one group text.
//
// To see it fail: return the first candidate unconditionally in Format.
func TestFormatAlwaysFits(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 30, 0, 0, time.UTC)
	zones := []Zone{{Code: "TXC121", Name: strings.Repeat("Longname ", 6), TimeZone: "America/Chicago"}}
	for _, event := range []string{
		"Tornado Warning",
		"Extreme Wind Warning And A Very Long Name Nobody Has Issued Yet Ever",
		strings.Repeat("Hydrologic Outlook ", 8),
	} {
		got := Format(Alert{Event: event, UGC: []string{"TXC121"}, Expires: now.Add(time.Hour)}, zones, now)
		if !fits(got) || got == "" || got != strings.TrimSpace(got) {
			t.Errorf("%q formatted as %q (%d characters), which does not fit %d", event, got, len(got), MaxText)
		}
	}
}

func TestZoneCodesAreReadTheWayPeoplePasteThem(t *testing.T) {
	got := NormalizeZones([]string{" txc121, TXZ103\nTXC121;txz104 "})
	want := []string{"TXC121", "TXZ103", "TXZ104"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("NormalizeZones = %v, want %v", got, want)
	}
	for code, ok := range map[string]bool{
		"TXC121": true, "TXZ103": true, "OKZ001": true,
		"TX121": false, "TXX121": false, "TXC12": false, "txc121": false, "": false,
	} {
		if ValidZoneCode(code) != ok {
			t.Errorf("ValidZoneCode(%q) = %v, want %v", code, !ok, ok)
		}
	}
}
