package ipsc_test

import (
	"testing"

	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// The capture that named 0x81. See testdata/ipsc/ipsc-private-voice.md.
const privateVoice = "../../../testdata/ipsc/ipsc-private-voice.pcap"

// The two radios in that capture, each of which is the source of one private
// transmission and the destination of the other.
const (
	k9mls  = 3132910
	kd9hdr = 3155373
)

// transmission is one run of frames sharing a kind, a sender and a stream.
type transmission struct {
	kind        ipsc.Kind
	sender      uint32
	source      uint32
	destination uint32
	private     bool
	frames      int
}

// transmissions groups a capture's voice frames by the repeater that sent them
// and the stream they belong to.
func transmissions(tb testing.TB, path string) []transmission {
	tb.Helper()
	type key struct {
		kind   ipsc.Kind
		sender uint32
		stream uint16
	}
	var order []key
	seen := map[key]*transmission{}
	for _, pkt := range readCapture(tb, path).UDP {
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			tb.Fatalf("%s packet %d: %v", path, pkt.Index, err)
		}
		v, ok := msg.AsVoice()
		if !ok {
			continue
		}
		k := key{msg.Kind, msg.SenderID, v.StreamID}
		if seen[k] == nil {
			seen[k] = &transmission{
				kind: msg.Kind, sender: msg.SenderID,
				source: v.SourceID, destination: v.Destination, private: v.Private,
			}
			order = append(order, k)
		}
		seen[k].frames++
	}
	out := make([]transmission, 0, len(order))
	for _, k := range order {
		out = append(out, *seen[k])
	}
	return out
}

// TestAPrivateCallIsVoiceWithARadioIDWhereTheTalkgroupGoes is what 0x81 is.
//
// **This whole message type was refused** from the day the listener was written
// until 2026-09-05, so every private call from a Motorola repeater was thrown
// away with a warning and no private call ever crossed the bridge. It hid the
// way the timeslot hid: this network's traffic is group calls on TG 2.
func TestAPrivateCallIsVoiceWithARadioIDWhereTheTalkgroupGoes(t *testing.T) {
	var private []transmission
	for _, tx := range transmissions(t, privateVoice) {
		if tx.kind == ipsc.KindVoicePrivate {
			private = append(private, tx)
		}
		if (tx.kind == ipsc.KindVoicePrivate) != tx.private {
			t.Errorf("a %#02x transmission parsed with private=%v", byte(tx.kind), tx.private)
		}
	}
	if len(private) != 2 {
		t.Fatalf("the capture yields %d private transmissions, want 2", len(private))
	}

	// **The two are mirror images, which is the whole experiment.** KD9EJA
	// called Mike and Mike called KD9EJA, so source and destination move in
	// opposite directions between them and neither field can be confused for
	// the other, for a fixed value, or for the envelope's sender ID.
	a, b := private[0], private[1]
	if a.source != kd9hdr || a.destination != k9mls {
		t.Errorf("first private call is %d to %d, want %d to %d",
			a.source, a.destination, kd9hdr, k9mls)
	}
	if b.source != k9mls || b.destination != kd9hdr {
		t.Errorf("second private call is %d to %d, want %d to %d",
			b.source, b.destination, k9mls, kd9hdr)
	}
	if a.sender == b.sender {
		t.Errorf("both private calls came from repeater %d; they were keyed through different ones", a.sender)
	}
}

// TestTheLeadingByteAndTheLinkControlAgreeOnTheCallType is the evidence that
// makes 0x81 a reading rather than a guess.
//
// Byte 38 of a header or terminator is the DMR Full Link Control opcode, and
// ETSI TS 102 361-2 gives 0x00 for Grp_V_Ch_Usr and 0x03 for UU_V_Ch_Usr. It is
// an encoding Motorola did not choose, arriving in the same frame as one it
// did, and the two agree on every frame that carries both.
func TestTheLeadingByteAndTheLinkControlAgreeOnTheCallType(t *testing.T) {
	const flcoAt = ipsc.TextBlockAt - ipsc.HeaderLen
	agreed := 0
	for _, pkt := range readCapture(t, privateVoice).UDP {
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			t.Fatalf("packet %d: %v", pkt.Index, err)
		}
		if !msg.Kind.IsVoice() || len(pkt.Payload) != ipsc.HeaderLenTotal {
			continue
		}
		marker := ipsc.FrameKindOf(msg.Body[25])
		if marker != ipsc.FrameHeader && marker != ipsc.FrameTerminator {
			continue
		}
		want := byte(0x00)
		if msg.Kind == ipsc.KindVoicePrivate {
			want = 0x03
		}
		if got := msg.Body[flcoAt]; got != want {
			t.Errorf("packet %d: kind %#02x carries FLCO %#02x, want %#02x",
				pkt.Index, byte(msg.Kind), got, want)
		}
		agreed++
	}
	if agreed != 32 {
		t.Errorf("checked %d header and terminator frames, want the 32 in this capture", agreed)
	}
}

// TestAPrivateCallIsReadByEveryVoiceReader is the failure this shape invites.
//
// Four readers gate on the kind — the header, the payload, the colour code and
// the slot bit — and one of them left comparing against KindVoice alone would
// refuse half the traffic while the rest of the path worked. That is precisely
// how a whole timeslot of audio went missing for a fortnight.
func TestAPrivateCallIsReadByEveryVoiceReader(t *testing.T) {
	var payloads, slots, colours int
	for _, pkt := range readCapture(t, privateVoice).UDP {
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			t.Fatalf("packet %d: %v", pkt.Index, err)
		}
		if msg.Kind != ipsc.KindVoicePrivate {
			continue
		}
		if _, ok := msg.AsVoice(); !ok {
			t.Fatalf("packet %d: a private voice frame has no readable header", pkt.Index)
		}
		if _, ok := msg.SlotBit(); !ok {
			t.Errorf("packet %d: a private voice frame carries no slot bit", pkt.Index)
		} else {
			slots++
		}
		if _, ok := msg.ColourCode(); ok {
			colours++
		}
		if _, _, _, ok := msg.Payload(); ok {
			payloads++
		}
	}
	// 236 private frames, of which 8 are the headers and terminators of two
	// transmissions and the other 228 carry vocoder audio.
	if slots != 236 {
		t.Errorf("%d private frames carry a slot bit, want 236", slots)
	}
	if payloads != 228 {
		t.Errorf("%d private frames yield a vocoder payload, want 228", payloads)
	}
	if colours == 0 {
		t.Error("no private frame yielded a colour code")
	}
}
