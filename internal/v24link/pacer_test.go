package v24link

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// When each voice record of a call leaves, for when it arrived. Times are
// milliseconds from the call's first record.
//
// Break it: send the first record as it arrives, wait again after a dry
// spell, send two records closer than twenty milliseconds to catch up, or
// carry one call's schedule into the next, and a row fails.
func TestWhenEachVoiceRecordLeaves(t *testing.T) {
	const hold = 60
	tests := []struct {
		name     string
		arrive   []int
		leave    []int
		dry      int
		worstGap int
	}{
		{"the first waits for the hold", []int{0}, []int{60}, 0, 0},
		{"steady arrivals leave steadily", []int{0, 20, 40, 60, 80}, []int{60, 80, 100, 120, 140}, 0, 20},
		{"a burst is spread out", []int{0, 0, 0, 0}, []int{60, 80, 100, 120}, 0, 0},
		{"a record late by less than the hold is on time", []int{0, 20, 95, 100}, []int{60, 80, 100, 120}, 0, 75},
		{"a record late by exactly the hold is on time", []int{0, 20, 100}, []int{60, 80, 100}, 0, 80},
		{"a record later than the hold leaves as it arrives", []int{0, 20, 130}, []int{60, 80, 130}, 1, 110},
		{"and the schedule goes on from there, with no second wait",
			[]int{0, 20, 130, 150, 170}, []int{60, 80, 130, 150, 170}, 1, 110},
		{"what queued behind a stall is not hurried",
			[]int{0, 400, 400, 400, 420}, []int{60, 400, 420, 440, 460}, 1, 400},
		{"a call whose end was lost does not lend its schedule to the next",
			[]int{0, 20, 2000, 2020}, []int{60, 80, 2060, 2080}, 0, 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t0 := time.Date(2026, 10, 5, 19, 0, 0, 0, time.UTC)
			s := schedule{hold: hold * time.Millisecond}
			for i, a := range tc.arrive {
				got := s.due(t0.Add(time.Duration(a) * time.Millisecond)).Sub(t0).Milliseconds()
				if got != int64(tc.leave[i]) {
					t.Errorf("record %d, arriving at %d ms, leaves at %d ms, want %d", i, a, got, tc.leave[i])
				}
			}
			if s.dry != tc.dry {
				t.Errorf("ran dry %d times, want %d", s.dry, tc.dry)
			}
			if got := s.worstGap.Milliseconds(); got != int64(tc.worstGap) {
				t.Errorf("the worst gap is %d ms, want %d", got, tc.worstGap)
			}
		})
	}
}

// No two records of a call leave closer than they are spoken, whatever the
// arrivals: the line has no room for it.
func TestNothingIsSentFasterThanItIsSpoken(t *testing.T) {
	t0 := time.Date(2026, 10, 5, 19, 0, 0, 0, time.UTC)
	arrivals := []int{0, 3, 3, 90, 91, 92, 300, 300, 301, 302, 900, 905}
	s := schedule{hold: 40 * time.Millisecond}
	var last time.Time
	for i, a := range arrivals {
		at := s.due(t0.Add(time.Duration(a) * time.Millisecond))
		if i > 0 && at.Sub(last) < RecordInterval {
			t.Errorf("record %d leaves %s after the one before, closer than %s", i, at.Sub(last), RecordInterval)
		}
		last = at
	}
}

// A gateway's call through a listener that holds: the same bytes in the same
// order as one that does not, and not before their time.
//
// Only lower bounds are asserted. A timer is never early, and how late it is
// on a busy machine is not what this is about.
//
// Break it: write voice as it arrives, or let the end markers overtake the
// voice queued ahead of them.
func TestAHeldCallIsTheSameCallLater(t *testing.T) {
	const hold = 60 * time.Millisecond
	l, _ := start(t, Config{Keepalive: time.Hour, Hold: hold})
	c := link(t, l, 1)
	waitFor(t, "the link up", func() bool { return l.LinksUp() == 1 })

	began := time.Now()
	for n := byte(1); n <= 4; n++ {
		l.FromGateway(gatewayFrame(n))
	}
	l.EndFromGateway()

	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	read := func(want []byte, what string) time.Duration {
		t.Helper()
		got := make([]byte, len(want))
		if _, err := io.ReadFull(c, got); err != nil {
			t.Fatalf("waiting for %s: %v", what, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: got % x, want % x", what, got, want)
		}
		return time.Since(began)
	}
	read(callStart, "the start")
	if at := read(asSent(gatewayFrame(1)), "the first voice record"); at < hold {
		t.Errorf("the first voice record left after %s, before the hold of %s", at, hold)
	}
	read(asSent(gatewayFrame(2)), "the second voice record")
	read(asSent(gatewayFrame(3)), "the third voice record")
	if at, least := read(asSent(gatewayFrame(4)), "the fourth voice record"), hold+3*RecordInterval; at < least {
		t.Errorf("the fourth voice record left after %s, want no sooner than %s", at, least)
	}
	read(join(callEnd, callEnd), "the end, twice")
	silent(t, c)
}

// A listener stopped with voice still queued stops: the queue is not a reason
// to wait, and a tunnel that closes mid-call takes its queue with it.
func TestStoppingDoesNotWaitForTheQueue(t *testing.T) {
	tests := []struct {
		name string
		stop func(cancel func(), c io.Closer)
	}{
		{"the listener is cancelled", func(cancel func(), _ io.Closer) { cancel() }},
		{"the tunnel closes", func(cancel func(), c io.Closer) { _ = c.Close(); cancel() }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, cancel := start(t, Config{Keepalive: time.Hour, Hold: MaxHold})
			c := link(t, l, 1)
			waitFor(t, "the link up", func() bool { return l.LinksUp() == 1 })
			for n := range byte(50) { // a second of voice, queued at once
				l.FromGateway(gatewayFrame(n))
			}
			tc.stop(cancel, c)

			done := make(chan struct{})
			go func() { l.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(500 * time.Millisecond):
				t.Fatal("the listener was still waiting on its queue half a second after it was stopped")
			}
		})
	}
}

func TestAHoldThatCannotWorkIsRefused(t *testing.T) {
	tests := []struct {
		name string
		hold time.Duration
		ok   bool
	}{
		{"none", 0, true},
		{"sixty milliseconds", 60 * time.Millisecond, true},
		{"the most allowed", MaxHold, true},
		{"more than the most", MaxHold + time.Millisecond, false},
		{"less than nothing", -time.Millisecond, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Config{ListenAddress: "127.0.0.1:0", Hold: tc.hold}.Validate()
			if (err == nil) != tc.ok {
				t.Errorf("Validate gave %v", err)
			}
		})
	}
}
