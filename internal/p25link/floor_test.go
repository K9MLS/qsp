package p25link_test

import (
	"net"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/p25link"
	"github.com/k9mls/qsp/internal/protocol/p25"
)

// Break it: grant the floor to whoever asks, never let a silent holder lose
// it, or let anybody release it, and a row fails.
func TestTheFloorIsOneTalkerAtATime(t *testing.T) {
	t0 := time.Unix(1000, 0)
	type step struct {
		do     string // "take" or "release"
		holder string
		at     time.Duration
		want   bool // for take
	}
	tests := []struct {
		name   string
		steps  []step
		holder string // at the last step's time
	}{
		{"the first to ask has it", []step{{"take", "a", 0, true}}, "a"},
		{"and keeps it against another", []step{{"take", "a", 0, true}, {"take", "b", 100 * time.Millisecond, false}}, "a"},
		{"asking again keeps it fresh",
			[]step{{"take", "a", 0, true}, {"take", "a", 900 * time.Millisecond, true}, {"take", "b", 1800 * time.Millisecond, false}}, "a"},
		{"a holder gone quiet loses it",
			[]step{{"take", "a", 0, true}, {"take", "b", p25link.FloorHold + time.Millisecond, true}}, "b"},
		{"exactly at the limit it is still held",
			[]step{{"take", "a", 0, true}, {"take", "b", p25link.FloorHold, false}}, "a"},
		{"released, it is anybody's",
			[]step{{"take", "a", 0, true}, {"release", "a", 10 * time.Millisecond, false}, {"take", "b", 20 * time.Millisecond, true}}, "b"},
		{"only the holder can release it",
			[]step{{"take", "a", 0, true}, {"release", "b", 10 * time.Millisecond, false}, {"take", "b", 20 * time.Millisecond, false}}, "a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var f p25link.Floor
			var last time.Duration
			for i, s := range tc.steps {
				last = s.at
				if s.do == "release" {
					f.Release(s.holder)
					continue
				}
				if got := f.Take(s.holder, t0.Add(s.at)); got != s.want {
					t.Errorf("step %d: take by %q returned %v", i, s.holder, got)
				}
			}
			if got := f.Holder(t0.Add(last)); got != tc.holder {
				t.Errorf("the holder is %q", got)
			}
		})
	}

	t.Run("no floor at all grants everything", func(t *testing.T) {
		var f *p25link.Floor
		if !f.Take("a", t0) || !f.Take("b", t0) || f.Holder(t0) != "" {
			t.Error("a nil floor refused somebody")
		}
		f.Release("a")
	})
}

// registered returns a gateway that has polled and been answered.
func registered(t *testing.T, addr, callsign string) *net.UDPConn {
	t.Helper()
	c := dial(t, addr)
	if _, err := c.Write(p25.NewPoll(callsign).Marshal()); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 64)
	if _, err := c.Read(buf); err != nil {
		t.Fatalf("a poll went unanswered: %v", err)
	}
	return c
}

func read(c *net.UDPConn, wait time.Duration) []byte {
	_ = c.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		return nil
	}
	return buf[:n]
}

// Break it: send a repeater's header or marker to a gateway, change a byte of
// its voice, or end its call with anything but the terminator a gateway
// knows, and a row fails.
func TestWhatARepeaterSendsTheGateways(t *testing.T) {
	voice := voiceFrame(t)
	terminator := append([]byte{0x80}, make([]byte, 16)...)
	tests := []struct {
		name string
		send func(l *p25link.Listener) int
		want []byte // nil is nothing
	}{
		{"a voice frame, as it came", func(l *p25link.Listener) int { return l.FromRepeater(voice) }, voice},
		{"the end of the call", func(l *p25link.Listener) int { return l.EndFromRepeater() }, terminator},
		{"a repeater's start marker", func(l *p25link.Listener) int {
			return l.FromRepeater([]byte{0x00, 0x02, 0x02, 0x0C, 0x0B, 0, 0, 0, 0, 0})
		}, nil},
		{"half a header", func(l *p25link.Listener) int {
			return l.FromRepeater(append([]byte{0x60}, make([]byte, 29)...))
		}, nil},
		{"a poll", func(l *p25link.Listener) int { return l.FromRepeater(p25.NewPoll("K9MLS").Marshal()) }, nil},
		{"a terminator passed as voice", func(l *p25link.Listener) int { return l.FromRepeater(terminator) }, nil},
		{"nothing", func(l *p25link.Listener) int { return l.FromRepeater(nil) }, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, addr, stop := serve(t, p25link.Config{})
			defer stop()
			a, b := registered(t, addr, "GWA"), registered(t, addr, "GWB")

			n := tc.send(l)
			if tc.want == nil {
				if n != 0 || read(a, 150*time.Millisecond) != nil {
					t.Errorf("it reached %d gateways", n)
				}
				return
			}
			if n != 2 {
				t.Errorf("it reached %d gateways, want 2", n)
			}
			for _, c := range []*net.UDPConn{a, b} {
				if got := read(c, 2*time.Second); string(got) != string(tc.want) {
					t.Errorf("a gateway got % x, want % x", got, tc.want)
				}
			}
		})
	}
}

// Break it: carry a gateway's call while a repeater has the floor, or keep
// the floor after the gateway's terminator, and this fails.
func TestAGatewayTakesTurnsWithARepeater(t *testing.T) {
	floor := &p25link.Floor{}
	l, addr, stop := serve(t, p25link.Config{Floor: floor})
	defer stop()
	a, b := registered(t, addr, "GWA"), registered(t, addr, "GWB")
	voice := voiceFrame(t)
	terminator := append([]byte{0x80}, make([]byte, 16)...)

	// A repeater is talking: the gateway's frame is counted and not carried.
	floor.Take("repeater 1", time.Now())
	_, _ = a.Write(voice)
	if got := read(b, 200*time.Millisecond); got != nil {
		t.Fatalf("a gateway's frame was carried over a repeater's call: % x", got)
	}
	if l.Held() != 1 {
		t.Errorf("%d frames held", l.Held())
	}

	// The repeater finishes: the gateway is carried, and holds the floor.
	floor.Release("repeater 1")
	_, _ = a.Write(voice)
	if got := read(b, 2*time.Second); string(got) != string(voice) {
		t.Fatalf("the gateway's frame was not carried: % x", got)
	}
	if floor.Take("repeater 1", time.Now()) {
		t.Error("a repeater took the floor from a gateway in mid-call")
	}

	// Its terminator is carried and gives the floor back.
	_, _ = a.Write(terminator)
	if got := read(b, 2*time.Second); string(got) != string(terminator) {
		t.Fatalf("the terminator was not carried: % x", got)
	}
	if !floor.Take("repeater 1", time.Now()) {
		t.Error("the floor was not given back at the end of the gateway's call")
	}
}

// Break it: make gateways contend with each other, and what two gateways do
// when they key together changes as a side effect of linking a repeater.
func TestGatewaysDoNotContendWithEachOther(t *testing.T) {
	_, addr, stop := serve(t, p25link.Config{Floor: &p25link.Floor{}})
	defer stop()
	a, b := registered(t, addr, "GWA"), registered(t, addr, "GWB")
	voice := voiceFrame(t)

	_, _ = a.Write(voice)
	if got := read(b, 2*time.Second); string(got) != string(voice) {
		t.Fatalf("A's frame did not reach B: % x", got)
	}
	_, _ = b.Write(voice)
	if got := read(a, 2*time.Second); string(got) != string(voice) {
		t.Fatalf("B's frame did not reach A while A had talked: % x", got)
	}
}
