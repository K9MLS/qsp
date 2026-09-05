package ipscbridge_test

import (
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// asPrivate rewrites captured group frames as the private calls they would have
// been.
//
// **The rewrite is one byte because the difference is one byte.**
// ipsc-private-voice.pcap establishes that a private transmission differs from
// a group one in its leading byte, its destination and the FLCO inside the Link
// Control, and in nothing else: same lengths, same superframe cycle, same slot
// bit, same frame markers. So audio captured as a group call is the right input
// for measuring what this converter does with the call type, and using it keeps
// the vocoder payload real rather than invented.
func asPrivate(in []ipsc.Message, destination uint32) []ipsc.Message {
	out := make([]ipsc.Message, 0, len(in))
	for _, m := range in {
		body := append([]byte(nil), m.Body...)
		body[4] = byte(destination >> 16)
		body[5] = byte(destination >> 8)
		body[6] = byte(destination)
		out = append(out, ipsc.Message{Kind: ipsc.KindVoicePrivate, SenderID: m.SenderID, Body: body})
	}
	return out
}

// TestAPrivateCallCrossesAsAPrivateCall is the point of decoding 0x81 at all.
//
// A private destination delivered as a group call would put one member's
// conversation on a talkgroup for everybody to hear, which is worse than not
// carrying it. Every frame of the transmission has to agree — the header, the
// audio and the terminator are three separate constructions of hbp.Data.
func TestAPrivateCallCrossesAsAPrivateCall(t *testing.T) {
	const target = 3155373
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	if err != nil {
		t.Fatalf("%v", err)
	}
	var frames []hbp.Data
	for _, m := range asPrivate(voiceMessages(t), target) {
		frames = append(frames, c.Convert(m, 3132910)...)
	}
	if len(frames) == 0 {
		t.Fatal("a private transmission produced no frames at all")
	}
	for i, f := range frames {
		if f.CallType != hbp.CallPrivate {
			t.Fatalf("frame %d of a private call is marked %v, want a private call", i, f.CallType)
		}
		if f.TargetID != target {
			t.Fatalf("frame %d is addressed to %d, want the radio %d", i, f.TargetID, target)
		}
	}
}

// TestAPrivateCallCarriesAPrivateLinkControl is the same fact one layer down.
//
// The call type is inside the burst as well as beside it: a radio that joins
// mid-transmission learns the call type from the Link Control and from nothing
// else. Marking the Homebrew frame private while building a group Link Control
// would satisfy every routing decision QSP makes and still tell every radio on
// the far end the wrong thing.
func TestAPrivateCallCarriesAPrivateLinkControl(t *testing.T) {
	const target = 3155373
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	if err != nil {
		t.Fatalf("%v", err)
	}
	var checked int
	for _, m := range asPrivate(voiceMessages(t), target) {
		for _, f := range c.Convert(m, 3132910) {
			if f.FrameType != hbp.FrameTypeSync {
				continue
			}
			payload, _, ok := dmrfec.DecodeBPTC(f.Payload[:])
			if !ok {
				t.Fatal("a burst this bridge built could not be decoded")
			}
			lc, ok := dmrfec.CheckLinkControl(payload, f.DataType)
			if !ok {
				continue
			}
			if lc[0] != dmrfec.FLCOPrivateVoice {
				t.Errorf("a private call's Link Control carries FLCO %#02x, want %#02x",
					lc[0], dmrfec.FLCOPrivateVoice)
			}
			if got := uint32(lc[3])<<16 | uint32(lc[4])<<8 | uint32(lc[5]); got != target {
				t.Errorf("the Link Control names %d, want the radio %d", got, target)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no header or terminator carried a Link Control to check")
	}
}

// TestAGroupCallIsStillAGroupCall is the control.
//
// The call type became a parameter in this patch, and a parameter threaded
// through three constructions is a parameter that can be threaded wrongly.
func TestAGroupCallIsStillAGroupCall(t *testing.T) {
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, m := range voiceMessages(t) {
		for i, f := range c.Convert(m, 3132910) {
			if f.CallType != hbp.CallGroup {
				t.Fatalf("frame %d of a group call is marked %v", i, f.CallType)
			}
		}
	}
}

// TestAPrivateCallGoesOutAsAPrivateCall is the inferred half.
//
// No capture holds a master sending a private call, so what QSP sends is
// reasoned from what it sends for a group call and from the three things the
// capture shows differ. ADR-0041 built the whole outbound voice path this way
// and it matched a real master on 22 of 24 bytes when one was captured — a
// precedent, not a proof, and recorded as such in the fixture's own notes.
func TestAPrivateCallGoesOutAsAPrivateCall(t *testing.T) {
	e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{ColourCode: 11})
	out := e.Encode(hbp.Data{
		SourceID: 3155413, TargetID: 3132910, Timeslot: hbp.Timeslot2,
		CallType: hbp.CallPrivate, FrameType: hbp.FrameTypeVoiceSync,
		StreamID: 0xC0FFEE,
	})
	if len(out) == 0 {
		t.Skip("a burst with no decodable audio produces nothing, which is correct")
	}
	for _, m := range out {
		if m.Kind != ipsc.KindVoicePrivate {
			t.Errorf("a private call went out as %#02x, want %#02x",
				byte(m.Kind), byte(ipsc.KindVoicePrivate))
		}
		if dst := uint32(m.Body[4])<<16 | uint32(m.Body[5])<<8 | uint32(m.Body[6]); dst != 3132910 {
			t.Errorf("the body names destination %d, want the radio 3132910", dst)
		}
	}
}
