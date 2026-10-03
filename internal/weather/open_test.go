package weather

import (
	"context"
	"slices"
	"testing"
	"time"
)

// A choice of alert types may be a whole class, and the classes are what a
// new Weather page starts with. The first week on the air a Flood Watch sat
// over the county for two days and nothing went out, because the list held
// five exact names.
//
// To see it fail: remove the isClass case from chosen, and "* Watch" matches
// nothing.
func TestAlertTypesMayBeChosenByClass(t *testing.T) {
	cases := []struct {
		event  string
		events []string
		want   bool
	}{
		{"Flood Watch", DefaultEvents, true},
		{"Flash Flood Warning", DefaultEvents, true},
		{"Winter Storm Warning", DefaultEvents, true},
		{"tornado warning", DefaultEvents, true},
		{"Wind Advisory", DefaultEvents, false},
		{"Special Weather Statement", DefaultEvents, false},
		{"Storm Watching Statement", DefaultEvents, false}, // the last word, whole
		{"Wind Advisory", []string{EveryWarning, "Wind Advisory"}, true},
		{"Flood Watch", []string{EveryWarning}, false},
		{"Flood Watch", NarrowDefaultEvents, false},
		{"Special Weather Statement", []string{EveryAlert}, true},
		{"Flood Watch", []string{" *  "}, true},
		{"Flood Watch", []string{"*  watch"}, true},
		{"Flood Watch", nil, false},
	}
	for _, tc := range cases {
		if got := chosen(tc.event, tc.events); got != tc.want {
			t.Errorf("%q with %q chosen: %v, want %v", tc.event, tc.events, got, tc.want)
		}
	}
}

// What an operator asks for at the page goes out at the next poll; only a
// restart is silent. Each case has a Flood Watch already in effect and a
// Tornado Warning that went out earlier, and says what the change sends.
//
// To see it fail:
//   - make every Apply start a baseline (set s.baselined = false in each
//     case of Apply's switch): nothing an operator switches on is sent;
//   - drop the `first` case: a restart sends the watch a second time;
//   - clear s.sent on an alert-type change: the warning goes out twice.
func TestWhatAnOperatorSwitchesOnGoesOutNow(t *testing.T) {
	warningsOnly := transmitting()
	warningsOnly.Events = []string{EveryWarning}
	off := transmitting()
	off.Enabled = false
	preview := denton()

	cases := []struct {
		name          string
		start, change Settings
		restart       bool
		want          []string
	}{
		{"switching alerts on", off, transmitting(), false,
			[]string{"FLOOD WATCH Denton until 1:00PM CDT", "TORNADO WARNING Denton until 12:00PM CDT"}},
		{"putting them on the air after a preview", preview, transmitting(), false,
			[]string{"FLOOD WATCH Denton until 1:00PM CDT", "TORNADO WARNING Denton until 12:00PM CDT"}},
		{"ticking watches sends the watch and not the warning again", warningsOnly, transmitting(), false,
			[]string{"FLOOD WATCH Denton until 1:00PM CDT"}},
		{"saving with nothing changed", transmitting(), transmitting(), false, nil},
		{"a restart", transmitting(), transmitting(), true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeNWS(t)
			c := &clock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
			send := &fakeSender{}
			pace := Pacing{Gap: time.Nanosecond}
			opts := Options{Log: discardLog(), Version: "0.1.294", BaseURL: srv.URL, Now: c.Now, Sender: send, Pace: pace}
			ctx := context.Background()
			drain := func(s *Service) {
				for range 4 {
					c.Advance(time.Second)
					s.Flush(ctx)
				}
			}

			s := New(opts)
			s.Apply(tc.start)
			s.Poll(ctx) // the starting baseline, nothing in effect
			watch := alertJSON("urn:watch", "Flood Watch", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(6*time.Hour))
			warning := alertJSON("urn:warn", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(5*time.Hour))
			f.setAlerts(watch, warning)
			c.Advance(time.Minute)
			s.Poll(ctx)
			drain(s)
			before := len(send.texts())

			if tc.restart {
				s = New(opts)
			}
			s.Apply(tc.change)
			c.Advance(time.Minute)
			s.Poll(ctx)
			drain(s)

			got := send.texts()[before:]
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("sent %q, want %q", got, tc.want)
			}
		})
	}
}

// A warning made longer is sent again, saying until when; one reissued with
// the end it already had is not. This is the flash flood warning of
// 2026-10-01: sent until 10:30, reissued, extended to 12:30, reissued.
//
// To see it fail: remove the s.extends(a) check from decide, and the
// extension is held with the reissues.
func TestAnExtendedWarningIsSentAgain(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 10, 1, 12, 24, 0, 0, time.UTC)}
	send := &fakeSender{}
	s := newTransmitService(t, f, srv.URL, c, send, Pacing{Gap: time.Nanosecond})
	ctx := context.Background()
	const event = "Flash Flood Warning"
	at := func(h, m int) time.Time { return time.Date(2026, 10, 1, h, m, 0, 0, time.UTC) }

	steps := []struct {
		name  string
		id    string
		until time.Time
		refs  []string
		want  string
		sent  int
	}{
		{"the warning", "urn:1", at(15, 30), nil, "send:", 1},
		{"reissued with the same end", "urn:2", at(15, 30), []string{"urn:1"}, "hold:" + ReasonUpdate, 1},
		{"reissued two minutes longer", "urn:3", at(15, 32), []string{"urn:1", "urn:2"}, "hold:" + ReasonUpdate, 1},
		{"extended two hours", "urn:4", at(17, 30), []string{"urn:1", "urn:2", "urn:3"}, "send:", 2},
		{"reissued with the new end", "urn:5", at(17, 30), []string{"urn:1", "urn:2", "urn:3", "urn:4"}, "hold:" + ReasonUpdate, 2},
		{"cut short", "urn:6", at(16, 0), []string{"urn:4", "urn:5"}, "hold:" + ReasonUpdate, 2},
	}
	for _, st := range steps {
		c.Advance(time.Minute)
		f.setAlerts(alertJSON(st.id, event, "Actual", []string{"TXC121"}, c.Now(), st.until, st.refs...))
		s.Poll(ctx)
		c.Advance(time.Second)
		s.Flush(ctx)
		if got := verdicts(s.Status())[st.id]; got != st.want {
			t.Errorf("%s: %q, want %q", st.name, got, st.want)
		}
		if got := len(send.texts()); got != st.sent {
			t.Errorf("%s: %d texts on the air, want %d: %q", st.name, got, st.sent, send.texts())
		}
	}
	if got, want := send.texts()[len(send.texts())-1], "FLASH FLOOD WARNING Denton until 12:30PM CDT"; got != want {
		t.Errorf("the extension read %q, want %q", got, want)
	}
}
