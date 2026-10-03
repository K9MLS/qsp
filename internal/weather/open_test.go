package weather

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/tms"
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
		{"Flood Watch", []string{"*Watch"}, true},
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

// An update is news when it differs from what stations were last told, not
// from the reissue before it. Each case is a chain of NWS messages for one
// storm and the texts it should put on the air.
//
// To see it fail:
//   - drop the event comparison from isNews: the upgrade is held;
//   - choose told() by latest end rather than latest airing
//     (`r.end.After(last.end)`): the creeping warning is compared with the
//     reissue before it and never sent again, and the reissues after an
//     upgrade are compared with the watch and all sent.
func TestAnUpdateIsNewsWhenItSaysSomethingNew(t *testing.T) {
	type msg struct {
		id, event string
		endMin    int // minutes after the first message
		refs      []string
	}
	cases := []struct {
		name string
		msgs []msg
		want []string
	}{
		{
			"a watch upgraded to a warning, then the warning reissued",
			[]msg{
				{"w", "Winter Storm Watch", 360, nil},
				{"W1", "Winter Storm Warning", 360, []string{"w"}},
				{"W2", "Winter Storm Warning", 360, []string{"w", "W1"}},
				{"W3", "Winter Storm Warning", 362, []string{"w", "W1", "W2"}},
			},
			[]string{"WINTER STORM WATCH Denton until 1:00PM CDT", "WINTER STORM WARNING Denton until 1:00PM CDT"},
		},
		{
			"a warning extended nine minutes at a time",
			[]msg{
				{"a0", "Flash Flood Warning", 60, nil},
				{"a1", "Flash Flood Warning", 69, []string{"a0"}},
				{"a2", "Flash Flood Warning", 78, []string{"a0", "a1"}},
				{"a3", "Flash Flood Warning", 87, []string{"a0", "a1", "a2"}},
				{"a4", "Flash Flood Warning", 96, []string{"a0", "a1", "a2", "a3"}},
			},
			[]string{
				"FLASH FLOOD WARNING Denton until 8:00AM CDT",
				"FLASH FLOOD WARNING Denton until 8:18AM CDT",
				"FLASH FLOOD WARNING Denton until 8:36AM CDT",
			},
		},
		{
			"a warning reissued as the storm moves",
			[]msg{
				{"t0", "Tornado Warning", 30, nil},
				{"t1", "Tornado Warning", 30, []string{"t0"}},
				{"t2", "Tornado Warning", 33, []string{"t0", "t1"}},
				{"t3", "Tornado Warning", 25, []string{"t0", "t1", "t2"}},
			},
			[]string{"TORNADO WARNING Denton until 7:30AM CDT"},
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
				f.setAlerts(alertJSON("urn:"+m.id, m.event, "Actual", []string{"TXC121"}, c.Now(),
					first.Add(time.Duration(m.endMin)*time.Minute), prefixed(m.refs)...))
				s.Poll(ctx)
				c.Advance(time.Second)
				s.Flush(ctx)
				c.Advance(time.Minute)
			}
			if got := send.texts(); !slices.Equal(got, tc.want) {
				t.Errorf("on the air:\n  %q\nwant:\n  %q", got, tc.want)
			}
		})
	}
}

func prefixed(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, "urn:"+id)
	}
	return out
}

// The baseline is what was issued before QSP started. When the first poll
// fails — the network is often not up when the service starts — a warning
// issued in that minute is new and must go out; the watch from yesterday must
// still not.
//
// To see it fail: drop `&& a.Sent.Before(s.started)` from decide, and the new
// warning is held as old.
func TestAFailedFirstPollDoesNotHideANewWarning(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	send := &fakeSender{}
	s := New(Options{Log: discardLog(), Version: "0.1.296", BaseURL: srv.URL, Now: c.Now, Sender: send, Pace: Pacing{Gap: time.Nanosecond}})
	s.Apply(transmitting())
	ctx := context.Background()

	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	s.Poll(ctx)
	if s.Status().LastError == "" {
		t.Fatal("the first poll was meant to fail")
	}

	old := alertJSON("urn:old", "Flood Watch", "Actual", []string{"TXC121"}, c.Now().Add(-20*time.Hour), c.Now().Add(4*time.Hour))
	c.Advance(30 * time.Second)
	fresh := alertJSON("urn:new", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(30*time.Minute))
	f.mu.Lock()
	f.fail = false
	f.mu.Unlock()
	f.setAlerts(old, fresh)
	c.Advance(30 * time.Second)
	s.Poll(ctx)
	for range 3 {
		c.Advance(time.Second)
		s.Flush(ctx)
	}
	if got, want := send.texts(), []string{"TORNADO WARNING Denton until 7:30AM CDT"}; !slices.Equal(got, want) {
		t.Errorf("on the air: %q, want %q", got, want)
	}
	if got := verdicts(s.Status())["urn:old"]; got != "hold:"+ReasonBaseline {
		t.Errorf("the watch from before the restart was %q", got)
	}
}

// An alert NWS withdraws while it waits on the pacing limit is not sent late.
//
// To see it fail: remove the stillActive filter on s.queue in decide.
func TestAWithdrawnAlertIsNotSentLate(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	send := &fakeSender{}
	s := newTransmitService(t, f, srv.URL, c, send, Pacing{Gap: 5 * time.Minute})
	ctx := context.Background()

	c.Advance(time.Minute)
	first := alertJSON("urn:1", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(time.Hour))
	second := alertJSON("urn:2", "Severe Thunderstorm Warning", "Actual", []string{"TXC121"}, c.Now().Add(time.Second), c.Now().Add(time.Hour))
	f.setAlerts(first, second)
	s.Poll(ctx)
	s.Flush(ctx) // the tornado warning; the other waits out the gap

	c.Advance(time.Minute)
	f.setAlerts(first) // NWS withdrew the second
	s.Poll(ctx)
	c.Advance(10 * time.Minute)
	s.Flush(ctx)
	if got := send.texts(); len(got) != 1 {
		t.Errorf("on the air: %q, want the tornado warning alone", got)
	}
}

// A save that lands while an alert is being handed to the sender must not
// make the next poll send it again.
//
// To see it fail: remove the `q.id == s.sending` skip from Apply's forget.
func TestASaveDuringASendDoesNotSendTwice(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	var s *Service
	more := transmitting()
	more.Events = []string{EveryAlert}
	send := &savingSender{save: func() { s.Apply(more) }}
	s = newTransmitService(t, f, srv.URL, c, send, Pacing{Gap: time.Nanosecond})
	ctx := context.Background()

	c.Advance(time.Minute)
	f.setAlerts(alertJSON("urn:1", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(time.Hour)))
	s.Poll(ctx)
	s.Flush(ctx) // the sender saves new settings while it holds the alert
	for range 3 {
		c.Advance(time.Minute)
		s.Poll(ctx)
		c.Advance(time.Second)
		s.Flush(ctx)
	}
	if send.n != 1 {
		t.Errorf("the warning went out %d times, want once", send.n)
	}
}

// savingSender saves settings in the middle of its first send.
type savingSender struct {
	save func()
	n    int
}

func (s *savingSender) SendLocalText(hbp.Timeslot, tms.Message) error {
	s.n++
	if s.n == 1 {
		s.save()
	}
	return nil
}
