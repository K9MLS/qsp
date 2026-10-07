package p25link_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/p25calls"
	"github.com/k9mls/qsp/internal/p25link"
)

// frame is a gateway frame of a kind, carrying three link control bytes where
// that kind has them.
func frame(kind byte, lc ...byte) []byte {
	lengths := map[byte]int{0x62: 22, 0x63: 14, 0x65: 17, 0x66: 17, 0x80: 17}
	raw := make([]byte, lengths[kind])
	raw[0] = kind
	copy(raw[1:], lc)
	return raw
}

func waitCalls(t *testing.T, tr *p25calls.Tracker, active, finished int) (live, done []p25calls.Call) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		live, done = tr.Snapshot()
		if len(live) == active && len(done) == finished {
			return live, done
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d in progress and %d finished, want %d and %d", len(live), len(done), active, finished)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Where a gateway's call begins and ends. The protocol carries no call
// identifier: the first voice frame begins one and the terminator ends it.
//
// Break it: begin a call on every frame, end one on a poll, carry the last
// call's talkgroup into the next, or record voice from a gateway that never
// registered, and a row fails.
func TestWhereAGatewaysCallBeginsAndEnds(t *testing.T) {
	voice, terminator := frame(0x63), frame(0x80)
	tg1, tg9 := frame(0x65, 0x00, 0x00, 0x01), frame(0x65, 0x00, 0x00, 0x09)
	radio := frame(0x66, 0x7B, 0x4B, 0xAF)

	type want struct {
		source    uint32
		talkgroup uint16
		frames    int
	}
	tests := []struct {
		name       string
		registered bool
		send       [][]byte
		live       int
		done       []want // newest first
	}{
		{"voice then a terminator is one call", true, [][]byte{voice, voice, terminator},
			0, []want{{0, 0, 2}}},
		{"voice with no terminator yet is a call in progress", true, [][]byte{voice, voice, voice},
			1, nil},
		{"the talkgroup and the radio come from the call's own frames", true,
			[][]byte{voice, tg1, radio, terminator}, 0, []want{{8080303, 1, 3}}},
		{"two calls, back to back", true, [][]byte{voice, terminator, voice, voice, terminator},
			0, []want{{0, 0, 2}, {0, 0, 1}}},
		{"the second call does not inherit the first one's talkgroup", true,
			[][]byte{voice, tg1, radio, terminator, voice, tg9, terminator},
			0, []want{{0, 9, 2}, {8080303, 1, 3}}},
		{"a terminator with no call open is not a call", true, [][]byte{terminator, terminator}, 0, nil},
		{"a gateway that has not registered is not recorded", false, [][]byte{voice, terminator}, 0, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := p25calls.NewTracker(p25calls.Options{})
			_, addr, stop := serve(t, p25link.Config{Calls: tr})
			defer stop()
			c := dial(t, addr)
			if tc.registered {
				c = registered(t, addr, "N0CALL")
			}
			for _, f := range tc.send {
				if _, err := c.Write(f); err != nil {
					t.Fatalf("write: %v", err)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if !tc.registered {
				time.Sleep(100 * time.Millisecond)
			}
			live, done := waitCalls(t, tr, tc.live, len(tc.done))
			for i, w := range tc.done {
				got := done[i]
				if got.Source != w.source || got.Talkgroup != w.talkgroup || got.Frames != w.frames {
					t.Errorf("call %d: radio %d, talkgroup %d, %d frames; want %+v",
						i, got.Source, got.Talkgroup, got.Frames, w)
				}
				if got.Via != "N0CALL" || got.ViaKind != p25calls.ViaGateway || !got.Carried ||
					got.EndReason != p25calls.EndMarked {
					t.Errorf("call %d recorded as %+v", i, got)
				}
			}
			for _, c := range live {
				if c.Via != "N0CALL" || !c.InProgress() {
					t.Errorf("in progress: %+v", c)
				}
			}
		})
	}
}

// Break it: wait for the gateway to key again before closing its last call,
// or end the call at the moment it was noticed and not when it was last
// heard, and this fails.
func TestAGatewaysCallThatStopsIsClosedWhenItWasLastHeard(t *testing.T) {
	tr := p25calls.NewTracker(p25calls.Options{})
	l, addr, stop := serve(t, p25link.Config{Calls: tr})
	defer stop()
	c := registered(t, addr, "N0CALL")

	before := time.Now()
	_, _ = c.Write(frame(0x63))
	waitCalls(t, tr, 1, 0)
	after := time.Now()

	// Still within a second of the last frame: the call stands.
	l.ExpireAt(after.Add(p25link.FloorHold / 2))
	waitCalls(t, tr, 1, 0)

	// Long after: closed, and its end is when the frame came, not now.
	l.ExpireAt(after.Add(10 * time.Second))
	_, done := waitCalls(t, tr, 0, 1)
	if done[0].EndReason != p25calls.EndQuiet {
		t.Errorf("ended %q", done[0].EndReason)
	}
	if done[0].Ended.Before(before) || done[0].Ended.After(after) {
		t.Errorf("ended at %v, and the last frame came between %v and %v", done[0].Ended, before, after)
	}
}

// Break it: record a call that lost its turn as carried, or one that got the
// floor part-way as not carried, and a row fails.
func TestWhetherAGatewaysCallWasCarried(t *testing.T) {
	tests := []struct {
		name    string
		release bool // the repeater finishes before the call's second frame
		carried bool
	}{
		{"a repeater talks throughout", false, false},
		{"a repeater finishes part-way", true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := p25calls.NewTracker(p25calls.Options{})
			floor := &p25link.Floor{}
			_, addr, stop := serve(t, p25link.Config{Calls: tr, Floor: floor})
			defer stop()
			c := registered(t, addr, "N0CALL")

			floor.Take("repeater 1", time.Now())
			_, _ = c.Write(frame(0x63))
			waitCalls(t, tr, 1, 0)
			if tc.release {
				floor.Release("repeater 1")
			} else {
				floor.Take("repeater 1", time.Now())
			}
			_, _ = c.Write(frame(0x63))
			_, _ = c.Write(frame(0x80))
			_, done := waitCalls(t, tr, 0, 1)
			if done[0].Carried != tc.carried || done[0].Frames != 2 {
				t.Errorf("carried %v with %d frames", done[0].Carried, done[0].Frames)
			}
		})
	}
}

// TestAGatewayThatMovesMidCallLeavesNoCallBehind. A gateway behind a home
// router can come from a new port between two polls. Its call was named to
// Last heard by callsign and address, worked out afresh at the end, so a
// call begun from one port and ended from another was ended under a name
// nothing was listed under, and the row stayed "in progress" until QSP was
// restarted.
//
// Break it: in Listener.voice, name the call to Heard by callKey(sender) in
// place of heard.key.
func TestAGatewayThatMovesMidCallLeavesNoCallBehind(t *testing.T) {
	tests := []struct {
		name string
		// end is how the call finishes once the gateway has moved.
		end func(l *p25link.Listener, moved interface{ Write([]byte) (int, error) })
		why p25calls.EndReason
	}{
		{"it sends a terminator from where it is now",
			func(_ *p25link.Listener, moved interface{ Write([]byte) (int, error) }) {
				_, _ = moved.Write(frame(0x80))
			}, p25calls.EndMarked},
		{"it goes quiet and is given up on",
			func(l *p25link.Listener, _ interface{ Write([]byte) (int, error) }) {
				l.ExpireAt(time.Now().Add(10 * time.Second))
			}, p25calls.EndQuiet},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := p25calls.NewTracker(p25calls.Options{})
			l, addr, stop := serve(t, p25link.Config{Calls: tr})
			defer stop()

			first := registered(t, addr, "N0CALL")
			_, _ = first.Write(frame(0x63))
			waitCalls(t, tr, 1, 0)

			// The same gateway, from another port, part-way through.
			moved := registered(t, addr, "N0CALL")
			_, _ = moved.Write(frame(0x63))
			time.Sleep(20 * time.Millisecond)
			live, _ := waitCalls(t, tr, 1, 0)
			if live[0].Frames != 2 {
				t.Errorf("the call in progress has %d frames, want both", live[0].Frames)
			}

			tc.end(l, moved)
			_, done := waitCalls(t, tr, 0, 1)
			if done[0].EndReason != tc.why || done[0].Frames != 2 {
				t.Errorf("recorded as %+v", done[0])
			}
		})
	}
}
