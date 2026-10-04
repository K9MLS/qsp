package v24link

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/p25link"
)

// gateways records what a repeater's call sends the gateway listener.
type gateways struct {
	mu     sync.Mutex
	frames [][]byte
	ends   int
}

func (g *gateways) FromRepeater(frame []byte) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.frames = append(g.frames, bytes.Clone(frame))
	return 1
}

func (g *gateways) EndFromRepeater() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ends++
	return 1
}

func (g *gateways) counts() (frames, ends int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.frames), g.ends
}

var (
	callStart = tunnel([]byte{0x07, 0x03, 0x00, 0x02, 0x02, 0x0C, 0x0B, 0, 0, 0, 0, 0})
	callEnd   = tunnel([]byte{0x07, 0x03, 0x00, 0x02, 0x02, 0x25, 0x0B, 0, 0, 0, 0, 0})
)

// voiceRecord is a voice record whose audio bytes are all n, so one call's
// frames can be told from another's.
func voiceRecord(n byte) []byte {
	return tunnel(append([]byte{0x07, 0x03, 0x63}, bytes.Repeat([]byte{n}, 13)...))
}

// The three captured transmissions, carried to the gateway listener.
//
// Break it: convert, trim or rebuild a voice record on its way through, and
// the bytes the gateways get are not the bytes the repeater sent. Send the
// header or the markers as well, and the count is wrong. Forget the end, and
// a gateway is left keyed.
func TestACapturedCallReachesTheGatewaysByteForByte(t *testing.T) {
	var want [][]byte
	for _, payload := range stationFrames(t, voiceFixture) {
		if rec, ok := ReadRecord(payload); ok && rec.Kind == RecordVoice {
			want = append(want, payload[2:])
		}
	}
	if len(want) != 513 {
		t.Fatalf("the capture holds %d voice records", len(want))
	}

	sink := &gateways{}
	l, _ := start(t, Config{Gateways: sink})
	c := dial(t, l)
	go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(c) }()
	raw := readFile(t, voiceFixture)
	if _, err := c.Write(raw); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitFor(t, "three calls ending at the gateways", func() bool { _, ends := sink.counts(); return ends == 3 })

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.frames) != len(want) {
		t.Fatalf("the gateways were sent %d frames, want %d", len(sink.frames), len(want))
	}
	for i := range want {
		if !bytes.Equal(sink.frames[i], want[i]) {
			t.Fatalf("frame %d changed on the way:\n in  % x\n out % x", i, want[i], sink.frames[i])
		}
	}
	if l.Relayed() != 513 || l.Held() != 0 {
		t.Errorf("%d relayed, %d held", l.Relayed(), l.Held())
	}
	if r := l.Repeaters(); len(r) != 1 || r[0].Relayed != 513 {
		t.Errorf("the repeater's own count: %+v", r)
	}
}

// One repeater's call, as another repeater receives it.
//
// Break it: send the talker its own call back, drop the markers, or change a
// byte, and this fails.
func TestACallReachesAnotherRepeaterAsItWasSent(t *testing.T) {
	tests := []struct {
		name string
		send []byte
		want []byte
	}{
		{"as a repeater sends it",
			join(callStart, voiceRecord(1), voiceRecord(2), callEnd, callEnd),
			join(callStart, voiceRecord(1), voiceRecord(2), callEnd, callEnd)},
		{"joined part-way, and given a start",
			join(voiceRecord(1), voiceRecord(2), callEnd, callEnd),
			join(callStart, voiceRecord(1), voiceRecord(2), callEnd, callEnd)},
		{"a header is carried too",
			join(callStart, tunnel(append([]byte{0x07, 0x03, 0x60}, make([]byte, 29)...)), voiceRecord(1), callEnd),
			join(callStart, tunnel(append([]byte{0x07, 0x03, 0x60}, make([]byte, 29)...)), voiceRecord(1), callEnd, callEnd)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := start(t, Config{Keepalive: time.Hour})
			talker := link(t, l, 1)
			listener := link(t, l, 3)
			waitFor(t, "both links up", func() bool { return l.LinksUp() == 2 })

			_, _ = talker.Write(tc.send)
			expect(t, listener, tc.want)
			silent(t, listener)
			silent(t, talker)
		})
	}
}

// Break it: carry a repeater whose link is not open, and frames go to a
// station that has not finished saying what it is.
func TestOnlyALinkedRepeaterIsSentACall(t *testing.T) {
	l, _ := start(t, Config{Keepalive: time.Hour})
	talker := link(t, l, 1)
	half := dial(t, l)
	_, _ = half.Write(linkRequest)
	expect(t, half, join(linkAnswer, ourRequest))

	_, _ = talker.Write(join(callStart, voiceRecord(1), callEnd, callEnd))
	waitFor(t, "the call finishing", func() bool { return l.Calls() == 1 })
	silent(t, half)
}

// One call at a time, across repeaters and the gateway side.
//
// Break it: carry whoever is talking, and two calls reach the gateways
// interleaved; ask for the floor on every frame, and the late caller takes
// over the moment the first one ends.
func TestOneCallAtATimeIsCarried(t *testing.T) {
	sink := &gateways{}
	floor := &p25link.Floor{}
	l, _ := start(t, Config{Keepalive: time.Hour, Gateways: sink, Floor: floor})
	a := link(t, l, 1)
	b := link(t, l, 3)
	waitFor(t, "both links up", func() bool { return l.LinksUp() == 2 })
	go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(a) }()
	go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(b) }()
	frames := func(n int) func() bool {
		return func() bool { f, _ := sink.counts(); return f == n }
	}

	// A talks and is carried.
	_, _ = a.Write(join(callStart, voiceRecord(0xA1)))
	waitFor(t, "A's first frame at the gateways", frames(1))

	// B keys over A: heard, counted, not carried.
	_, _ = b.Write(join(callStart, voiceRecord(0xB1)))
	waitFor(t, "B's call being held", func() bool { return l.Held() == 1 })

	// A ends. B is still talking, and stays uncarried to the end of its call.
	_, _ = a.Write(join(voiceRecord(0xA2), callEnd, callEnd))
	waitFor(t, "A's call ending at the gateways", func() bool { _, e := sink.counts(); return e == 1 })
	_, _ = b.Write(join(voiceRecord(0xB2), callEnd, callEnd))
	waitFor(t, "B's call finishing", func() bool { return l.Calls() == 2 })

	// A gateway talking holds the floor against a repeater too.
	floor.Take(p25link.GatewayFloor, time.Now())
	_, _ = b.Write(join(callStart, voiceRecord(0xB3), callEnd, callEnd))
	waitFor(t, "B's second call being held", func() bool { return l.Held() == 2 })
	floor.Release(p25link.GatewayFloor)

	// And with the floor free, B is carried.
	_, _ = b.Write(join(callStart, voiceRecord(0xB4), callEnd, callEnd))
	waitFor(t, "B's third call at the gateways", func() bool { _, e := sink.counts(); return e == 2 })

	sink.mu.Lock()
	defer sink.mu.Unlock()
	var got []byte
	for _, f := range sink.frames {
		got = append(got, f[1])
	}
	if want := []byte{0xA1, 0xA2, 0xB4}; !bytes.Equal(got, want) {
		t.Errorf("the gateways were sent % x, want % x", got, want)
	}
	if l.Held() != 2 {
		t.Errorf("%d calls held", l.Held())
	}
}

// Break it: leave a carried call open when its repeater goes quiet or its
// tunnel closes, and the gateways are never told it ended and nobody else
// can take the floor.
func TestACarriedCallThatIsCutOffIsStillEnded(t *testing.T) {
	tests := []struct {
		name string
		cut  func(c interface{ Close() error })
	}{
		{"the repeater goes quiet", func(interface{ Close() error }) {}},
		{"the tunnel closes", func(c interface{ Close() error }) { _ = c.Close() }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := &gateways{}
			floor := &p25link.Floor{}
			l, _ := start(t, Config{Keepalive: time.Hour, Request: 50 * time.Millisecond, Gateways: sink, Floor: floor})
			c := dial(t, l)
			go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(c) }()
			_, _ = c.Write(join(callStart, voiceRecord(1)))
			waitFor(t, "the frame at the gateways", func() bool { f, _ := sink.counts(); return f == 1 })

			tc.cut(c)
			waitFor(t, "the call being ended at the gateways", func() bool { _, e := sink.counts(); return e == 1 })
			if !floor.Take("somebody else", time.Now()) {
				t.Error("the floor was not given back")
			}
		})
	}
}
