package weather

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/tms"
)

// fakeSender records what would have gone on the air.
type fakeSender struct {
	mu    sync.Mutex
	sent  []tms.Message
	slots []hbp.Timeslot
	fail  error
}

func (f *fakeSender) SendLocalText(slot hbp.Timeslot, m tms.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.sent = append(f.sent, m)
	f.slots = append(f.slots, slot)
	return nil
}

func (f *fakeSender) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.sent {
		out = append(out, m.Text)
	}
	return out
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func transmitting() Settings {
	s := denton()
	s.Transmit = true
	return s
}

// newTransmitService is a service with a fake sender and no pacing gap, past
// its baseline poll.
func newTransmitService(t *testing.T, f *fakeNWS, srvURL string, c *clock, send Sender, pace Pacing) *Service {
	t.Helper()
	s := New(Options{
		Log:     discardLog(),
		Version: "0.1.293", BaseURL: srvURL, Now: c.Now, Sender: send, Pace: pace,
	})
	s.Apply(transmitting())
	s.Poll(context.Background()) // baseline
	return s
}

func view(st Status, id string) AlertView {
	for _, a := range st.Active {
		if a.ID == id {
			return a
		}
	}
	return AlertView{}
}

// A new warning goes on the air once, as a group text from the configured ID
// on the configured talkgroup and timeslot, and the page says when.
//
// To see it fail: drop the queue append in decide (nothing is sent), or the
// queue removal in Flush (it is sent on every flush).
func TestATransmittedAlertGoesOutOnce(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	send := &fakeSender{}
	s := newTransmitService(t, f, srv.URL, c, send, Pacing{Gap: time.Millisecond})
	ctx := context.Background()

	c.Advance(time.Minute)
	f.setAlerts(alertJSON("urn:warn", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(30*time.Minute)))
	s.Poll(ctx)
	s.Flush(ctx)
	c.Advance(time.Second)
	s.Flush(ctx)
	s.Poll(ctx)
	s.Flush(ctx)

	if got := send.texts(); len(got) != 1 || got[0] != "TORNADO WARNING Denton until 7:31PM CDT" {
		t.Fatalf("sent %q, want the warning once", got)
	}
	m := send.sent[0]
	if m.From != 9990 || m.To != 2 || !m.Group || send.slots[0] != hbp.Timeslot2 {
		t.Errorf("sent %+v on %v, want a group text to TG 2 from 9990 on TS2", m, send.slots[0])
	}
	if v := view(s.Status(), "urn:warn"); v.SentAt.IsZero() || v.Waiting != "" {
		t.Errorf("the page shows %+v, want it sent", v)
	}
	if st := s.Status(); st.Mode != ModeTransmit || !st.CanTransmit {
		t.Errorf("status %+v", st)
	}
}

// Preview sends nothing, and the baseline sends nothing in Transmit either.
//
// Preview is held twice, deliberately: decide does not queue in Preview, and
// Flush does not send in it. To see it fail, remove both the Transmit
// condition on the queue in decide and the Transmit check in Flush; either
// alone still holds.
func TestNothingIsSentInPreviewOrFromTheBaseline(t *testing.T) {
	for _, tc := range []struct {
		name     string
		transmit bool
		baseline bool
	}{
		{"preview", false, false},
		{"issued before QSP started", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeNWS(t)
			c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
			send := &fakeSender{}
			s := New(Options{
				Log:     discardLog(),
				Version: "0.1.293", BaseURL: srv.URL, Now: c.Now, Sender: send, Pace: Pacing{Gap: time.Millisecond},
			})
			set := denton()
			set.Transmit = tc.transmit
			// Issued before QSP started, which is what the baseline is.
			warning := alertJSON("urn:warn", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now().Add(-time.Minute), c.Now().Add(time.Hour))
			if tc.baseline {
				f.setAlerts(warning)
			}
			s.Apply(set)
			s.Poll(context.Background())
			if !tc.baseline {
				c.Advance(time.Minute)
				f.setAlerts(warning)
				s.Poll(context.Background())
			}
			s.Flush(context.Background())
			if got := send.texts(); len(got) != 0 {
				t.Errorf("sent %q", got)
			}
		})
	}
}

// A busy timeslot delays an alert; it is not lost.
//
// To see it fail: remove the alert from the queue when the send fails.
func TestABusyTimeslotDelaysAnAlert(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	send := &fakeSender{fail: errors.New("peers: a call is active on that timeslot")}
	s := newTransmitService(t, f, srv.URL, c, send, Pacing{Gap: time.Millisecond})
	ctx := context.Background()

	f.setAlerts(alertJSON("urn:warn", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(time.Hour)))
	c.Advance(time.Minute)
	s.Poll(ctx)
	s.Flush(ctx)
	if v := view(s.Status(), "urn:warn"); !strings.Contains(v.Waiting, "a call is active") {
		t.Fatalf("the page shows %+v, want it waiting for the call", v)
	}

	send.mu.Lock()
	send.fail = nil
	send.mu.Unlock()
	c.Advance(5 * time.Second)
	s.Flush(ctx)
	if got := send.texts(); len(got) != 1 {
		t.Errorf("after the call ended, sent %q", got)
	}
}

// In an outbreak: warnings first, a gap between alerts, a limit per window,
// and nothing dropped unless it expires.
//
// To see it fail: remove the sort in Flush (the watch goes first), the limit
// check (all three go at once), or the expiry check (a dead warning is sent).
func TestAnOutbreakIsPaced(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	send := &fakeSender{}
	s := newTransmitService(t, f, srv.URL, c, send,
		Pacing{Gap: 10 * time.Second, Limit: 2, Window: 10 * time.Minute, Retry: time.Second})
	ctx := context.Background()

	c.Advance(time.Minute)
	f.setAlerts(
		alertJSON("urn:watch", "Tornado Watch", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(6*time.Hour)),
		alertJSON("urn:w1", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now().Add(time.Second), c.Now().Add(30*time.Minute)),
		alertJSON("urn:w2", "Severe Thunderstorm Warning", "Actual", []string{"TXC121"}, c.Now().Add(2*time.Second), c.Now().Add(2*time.Minute)),
	)
	s.Poll(ctx)
	s.Flush(ctx)
	s.Flush(ctx) // within the gap: nothing more
	if got := send.texts(); len(got) != 1 || !strings.HasPrefix(got[0], "TORNADO WARNING") {
		t.Fatalf("first out: %q, want the tornado warning alone", got)
	}
	c.Advance(11 * time.Second)
	s.Flush(ctx)
	c.Advance(11 * time.Second)
	s.Flush(ctx)
	got := send.texts()
	if len(got) != 2 || !strings.HasPrefix(got[1], "SEVERE THUNDERSTORM WARNING") {
		t.Fatalf("after two gaps: %q, want the warnings before the watch and the limit holding the watch", got)
	}
	if v := view(s.Status(), "urn:watch"); !strings.Contains(v.Waiting, "the limit") {
		t.Errorf("the watch shows %+v, want it waiting on the limit", v)
	}

	// The window passes; the watch goes. A warning that expired meanwhile
	// would not have.
	c.Advance(10 * time.Minute)
	s.Flush(ctx)
	if got := send.texts(); len(got) != 3 || !strings.HasPrefix(got[2], "TORNADO WATCH") {
		t.Errorf("after the window: %q", got)
	}
}

// An alert that expires while waiting is never sent.
func TestAnExpiredAlertIsNotSent(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	send := &fakeSender{fail: errors.New("busy")}
	s := newTransmitService(t, f, srv.URL, c, send, Pacing{Gap: time.Millisecond})
	ctx := context.Background()

	f.setAlerts(alertJSON("urn:short", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(2*time.Minute)))
	c.Advance(time.Minute)
	s.Poll(ctx)
	s.Flush(ctx)
	send.mu.Lock()
	send.fail = nil
	send.mu.Unlock()
	c.Advance(2 * time.Minute)
	s.Flush(ctx)
	if got := send.texts(); len(got) != 0 {
		t.Errorf("sent an expired alert: %q", got)
	}
}

// With no DMR listener there is nothing to send on, and the page says so
// instead of an alert waiting forever without a reason.
func TestWithNoListenerTheReasonIsShown(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := newTransmitService(t, f, srv.URL, c, nil, Pacing{Gap: time.Millisecond})
	ctx := context.Background()
	f.setAlerts(alertJSON("urn:warn", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(time.Hour)))
	c.Advance(time.Minute)
	s.Poll(ctx)
	s.Flush(ctx)
	if v := view(s.Status(), "urn:warn"); v.Waiting != waitingNoDMR {
		t.Errorf("the page shows %+v", v)
	}
	if st := s.Status(); st.CanTransmit {
		t.Error("status says it can transmit")
	}
	if err := s.SendTest(ctx); !errors.Is(err, ErrCannotTransmit) {
		t.Errorf("SendTest: %v, want ErrCannotTransmit", err)
	}
}

// Send test puts the test text on the air the way an alert goes, in Preview
// too, because the operator asked for it.
func TestSendTest(t *testing.T) {
	_, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	send := &fakeSender{}
	s := New(Options{Log: discardLog(), Version: "0.1.293", BaseURL: srv.URL, Now: c.Now, Sender: send})
	s.Apply(denton()) // Preview
	if err := s.SendTest(context.Background()); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if got := send.texts(); len(got) != 1 || got[0] != TestText || send.sent[0].To != 2 || send.sent[0].From != 9990 {
		t.Errorf("sent %+v", send.sent)
	}
	if !fits(TestText) {
		t.Error("the test text does not fit one message")
	}

	unset := denton()
	unset.Talkgroup = 0
	s.Apply(unset)
	if err := s.SendTest(context.Background()); err == nil || !strings.Contains(err.Error(), "talkgroup") {
		t.Errorf("with no talkgroup: %v", err)
	}
}
