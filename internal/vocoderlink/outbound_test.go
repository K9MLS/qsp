package vocoderlink

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ambe"
	"github.com/k9mls/qsp/internal/audio"
	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

const gatewayID = 3100999

// delivered records every burst a channel hands to routing.
type delivered struct {
	mu     sync.Mutex
	frames []hbp.Data
}

func (d *delivered) add(f hbp.Data) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frames = append(d.frames, f)
}

func (d *delivered) snapshot() []hbp.Data {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.frames)
}

func outboundChannel(t *testing.T, chip Chip, radio Radio, out *delivered, idle time.Duration) *Channel {
	t.Helper()
	ch, err := New(Options{Name: "dvstick", Chip: func() Chip { return chip }, Radio: radio,
		RadioID: gatewayID, Talkgroup: 2, Timeslot: hbp.Timeslot2, Deliver: out.add, Idle: idle})
	if err != nil {
		t.Fatalf("building a channel: %v", err)
	}
	return ch
}

func pcm(n int16) audio.Frame {
	s := make([]int16, audio.SamplesPerFrame)
	s[0] = n
	return audio.Frame{PTT: true, Talkgroup: 2, Samples: s}
}

var (
	usrpKeyup   = audio.Frame{PTT: true, Talkgroup: 2}
	usrpRelease = audio.Frame{PTT: false, Talkgroup: 2}
)

// kinds reduces deliveries to H (header), A-F (voice by position) and T.
func kinds(frames []hbp.Data) string {
	var b strings.Builder
	for _, f := range frames {
		switch {
		case f.FrameType == hbp.FrameTypeSync && f.DataType == dmrfec.DataTypeVoiceLCHeader:
			b.WriteByte('H')
		case f.FrameType == hbp.FrameTypeSync && f.DataType == dmrfec.DataTypeTerminatorWithLC:
			b.WriteByte('T')
		case f.FrameType == hbp.FrameTypeVoiceSync || f.FrameType == hbp.FrameTypeVoice:
			b.WriteByte('A' + f.DataType)
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

// TestUSRPAudioBecomesAWholeDMRTransmission checks the transmission shape.
//
// Each row is USRP input and the burst sequence routing must receive.
// To see rows fail, break the implementation deliberately:
//   - drop the silence padding in endOutbound: "a partial last burst" loses
//     its final C and the last word of every over
//   - skip terminate in endOutbound: every row ends without T and repeaters
//     hang keyed
//   - reset position to 0 on every burst: "a whole superframe" reads AAAAAAA
func TestUSRPAudioBecomesAWholeDMRTransmission(t *testing.T) {
	frames := func(n int) []audio.Frame {
		out := []audio.Frame{usrpKeyup}
		for i := range n {
			out = append(out, pcm(int16(i+1)))
		}
		return append(out, usrpRelease)
	}
	tests := []struct {
		name        string
		in          []audio.Frame
		want        string
		wantEncoded int
	}{
		{"a keyup and a release with nothing between", frames(0), "HT", 0},
		{"one exact burst", frames(3), "HAT", 3},
		{"a partial last burst is padded, not dropped", frames(7), "HABCT", 9},
		{"a whole superframe and into the next", frames(20), "HABCDEFAT", 21},
		{"audio with no keyup opens the transmission", frames(3)[1:], "HAT", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chip := &fakeChip{rate: ambe.RateIndexDMR}
			out := &delivered{}
			ch := outboundChannel(t, chip, &fakeRadio{}, out, 0)
			var tx *outbound
			for _, f := range tc.in {
				tx = ch.handleUSRP(tx, nil, f, time.Now())
			}
			ch.flushPaced()
			got := out.snapshot()
			if k := kinds(got); k != tc.want {
				t.Errorf("bursts %q, want %q", k, tc.want)
			}
			if len(chip.encoded) != tc.wantEncoded {
				t.Errorf("encoded %d frames, want %d (padding included)", len(chip.encoded), tc.wantEncoded)
			}
			if chip.held {
				t.Error("the chip is still held after the release; every later call is refused")
			}
			for i, f := range got {
				if f.SourceID != gatewayID || f.TargetID != 2 || f.Timeslot != hbp.Timeslot2 ||
					f.CallType != hbp.CallGroup || f.StreamID != got[0].StreamID || f.Sequence != uint8(i) {
					t.Fatalf("burst %d is %+v; every burst must carry the gateway's ID, TG2 TS2, "+
						"one stream and consecutive sequence numbers", i, f)
				}
			}
		})
	}
}

// TestTheBuiltTransmissionDecodesAsDMR reads a built transmission back with
// the decoders that were proved against real radios.
//
// To see it bite: put tx.middles[tx.position] (off by one) in emitVoice, or
// build the header with a source of 0; the embedded LC or header LC fails.
func TestTheBuiltTransmissionDecodesAsDMR(t *testing.T) {
	chip := &fakeChip{rate: ambe.RateIndexDMR}
	out := &delivered{}
	ch := outboundChannel(t, chip, &fakeRadio{}, out, 0)
	var tx *outbound
	tx = ch.handleUSRP(tx, nil, usrpKeyup, time.Now())
	for i := range 18 {
		tx = ch.handleUSRP(tx, nil, pcm(int16(i+1)), time.Now())
	}
	ch.handleUSRP(tx, nil, usrpRelease, time.Now())
	ch.flushPaced()

	got := out.snapshot()
	if kinds(got) != "HABCDEFT" {
		t.Fatalf("bursts %q", kinds(got))
	}
	wantLC := dmrfec.LinkControlFor(2, gatewayID, false)

	for _, i := range []int{0, 7} {
		cc, dt, ok := dmrfec.SlotTypeOf(got[i].Payload[:])
		if !ok || cc != GeneratedColourCode || dt != got[i].DataType {
			t.Errorf("burst %d slot type reads cc %d data type %d ok %v", i, cc, dt, ok)
		}
		payload, _, ok := dmrfec.DecodeBPTC(got[i].Payload[:])
		if !ok {
			t.Fatalf("burst %d is not a valid BPTC block", i)
		}
		lc, ok := dmrfec.CheckLinkControl(payload, got[i].DataType)
		if !ok || !slices.Equal(lc, wantLC) {
			t.Errorf("burst %d carries LC %x (ok %v), want %x", i, lc, ok, wantLC)
		}
	}

	if m, _ := dmrfec.Middle(got[1].Payload[:]); m != dmrfec.VoiceSyncBS {
		t.Errorf("burst A carries %012x, want the voice sync pattern", m)
	}
	var fragments [dmrfec.EmbeddedLCBursts]uint32
	for pos := 1; pos <= 5; pos++ {
		m, _ := dmrfec.Middle(got[pos+1].Payload[:])
		emb, fragment := dmrfec.SplitMiddle(m)
		lcss, _ := dmrfec.LCSSForPosition(pos)
		if !dmrfec.ValidEMB(emb) || dmrfec.ColourCodeOf(emb) != GeneratedColourCode || dmrfec.LCSSOf(emb) != lcss {
			t.Errorf("burst %c EMB %04x: valid %v cc %d lcss %d, want lcss %d", 'A'+pos, emb,
				dmrfec.ValidEMB(emb), dmrfec.ColourCodeOf(emb), dmrfec.LCSSOf(emb), lcss)
		}
		if pos <= dmrfec.EmbeddedLCBursts {
			fragments[pos-1] = fragment
		} else if fragment != 0 {
			t.Errorf("burst F carries fragment %08x; every capture shows none", fragment)
		}
	}
	if lc, ok := dmrfec.DecodeEmbeddedLC(fragments); !ok || !slices.Equal(lc, wantLC) {
		t.Errorf("embedded LC decodes as %x (ok %v), want %x", lc, ok, wantLC)
	}

	// And the audio is the chip's frames, in order, with nothing mixed in.
	for pos := 0; pos < 6; pos++ {
		vf, _ := dmrfec.VocoderFrames(got[pos+1].Payload[:])
		for j, bits := range vf {
			want := dmrfec.Encode(dmrfec.Parameters(pos*3 + j + 1))
			if !slices.Equal(bits, want) {
				t.Fatalf("burst %c frame %d is not encode %d", 'A'+pos, j+1, pos*3+j+1)
			}
		}
	}
}

// TestUSRPKeyingIsRefusedWhileTheNetworkIsTalking, both ways.
//
// To see it bite: delete the cur.chip check in startOutbound, and the Run
// guard on tx in the DMR case.
func TestUSRPKeyingIsRefusedWhileTheNetworkIsTalking(t *testing.T) {
	t.Run("a Zello keyup during a DMR call", func(t *testing.T) {
		chip := &fakeChip{rate: ambe.RateIndexDMR}
		out := &delivered{}
		radio := &fakeRadio{}
		ch := outboundChannel(t, chip, radio, out, 0)
		cur := ch.handle(nil, header(1), time.Now())
		var tx *outbound
		for _, f := range []audio.Frame{usrpKeyup, pcm(1), pcm(2), pcm(3), usrpRelease} {
			tx = ch.handleUSRP(tx, cur, f, time.Now())
		}
		ch.flushPaced()
		if n := len(out.snapshot()); n != 0 {
			t.Errorf("%d bursts built over a DMR call in progress", n)
		}
		if ch.txRefused.Load() != 1 {
			t.Errorf("tx refused %d, want 1", ch.txRefused.Load())
		}
		if !chip.held {
			t.Error("the refused keyup released the DMR call's chip")
		}
	})

	t.Run("a DMR call during a Zello transmission", func(t *testing.T) {
		chip := &fakeChip{rate: ambe.RateIndexDMR}
		out := &delivered{}
		radio := newScriptedRadio()
		ch := outboundChannel(t, chip, radio, out, 0)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go ch.Run(ctx)

		radio.in <- usrpKeyup
		radio.in <- pcm(1)
		waitUntil(t, "the Zello transmission to start", func() bool { return len(out.snapshot()) >= 1 })
		_ = ch.Send(header(9))
		_ = ch.Send(voiceBurst(t, 9))
		waitUntil(t, "the DMR call to be refused", func() bool { return ch.refused.Load() == 1 })
		if s := radio.shape(); s != "" {
			t.Errorf("the DMR call reached USRP as %q while Zello held the chip", s)
		}
	})
}

// TestAUSRPTransmissionThatStopsGetsATerminator: qsp-zello crashing mid-over
// sends no release, and a repeater with no terminator hangs keyed.
//
// To see it bite: delete the tx case from Run's tick handler.
func TestAUSRPTransmissionThatStopsGetsATerminator(t *testing.T) {
	chip := &fakeChip{rate: ambe.RateIndexDMR}
	out := &delivered{}
	radio := newScriptedRadio()
	ch := outboundChannel(t, chip, radio, out, 40*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ch.Run(ctx)
	for _, f := range []audio.Frame{usrpKeyup, pcm(1), pcm(2), pcm(3)} {
		radio.in <- f
	}
	waitUntil(t, "a terminator", func() bool { return strings.HasSuffix(kinds(out.snapshot()), "T") })
	if kinds(out.snapshot()) != "HAT" {
		t.Errorf("bursts %q, want HAT", kinds(out.snapshot()))
	}
	if ch.txAbandoned.Load() != 1 {
		t.Errorf("tx abandoned %d, want 1", ch.txAbandoned.Load())
	}
}

// TestAFrameFailingDMRFECIsCounted: the bench question "did the chip produce
// DMR" answered by a number rather than by listening to a silent repeater.
func TestAFrameFailingDMRFECIsCounted(t *testing.T) {
	chip := &fakeChip{rate: ambe.RateIndexDMR, badFEC: true}
	// The guard that found Decode's ok too lenient to count with: it is false
	// only when both Golay blocks are beyond repair.
	if _, corrected, ok := dmrfec.Decode(unpackBits([]byte{0xff, 0, 0xff, 0, 0xff, 0, 0xff, 0, 0xff}, 72)); ok && corrected == 0 {
		t.Fatal("the fake's bad frame needs no correction; this test would prove nothing")
	}
	if _, corrected, ok := dmrfec.Decode(dmrfec.Encode(1)); !ok || corrected != 0 {
		t.Fatal("a clean encoded frame needs correction; the counter would count every good frame")
	}
	out := &delivered{}
	ch := outboundChannel(t, chip, &fakeRadio{}, out, 0)
	var tx *outbound
	for _, f := range []audio.Frame{usrpKeyup, pcm(1), pcm(2), pcm(3), usrpRelease} {
		tx = ch.handleUSRP(tx, nil, f, time.Now())
	}
	if ch.txBadFEC.Load() != 3 {
		t.Errorf("bad FEC %d, want 3", ch.txBadFEC.Load())
	}
}

// TestAChannelThatBuildsTransmissionsNeedsASource.
func TestAChannelThatBuildsTransmissionsNeedsASource(t *testing.T) {
	_, err := New(Options{Name: "dvstick", Chip: func() Chip { return nil }, Radio: &fakeRadio{},
		Deliver: func(hbp.Data) {}})
	if err == nil {
		t.Fatal("a channel with a Deliver and no radio ID was built; its frames would come from radio 0")
	}
}

// scriptedRadio delivers frames a test pushes, then blocks until cancelled.
type scriptedRadio struct {
	fakeRadio
	in chan audio.Frame
}

func newScriptedRadio() *scriptedRadio { return &scriptedRadio{in: make(chan audio.Frame, 64)} }

func (r *scriptedRadio) Receive(ctx context.Context) (audio.Frame, error) {
	select {
	case f := <-r.in:
		return f, nil
	case <-ctx.Done():
		return audio.Frame{}, ctx.Err()
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
