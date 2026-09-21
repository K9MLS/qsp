package vocoderlink

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ambe"
	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

var paceBase = time.Unix(1_700_000_000, 0)

// step is one item arriving at the pacer, at an offset from paceBase.
type step struct {
	kind pacedKind
	at   time.Duration
}

// out is one item the pacer released: when, and whether it was a silence fill.
type out struct {
	kind    pacedKind
	at      time.Duration
	silence bool
}

// run pushes items at their arrival times and releases on a 1 ms clock, as the
// run loop would, and returns what went out, when, and how many slots it filled.
func run(steps []step, until time.Duration) (released []out, filled int) {
	var p pacer
	tx := &outbound{}
	next := 0
	for t := time.Duration(0); t <= until; t += time.Millisecond {
		for next < len(steps) && steps[next].at <= t {
			item := pacedItem{kind: steps[next].kind, tx: tx, at: paceBase.Add(steps[next].at)}
			if item.kind == pacedVoice {
				item.frames = [][]byte{{1}, {2}, {3}} // a real burst, told apart from silence
			}
			p.push(item)
			next++
		}
		got, f := p.release(paceBase.Add(t))
		filled += f
		for _, item := range got {
			silent := item.kind == pacedVoice && len(item.frames[0]) == dmrfec.ProtectedBits
			released = append(released, out{item.kind, t, silent})
		}
	}
	return released, filled
}

const (
	H = pacedHeader
	V = pacedVoice
	T = pacedTerminator
)

func at(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }

// TestThePacerKeepsARadiosCadenceAndFillsStalls covers the rule in pace.go
// against explicit times.
//
// To see rows fail: advance the slot from the release time instead of from the
// slot; drop the head-start; fill a stall with the last burst rather than
// silence; drop PacerGrace so a burst late by a few milliseconds is replaced;
// or keep filling after the terminator.
func TestThePacerKeepsARadiosCadenceAndFillsStalls(t *testing.T) {
	tests := []struct {
		name       string
		steps      []step
		until      time.Duration
		want       []out
		wantFilled int
	}{
		{
			name:  "the header goes at once, the first burst after the head-start",
			steps: []step{{H, at(0)}, {V, at(10)}, {T, at(11)}},
			until: time.Second,
			want:  []out{{H, at(0), false}, {V, at(120), false}, {T, at(180), false}},
		},
		{
			name:  "audio arriving fast is sent at 60 ms, never faster",
			steps: []step{{H, at(0)}, {V, at(5)}, {V, at(10)}, {V, at(15)}, {T, at(16)}},
			until: time.Second,
			want:  []out{{H, at(0), false}, {V, at(120), false}, {V, at(180), false}, {V, at(240), false}, {T, at(300), false}},
		},
		{
			name:  "a burst late by less than the grace goes as itself",
			steps: []step{{H, at(0)}, {V, at(10)}, {V, at(195)}, {T, at(196)}},
			until: time.Second,
			want:  []out{{H, at(0), false}, {V, at(120), false}, {V, at(195), false}, {T, at(255), false}},
		},
		{
			name:  "a stall past the grace is filled with silence, at the slot's cadence",
			steps: []step{{H, at(0)}, {V, at(10)}, {V, at(250)}, {T, at(251)}},
			until: time.Second,
			// The slot at 180 is empty past its grace, so silence goes at
			// 200; the real burst takes the slot at 240 and arrives at 250.
			want: []out{{H, at(0), false}, {V, at(120), false}, {V, at(200), true},
				{V, at(250), false}, {T, at(310), false}},
			wantFilled: 1,
		},
		{
			name: "a half-second stall is filled slot by slot, as the capture's 526 ms one would be",
			steps: []step{{H, at(0)}, {V, at(10)}, {V, at(11)},
				{V, at(700)}, {T, at(701)}},
			until: 2 * time.Second,
			want: []out{{H, at(0), false}, {V, at(120), false}, {V, at(180), false},
				{V, at(260), true}, {V, at(320), true}, {V, at(380), true}, {V, at(440), true},
				{V, at(500), true}, {V, at(560), true}, {V, at(620), true}, {V, at(680), true},
				// The burst arrives at 700, between the last fill at 680 and
				// the next slot at 720, and waits for the slot: sending it at
				// arrival would put two bursts 20 ms apart. The first version
				// of this row expected 700, which was the row's mistake.
				{V, at(720), false}, {T, at(780), false}},
			wantFilled: 8,
		},
		{
			name:  "nothing is filled after the terminator",
			steps: []step{{H, at(0)}, {V, at(10)}, {T, at(11)}},
			until: 2 * time.Second,
			want:  []out{{H, at(0), false}, {V, at(120), false}, {T, at(180), false}},
		},
		{
			name: "a second transmission waits its turn, and the gap between is not filled",
			steps: []step{{H, at(0)}, {V, at(5)}, {T, at(6)},
				{H, at(900)}, {V, at(901)}, {T, at(902)}},
			until: 2 * time.Second,
			want: []out{{H, at(0), false}, {V, at(120), false}, {T, at(180), false},
				{H, at(900), false}, {V, at(1020), false}, {T, at(1080), false}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, filled := run(tc.steps, tc.until)
			if len(got) != len(tc.want) {
				t.Fatalf("released %d items %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("item %d: got %+v, want %+v", i, got[i], tc.want[i])
				}
			}
			if filled != tc.wantFilled {
				t.Errorf("filled %d slots, want %d", filled, tc.wantFilled)
			}
		})
	}
}

// TestNothingIsSentBeforeATransmissionOpens: an idle pacer has nothing due, so
// the run loop's timer stays disarmed and no silence goes to a network that has
// not been sent a header.
func TestNothingIsSentBeforeATransmissionOpens(t *testing.T) {
	var p pacer
	if _, ok := p.due(); ok {
		t.Fatal("an idle pacer has something due")
	}
	if got, _ := p.release(paceBase.Add(time.Hour)); len(got) != 0 {
		t.Errorf("an idle pacer released %d items", len(got))
	}
}

// TestShutdownSendsEverythingIncludingTheTerminator: a repeater sent a header
// and no terminator stays keyed.
func TestShutdownSendsEverythingIncludingTheTerminator(t *testing.T) {
	var p pacer
	tx := &outbound{}
	for _, k := range []pacedKind{H, V, V, T} {
		p.push(pacedItem{kind: k, tx: tx, at: paceBase})
	}
	got := p.flush()
	if len(got) != 4 || got[3].kind != T {
		t.Fatalf("flush returned %d items, want all 4 ending with the terminator", len(got))
	}
	if _, ok := p.due(); ok {
		t.Error("items remain, or a transmission stays open, after a flush")
	}
}

// TestProductionsZelloTimingComesOutAtSixtyMilliseconds replays the timing QSP
// actually sent a Motorola repeater on 2026-09-21: two calls before pacing, and
// the 526 ms stall seen after it.
//
// **The gaps are from captures, the captures are not here.** They hold a
// repeater's public address, and this repository publishes none.
//
// The fixture is checked before it is used, because a replay of tidy timing
// would pass without proving anything.
func TestProductionsZelloTimingComesOutAtSixtyMilliseconds(t *testing.T) {
	captured := map[string][]int{
		"a 1.7 s call": {51, 45, 55, 71, 59, 67, 61, 51, 51, 51, 48, 60, 59, 113, 51, 51, 53,
			51, 51, 51, 64, 55, 82, 48, 59, 87, 51, 48},
		"a 3.5 s call": {51, 51, 47, 51, 48, 51, 51, 45, 48, 51, 45, 48, 51, 48, 48, 51, 46, 48,
			177, 51, 47, 45, 150, 51, 48, 45, 48, 51, 51, 48, 44, 51, 50, 49, 46, 49, 47, 45,
			48, 51, 48, 48, 51, 51, 51, 48, 51, 63, 55, 219, 47, 48, 48, 51, 52, 51, 51, 49,
			117, 51, 53},
		"the 526 ms stall": {60, 60, 60, 60, 60, 60, 526, 60, 60, 60, 60, 60},
	}

	for name, gaps := range captured {
		t.Run(name, func(t *testing.T) {
			stalls := 0
			for _, g := range gaps {
				if g > 100 {
					stalls++
				}
			}
			if stalls == 0 {
				t.Fatal("the fixture holds no stall; it no longer reproduces the fault")
			}

			steps := []step{{H, at(0)}, {V, at(5)}}
			a := 5
			for _, g := range gaps {
				a += g
				steps = append(steps, step{V, at(a)})
			}
			steps = append(steps, step{T, at(a + 3)})

			got, _ := run(steps, 20*time.Second)

			// **No gap a repeater would run dry in.** A fill is released a
			// grace after its slot, so the spacing around one is 60 ± 20 ms;
			// what matters is that nothing waits long enough to empty a
			// repeater's buffer, and that the average stays a radio's.
			var voice []time.Duration
			for _, r := range got {
				if r.kind == V {
					voice = append(voice, r.at)
				}
			}
			for i := 1; i < len(voice); i++ {
				if g := voice[i] - voice[i-1]; g > BurstInterval+PacerGrace {
					t.Errorf("burst %d went %v after the one before it; a repeater runs dry past %v",
						i, g, BurstInterval+PacerGrace)
				}
			}
			reals := 0
			for _, r := range got {
				if r.kind == V && !r.silence {
					reals++
				}
			}
			if reals != len(gaps)+1 {
				t.Errorf("%d real bursts went out, want %d: audio was lost or duplicated", reals, len(gaps)+1)
			}
		})
	}
}

// TestTheChannelQueuesRatherThanSendingAtOnce is the channel-level half: every
// pacer test above would still pass if the channel delivered straight to the
// network, which is the fault 0426 fixed.
func TestTheChannelQueuesRatherThanSendingAtOnce(t *testing.T) {
	chip := &fakeChip{rate: ambe.RateIndexDMR}
	got := &delivered{}
	ch := outboundChannel(t, chip, &fakeRadio{}, got, 0)

	start := time.Now()
	var tx *outbound
	tx = ch.handleUSRP(tx, nil, usrpKeyup, start)
	for i := range 3 * dmrfec.FramesPerBurst {
		tx = ch.handleUSRP(tx, nil, pcm(int16(i+1)), start)
	}
	ch.releasePaced(time.Now())

	if k := kinds(got.snapshot()); k != "H" {
		t.Fatalf("delivered %q straight away, want only the header; voice must wait for its slot", k)
	}
	ch.releasePaced(time.Now().Add(time.Second))
	if k := kinds(got.snapshot()); k[:4] != "HABC" {
		t.Errorf("after a second of slots, delivered %q, want the header and three bursts first", k)
	}
	ch.handleUSRP(tx, nil, usrpRelease, time.Now())
	ch.flushPaced()
	if k := kinds(got.snapshot()); k[len(k)-1] != 'T' {
		t.Errorf("the transmission ended %q, want a terminator last", k)
	}
}

// TestASilentFillKeepsTheTransmissionWhole checks what a radio receives across
// a stall: silence that decodes as silence, positions with no skip, and the
// Link Control and alias cycle intact on both sides.
//
// **This is why assembly moved to release.** Had a silent burst been inserted
// between bursts already assembled, everything after it would carry the wrong
// position, and the embedded signalling would reassemble into nothing.
func TestASilentFillKeepsTheTransmissionWhole(t *testing.T) {
	chip := &fakeChip{rate: ambe.RateIndexDMR}
	got := &delivered{}
	ch := aliasChannel(t, chip, &fakeRadio{}, got, "KD9BXO")

	start := time.Now()
	var tx *outbound
	tx = ch.handleUSRP(tx, nil, usrpKeyup, start)
	feed := func(bursts int) {
		for i := range bursts * dmrfec.FramesPerBurst {
			tx = ch.handleUSRP(tx, nil, pcm(int16(i+1)), time.Now())
		}
	}

	feed(8)
	ch.releasePaced(time.Now().Add(time.Hour)) // everything queued goes, then a long stall is filled up to now+1h
	stallEnd := len(got.snapshot())
	if stallEnd < 20 {
		t.Fatalf("only %d frames after a long stall; the stall was not filled", stallEnd)
	}
	feed(12)
	ch.handleUSRP(tx, nil, usrpRelease, time.Now())
	ch.flushPaced()
	frames := got.snapshot()

	silent, positions := 0, 0
	for i, f := range frames {
		if f.FrameType != hbp.FrameTypeVoice && f.FrameType != hbp.FrameTypeVoiceSync {
			continue
		}
		if want := uint8(positions % dmrfec.SuperframeBursts); f.DataType != want {
			t.Fatalf("frame %d is at position %d, want %d: a fill broke the superframe", i, f.DataType, want)
		}
		positions++
		if isSilentBurst(f) {
			silent++
		}
	}
	if silent == 0 {
		t.Fatal("no burst decodes as the silence frame")
	}

	pdus := embeddedPDUs(t, frames)
	alias, ok := dmrfec.TalkerAliasFrom(aliasOnly(pdus))
	if !ok || alias != "KD9BXO" {
		t.Errorf("the alias across a fill decodes as %q, %v", alias, ok)
	}
	lcs := 0
	for _, p := range pdus {
		if !isAliasPDU(p) {
			lcs++
		}
	}
	if lcs < 2 {
		t.Errorf("the Link Control decoded %d times across a fill; it must survive it", lcs)
	}
}

// isSilentBurst reports whether all three vocoder frames of a burst decode to
// the measured silence frame.
func isSilentBurst(f hbp.Data) bool {
	frames, ok := dmrfec.VocoderFrames(f.Payload[:])
	if !ok {
		return false
	}
	for _, fr := range frames {
		p, _, ok := dmrfec.Decode(fr)
		if !ok || p != silenceParameters {
			return false
		}
	}
	return true
}

// TestTheSilenceFrameIsWhatRealEquipmentSends checks silenceParameters against
// the captures it came from, rather than against itself.
//
// **The first version of this package's tests compared silent bursts with
// silenceParameters**, the very constant that built them, so a wrong value
// passed every test: a deliberate break changed it and nothing failed. The
// constant's authority is the capture evidence -- internal/dmrfec records the
// silence frame as the most common one in the Homebrew captures, where a real
// MMDVM hotspot carried long stretches of nobody talking -- so that is what
// this reads.
func TestTheSilenceFrameIsWhatRealEquipmentSends(t *testing.T) {
	counts := map[dmrfec.Parameters]int{}
	for _, path := range []string{
		"../../testdata/hbp/hbp-voice-live.pcap",
		"../../testdata/hbp/hbp-voice-session.pcap",
	} {
		for _, payload := range homebrewVoicePayloads(t, path) {
			frames, ok := dmrfec.VocoderFrames(payload)
			if !ok {
				continue
			}
			for _, f := range frames {
				if p, _, ok := dmrfec.Decode(f); ok {
					counts[p]++
				}
			}
		}
	}
	var commonest dmrfec.Parameters
	for p, n := range counts {
		if n > counts[commonest] {
			commonest = p
		}
	}
	if counts[commonest] < 100 {
		t.Fatalf("the commonest frame appears %d times; the captures no longer hold enough silence to say", counts[commonest])
	}
	if silenceParameters != commonest {
		t.Errorf("silenceParameters is %#x; the frame real equipment sends most, %d times, is %#x",
			uint64(silenceParameters), counts[commonest], uint64(commonest))
	}
}

// homebrewVoicePayloads reads the voice burst payloads out of a capture.
func homebrewVoicePayloads(t *testing.T, path string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	ipAt := map[uint32]int{1: 14, 113: 16, 276: 20}[binary.LittleEndian.Uint32(raw[20:24])]
	var out [][]byte
	for off := 24; off+16 <= len(raw); {
		n := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+n > len(raw) {
			break
		}
		rec := raw[off : off+n]
		off += n
		if ipAt == 0 || len(rec) < ipAt+28 || rec[ipAt+9] != 17 {
			continue
		}
		ip := rec[ipAt:]
		m, err := hbp.Parse(ip[int(ip[0]&0x0f)*4+8:])
		if err != nil {
			continue
		}
		if d, ok := m.(hbp.Data); ok && (d.FrameType == hbp.FrameTypeVoice || d.FrameType == hbp.FrameTypeVoiceSync) {
			out = append(out, append([]byte(nil), d.Payload[:]...))
		}
	}
	return out
}
