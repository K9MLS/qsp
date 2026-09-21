package vocoderlink

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ambe"
	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

var (
	paceHeader = hbp.Data{FrameType: hbp.FrameTypeSync, DataType: dmrfec.DataTypeVoiceLCHeader}
	paceVoice  = hbp.Data{FrameType: hbp.FrameTypeVoice}
	paceTerm   = hbp.Data{FrameType: hbp.FrameTypeSync, DataType: dmrfec.DataTypeTerminatorWithLC}
)

// sameKind compares what identifies a frame to the pacer: hbp.Data holds a
// byte slice and cannot be compared whole.
func sameKind(a, b hbp.Data) bool {
	return a.FrameType == b.FrameType && a.DataType == b.DataType
}

// released is one frame and when the pacer let it go.
type released struct {
	f  hbp.Data
	at time.Time
}

// replay pushes frames at their arrival times and releases on a fine clock,
// as the run loop would, returning what went out and when.
func replay(frames []hbp.Data, arrivals []time.Duration, until time.Duration) (out []released, late int) {
	var p pacer
	base := time.Unix(1_700_000_000, 0)
	next := 0
	for t := time.Duration(0); t <= until; t += time.Millisecond {
		now := base.Add(t)
		for next < len(frames) && arrivals[next] <= t {
			p.push(frames[next], base.Add(arrivals[next]))
			next++
		}
		got, l := p.release(now)
		late += l
		for _, f := range got {
			out = append(out, released{f, now})
		}
	}
	return out, late
}

func ms(v ...int) []time.Duration {
	out := make([]time.Duration, len(v))
	for i, x := range v {
		out[i] = time.Duration(x) * time.Millisecond
	}
	return out
}

// TestThePacerKeepsARadiosCadence covers the rule in pace.go, one case at a
// time, against explicit times.
//
// To see rows fail: advance the slot from `now` instead of from when the frame
// was due; drop the head-start after a header; or let a late burst be
// followed by one sent early to catch up.
func TestThePacerKeepsARadiosCadence(t *testing.T) {
	tests := []struct {
		name     string
		frames   []hbp.Data
		arrivals []time.Duration
		until    time.Duration
		want     []time.Duration // release time of each frame
		wantLate int
	}{
		{
			name:     "the header goes at once, the first burst after the head-start",
			frames:   []hbp.Data{paceHeader, paceVoice},
			arrivals: ms(0, 10),
			until:    time.Second,
			want:     ms(0, 120),
		},
		{
			name:     "audio arriving fast is sent at 60 ms, never faster",
			frames:   []hbp.Data{paceHeader, paceVoice, paceVoice, paceVoice, paceVoice},
			arrivals: ms(0, 5, 10, 15, 20),
			until:    time.Second,
			want:     ms(0, 120, 180, 240, 300),
		},
		{
			name:     "a stall past the slot sends the burst on arrival, and counts it",
			frames:   []hbp.Data{paceHeader, paceVoice, paceVoice, paceVoice},
			arrivals: ms(0, 10, 20, 500),
			until:    time.Second,
			want:     ms(0, 120, 180, 500),
			wantLate: 1,
		},
		{
			name:     "after a stall the cadence resumes at 60 ms, with no catch-up clump",
			frames:   []hbp.Data{paceHeader, paceVoice, paceVoice, paceVoice, paceVoice},
			arrivals: ms(0, 10, 500, 501, 502),
			until:    time.Second,
			want:     ms(0, 120, 500, 560, 620),
			wantLate: 1,
		},
		{
			name:     "the terminator follows the last burst by one slot",
			frames:   []hbp.Data{paceHeader, paceVoice, paceTerm},
			arrivals: ms(0, 10, 11),
			until:    time.Second,
			want:     ms(0, 120, 180),
		},
		{
			name: "a second transmission waits its turn behind the first",
			frames: []hbp.Data{paceHeader, paceVoice, paceTerm,
				paceHeader, paceVoice, paceTerm},
			arrivals: ms(0, 5, 6, 7, 8, 9),
			until:    time.Second,
			want:     ms(0, 120, 180, 240, 360, 420),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, late := replay(tc.frames, tc.arrivals, tc.until)
			if len(out) != len(tc.want) {
				t.Fatalf("released %d frames, want %d", len(out), len(tc.want))
			}
			base := time.Unix(1_700_000_000, 0)
			for i, r := range out {
				if got := r.at.Sub(base); got != tc.want[i] {
					t.Errorf("frame %d released at %v, want %v", i, got, tc.want[i])
				}
				if !sameKind(r.f, tc.frames[i]) {
					t.Errorf("frame %d is out of order", i)
				}
			}
			if late != tc.wantLate {
				t.Errorf("counted %d late, want %d", late, tc.wantLate)
			}
		})
	}
}

// TestALateTimerDoesNotDelayEverythingAfterIt: the run loop's timer can fire a
// few milliseconds late, and the next slot must come from when the frame was
// due, or every burst after it inherits the lateness and the cadence drifts.
func TestALateTimerDoesNotDelayEverythingAfterIt(t *testing.T) {
	var p pacer
	base := time.Unix(1_700_000_000, 0)
	p.push(paceHeader, base)
	p.push(paceVoice, base)
	p.push(paceVoice, base)

	p.release(base) // the header
	// The first burst is due at +120; release it 7 ms late.
	got, _ := p.release(base.Add(127 * time.Millisecond))
	if len(got) != 1 {
		t.Fatalf("released %d frames at +127 ms, want 1", len(got))
	}
	when, ok := p.due()
	if !ok || when.Sub(base) != 180*time.Millisecond {
		t.Errorf("the next burst is due at %v, want 180ms: a late timer shifted the cadence",
			when.Sub(base))
	}
}

// TestShutdownSendsEverythingIncludingTheTerminator: a repeater sent a header
// and no terminator stays keyed.
func TestShutdownSendsEverythingIncludingTheTerminator(t *testing.T) {
	var p pacer
	base := time.Unix(1_700_000_000, 0)
	for _, f := range []hbp.Data{paceHeader, paceVoice, paceVoice, paceTerm} {
		p.push(f, base)
	}
	got := p.flush()
	if len(got) != 4 || !sameKind(got[3], paceTerm) {
		t.Fatalf("flush returned %d frames ending %+v, want all 4 ending with the terminator", len(got), got[len(got)-1])
	}
	if _, ok := p.due(); ok {
		t.Error("frames remain queued after a flush")
	}
}

// TestProductionsZelloTimingComesOutAtSixtyMilliseconds replays the timing QSP
// actually sent a Motorola repeater on 2026-09-21, when the operator heard an
// echo.
//
// **The gaps are from a capture, the capture is not here.** It holds a
// repeater's public address, and this repository publishes none. What it
// contributed is the timing: two Zello calls whose voice bursts left QSP at
// these intervals, where real Motorola equipment keeps 57 to 65 ms.
//
// The fixture is checked before it is used, because a replay of tidy timing
// would pass without proving anything: it must hold gaps well under 60 ms and
// stalls well over 100.
func TestProductionsZelloTimingComesOutAtSixtyMilliseconds(t *testing.T) {
	captured := map[string][]int{
		"a 1.7 s call": {51, 45, 55, 71, 59, 67, 61, 51, 51, 51, 48, 60, 59, 113, 51, 51, 53,
			51, 51, 51, 64, 55, 82, 48, 59, 87, 51, 48},
		"a 3.5 s call": {51, 51, 47, 51, 48, 51, 51, 45, 48, 51, 45, 48, 51, 48, 48, 51, 46, 48,
			177, 51, 47, 45, 150, 51, 48, 45, 48, 51, 51, 48, 44, 51, 50, 49, 46, 49, 47, 45,
			48, 51, 48, 48, 51, 51, 51, 48, 51, 63, 55, 219, 47, 48, 48, 51, 52, 51, 51, 49,
			117, 51, 53},
	}

	for name, gaps := range captured {
		t.Run(name, func(t *testing.T) {
			fast, stalls := 0, 0
			for _, g := range gaps {
				if g < 50 {
					fast++
				}
				if g > 100 {
					stalls++
				}
			}
			if fast == 0 || stalls == 0 {
				t.Fatalf("the fixture has %d fast gaps and %d stalls; it no longer reproduces the fault", fast, stalls)
			}

			// A header, a first burst 5 ms later, the captured gaps, then the
			// terminator right behind the last burst as the capture shows.
			frames := []hbp.Data{paceHeader, paceVoice}
			arrivals := ms(0, 5)
			at := 5
			for _, g := range gaps {
				at += g
				frames = append(frames, paceVoice)
				arrivals = append(arrivals, time.Duration(at)*time.Millisecond)
			}
			frames = append(frames, paceTerm)
			arrivals = append(arrivals, time.Duration(at+3)*time.Millisecond)

			out, late := replay(frames, arrivals, 10*time.Second)
			if late != 0 {
				t.Errorf("%d bursts went out late; PacerHeadStart does not cover this call", late)
			}
			var prev time.Time
			for i, r := range out {
				if r.f.FrameType == hbp.FrameTypeVoice || sameKind(r.f, paceTerm) {
					if !prev.IsZero() {
						if g := r.at.Sub(prev); g != BurstInterval {
							t.Errorf("frame %d left %v after the one before it, want %v", i, g, BurstInterval)
						}
					}
					prev = r.at
				}
			}
			// **What it costs.** The last burst goes out later than it
			// arrived by the head-start plus whatever lead the fast runs
			// built; bounded, so pacing cannot grow into a delay nobody
			// would accept.
			lag := out[len(out)-1].at.Sub(time.Unix(1_700_000_000, 0).Add(arrivals[len(arrivals)-1]))
			if lag > 500*time.Millisecond {
				t.Errorf("the transmission ends %v after its audio arrived; pacing is adding too much delay", lag)
			}
		})
	}
}

// TestTheChannelQueuesRatherThanSendingAtOnce is the channel-level half: the
// pacer tests above exercise the pacer alone, and would all still pass if the
// channel delivered straight to the network again, which is the fault fixed.
func TestTheChannelQueuesRatherThanSendingAtOnce(t *testing.T) {
	chip := &fakeChip{rate: ambe.RateIndexDMR}
	out := &delivered{}
	ch := outboundChannel(t, chip, &fakeRadio{}, out, 0)

	start := time.Now()
	var tx *outbound
	tx = ch.handleUSRP(tx, nil, usrpKeyup, start)
	for i := range 3 * dmrfec.FramesPerBurst {
		tx = ch.handleUSRP(tx, nil, pcm(int16(i+1)), start)
	}
	ch.releasePaced(time.Now())

	if got := kinds(out.snapshot()); got != "H" {
		t.Fatalf("delivered %q straight away, want only the header; voice must wait for its slot", got)
	}
	ch.releasePaced(time.Now().Add(time.Second))
	if got := kinds(out.snapshot()); got != "HABC" {
		t.Errorf("after a second of slots, delivered %q, want the header and three bursts", got)
	}
	ch.handleUSRP(tx, nil, usrpRelease, time.Now())
	ch.flushPaced()
	if got := kinds(out.snapshot()); got[len(got)-1] != 'T' {
		t.Errorf("the transmission ended %q, want a terminator last", got)
	}
}
