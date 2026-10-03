package weather

import (
	"context"
	"slices"
	"testing"
	"time"
)

// An alert in effect until further notice says so, and is not given the time
// its message expires as an end. The shapes are the ones the live feed had on
// 2026-10-03: a river Flood Warning with a null "ends" and an all-zero VTEC
// end, a Flood Warning with a real end days after its "expires", and a Special
// Weather Statement with no VTEC line, whose "expires" is all it has.
//
// To see it fail: make hazardEnd return a.until() unconditionally and drop the
// NoSetEnd branch from Format, and the river warning reads "until 6:30AM CDT".
func TestAnAlertWithNoSetEndSaysSo(t *testing.T) {
	now := time.Date(2026, 10, 3, 11, 22, 0, 0, time.UTC) // 6:22AM CDT
	zones := []Zone{{Code: "TXC121", Name: "Denton", TimeZone: "America/Chicago"}}
	ugc := []string{"TXC121"}
	cases := []struct {
		name    string
		a       Alert
		want    string
		wantEnd time.Time
	}{
		{
			name: "a river flood warning until further notice",
			a:    Alert{Event: "Flood Warning", UGC: ugc, Expires: now.Add(24 * time.Hour), NoSetEnd: true},
			want: "FLOOD WARNING Denton until further notice",
		},
		{
			name:    "a flood warning with a real end, days after the message expires",
			a:       Alert{Event: "Flood Warning", UGC: ugc, Expires: now.Add(12 * time.Hour), Ends: now.Add(100 * time.Hour)},
			want:    "FLOOD WARNING Denton until Wed 10:22AM CDT",
			wantEnd: now.Add(100 * time.Hour),
		},
		{
			name:    "a statement with no VTEC line keeps its expiry",
			a:       Alert{Event: "Special Weather Statement", UGC: ugc, Expires: now.Add(time.Hour)},
			want:    "SPECIAL WEATHER STATEMENT Denton until 7:22AM CDT",
			wantEnd: now.Add(time.Hour),
		},
		{
			name: "too long with the area: the area goes first",
			a: Alert{Event: "Tropical Storm Warning", UGC: []string{"TXC999"}, NoSetEnd: true,
				AreaDesc: "Coastal Waters From Baffin Bay To Port Aransas Out Twenty Miles", Expires: now.Add(6 * time.Hour)},
			want: "TROPICAL STORM WARNING until further notice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Format(tc.a, zones, now); got != tc.want {
				t.Errorf("Format = %q, want %q", got, tc.want)
			}
			if got := tc.a.hazardEnd(); !got.Equal(tc.wantEnd) {
				t.Errorf("hazardEnd = %v, want %v", got, tc.wantEnd)
			}
			// The message itself still stops applying when NWS says, which is
			// what takes it off the queue.
			if tc.a.until().IsZero() {
				t.Error("until is zero: the alert would never leave the queue or the memory")
			}
		})
	}
}

// A reissue of an alert in effect until further notice is not an extension.
// Each chain is what NWS sent for one event and what should go on the air.
// The first is Flood Warning KDVN 0067 as read from the live feed: reissued
// about twice a day, each message expiring a day after it was sent.
//
// To see it fail: have isNews measure a.until() rather than a.hazardEnd(), and
// the reissue that drops a set end is sent as an extension to the time its
// message expires. (Recording a.until() as the airing's end as well sends
// every reissue of the river warning.)
func TestAReissueWithNoSetEndIsNotAnExtension(t *testing.T) {
	type msg struct {
		id         string
		afterHours int // when it is sent, after the first
		lastsHours int // "expires", after it is sent
		noEnd      bool
		refs       []string
	}
	cases := []struct {
		name  string
		event string
		msgs  []msg
		want  []string
	}{
		{
			"a river warning reissued until further notice",
			"Flood Warning",
			[]msg{
				{"r0", 0, 24, true, nil},
				{"r1", 10, 24, true, []string{"r0"}},
				{"r2", 20, 24, true, []string{"r0", "r1"}},
				{"r3", 30, 24, true, []string{"r2"}},
			},
			[]string{"FLOOD WARNING Denton until further notice"},
		},
		{
			"the same reissues with a set end are extensions",
			"Flood Warning",
			[]msg{
				{"e0", 0, 24, false, nil},
				{"e1", 10, 24, false, []string{"e0"}},
			},
			[]string{"FLOOD WARNING Denton until Fri 7:00AM CDT", "FLOOD WARNING Denton until Fri 5:00PM CDT"},
		},
		{
			"a set end replaced by none is not a later end",
			"Flood Warning",
			[]msg{
				{"m0", 0, 24, false, nil},
				{"m1", 10, 24, true, []string{"m0"}},
			},
			[]string{"FLOOD WARNING Denton until Fri 7:00AM CDT"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeNWS(t)
			c := &clock{now: time.Date(2026, 10, 1, 11, 59, 0, 0, time.UTC)}
			send := &fakeSender{}
			s := newTransmitService(t, f, srv.URL, c, send, Pacing{Gap: time.Nanosecond})
			ctx := context.Background()
			c.Advance(time.Minute)
			first := c.Now()
			for _, m := range tc.msgs {
				sent := first.Add(time.Duration(m.afterHours) * time.Hour)
				c.mu.Lock()
				c.now = sent.Add(time.Minute)
				c.mu.Unlock()
				a := alertJSON("urn:"+m.id, tc.event, "Actual", []string{"TXC121"}, sent,
					sent.Add(time.Duration(m.lastsHours)*time.Hour), prefixed(m.refs)...)
				if m.noEnd {
					a = noSetEnd(a)
				}
				f.setAlerts(a)
				s.Poll(ctx)
				c.Advance(time.Second)
				s.Flush(ctx)
			}
			if got := send.texts(); !slices.Equal(got, tc.want) {
				t.Errorf("on the air:\n  %q\nwant:\n  %q", got, tc.want)
			}
			if tc.msgs[0].noEnd {
				if v := view(s.Status(), "urn:"+tc.msgs[0].id); !v.Until.IsZero() {
					t.Errorf("the page shows an end of %v for an alert with none", v.Until)
				}
			}
		})
	}
}

// What parameters.VTEC says about an alert's end, in the shapes NWS sends and
// some it does not. A shape nobody expected must read as "nothing known", not
// fail the poll that carries every other alert.
//
// To see it fail: return true from untilFurtherNotice when the JSON does not
// decode, and the alert with no parameters is read as having no end.
func TestTheVTECLineSaysWhetherThereIsAnEnd(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"until further notice", `["/O.CON.KDVN.FL.W.0067.000000T0000Z-000000T0000Z/"]`, true},
		{"a set end, begun already", `["/O.EXT.KEAX.FL.W.0203.000000T0000Z-261007T1600Z/"]`, false},
		{"a new warning", `["/O.NEW.KMPX.FF.W.0003.260704T0351Z-260704T0800Z/"]`, false},
		{"no VTEC line, as on a Special Weather Statement", ``, false},
		{"null", `null`, false},
		{"not a list", `"/O.CON.KDVN.FL.W.0067.000000T0000Z-000000T0000Z/"`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := untilFurtherNotice([]byte(tc.raw)); got != tc.want {
				t.Errorf("untilFurtherNotice(%s) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
