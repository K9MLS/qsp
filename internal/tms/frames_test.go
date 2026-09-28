package tms_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/tms"
)

// captureHotspotGroup is the operator's group text "K9MLS", to talkgroup 2 on
// timeslot 2, as the hotspot at 192.168.1.155 forwarded it.
const captureHotspotGroup = "../../testdata/hbp/hbp-text-preambles.pcap"

// hotspotFrames returns the DMRD frames the hotspot sent, in order.
func hotspotFrames(tb testing.TB, path string) []hbp.Data {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("reading %s: %v", path, err)
	}
	var out []hbp.Data
	for off := 24; off+16 <= len(raw); {
		caplen := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+caplen > len(raw) {
			break
		}
		packet := raw[off : off+caplen]
		off += caplen
		if len(packet) < sll2HeaderBytes+28 {
			continue
		}
		ip := packet[sll2HeaderBytes:]
		if ip[9] != 17 || binary.BigEndian.Uint32(ip[12:16]) != 0xc0a8019b {
			continue
		}
		msg, err := hbp.Parse(ip[int(ip[0]&0x0f)*4+8:])
		if err != nil {
			continue
		}
		if d, ok := msg.(hbp.Data); ok {
			out = append(out, d)
		}
	}
	return out
}

// counter hands out stream IDs 1, 2, 3 …, so the structure of a composed
// transmission can be compared without its values meaning anything.
func counter() func() hbp.StreamID {
	var n hbp.StreamID
	return func() hbp.StreamID { n++; return n }
}

// k9mls is the captured message, with the two fields the capture chose
// arbitrarily — the IP identification and the TMS reference — read from it.
var k9mls = tms.Message{From: 3132910, To: 2, Group: true, IPID: 0x115f, Reference: 0x95, Text: "K9MLS"}

// TestAComposedTextIsWhatTheHotspotSent is ADR-0067 phase 2's differential:
// before anything reaches a radio, the frames QSP composes must be the frames
// a hotspot sends for the same message.
//
// Every burst is compared whole — thirty-three octets of coded DMR, slot type
// and colour code included — and so is every header field but two. The
// stream IDs are compared by shape (a fresh one per preamble, one shared by
// the message) because their values are arbitrary. The trailing octets are
// not compared: `00 2f` is MMDVMHost reporting its receiver, which a master
// has nothing to say in.
//
// To see it fail:
//   - set tms.Preambles to 15, and the count and every preamble's
//     bursts-to-come change;
//   - drop the `+ 1` for the header from BlocksToFollow in Frames;
//   - send the content blocks as dmrfec.DataTypeDataHeader;
//   - give each content block its own stream;
//   - restart the sequence at zero for the message's stream.
func TestAComposedTextIsWhatTheHotspotSent(t *testing.T) {
	want := hotspotFrames(t, captureHotspotGroup)
	if len(want) != 22 {
		t.Fatalf("read %d frames from the hotspot, want 22", len(want))
	}
	// The colour code a hotspot writes is its own. Read it from the capture
	// rather than restating it, so this test says where eleven came from.
	colourCode, _, ok := dmrfec.SlotTypeOf(want[0].Payload[:])
	if !ok || colourCode != 11 {
		t.Fatalf("the capture's colour code reads %d, want 11", colourCode)
	}

	got, err := tms.Frames(k9mls, hbp.Timeslot2, colourCode, counter())
	if err != nil {
		t.Fatalf("composing: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("composed %d frames, the hotspot sent %d", len(got), len(want))
	}

	for i := range want {
		g, w := got[i], want[i]
		if g.Payload != w.Payload {
			t.Errorf("frame %d burst differs\n have %x\n want %x", i, g.Payload, w.Payload)
		}
		if g.Sequence != w.Sequence || g.SourceID != w.SourceID || g.TargetID != w.TargetID ||
			g.Timeslot != w.Timeslot || g.CallType != w.CallType ||
			g.FrameType != w.FrameType || g.DataType != w.DataType {
			t.Errorf("frame %d header differs\n have seq %d %d→%d %v %v %v %#x\n want seq %d %d→%d %v %v %v %#x", i,
				g.Sequence, g.SourceID, g.TargetID, g.Timeslot, g.CallType, g.FrameType, g.DataType,
				w.Sequence, w.SourceID, w.TargetID, w.Timeslot, w.CallType, w.FrameType, w.DataType)
		}
		if g.RepeaterID != 0 {
			t.Errorf("frame %d names repeater %d; the sender stamps the peer", i, g.RepeaterID)
		}
	}

	// Stream shape: the same on both sides.
	shape := func(frames []hbp.Data) string {
		seen := map[hbp.StreamID]byte{}
		var b strings.Builder
		for _, f := range frames {
			if _, ok := seen[f.StreamID]; !ok {
				seen[f.StreamID] = byte('a' + len(seen))
			}
			b.WriteByte(seen[f.StreamID])
		}
		return b.String()
	}
	if g, w := shape(got), shape(want); g != w {
		t.Errorf("stream shape %s, the hotspot's is %s", g, w)
	}
}

// TestAComposedTextIsRefusedWhereItCannotBeRight covers every refusal Frames
// makes, and one it must not make.
func TestAComposedTextIsRefusedWhereItCannotBeRight(t *testing.T) {
	private := k9mls
	private.Group = false
	long := k9mls
	long.Text = strings.Repeat("x", 80)
	fits := k9mls
	fits.Text = strings.Repeat("x", 60)

	tests := []struct {
		name   string
		m      tms.Message
		slot   hbp.Timeslot
		cc     uint8
		is     error
		wantOK bool
	}{
		{"a group text", k9mls, hbp.Timeslot2, 11, nil, true},
		{"sixty characters, which fit fifteen blocks", fits, hbp.Timeslot1, 1, nil, true},
		{"a private text", private, hbp.Timeslot2, 11, tms.ErrPrivateNotYet, false},
		{"eighty characters", long, hbp.Timeslot2, 11, tms.ErrTooLong, false},
		{"colour code sixteen", k9mls, hbp.Timeslot2, 16, nil, false},
		{"no timeslot", k9mls, 0, 11, nil, false},
		{"a 25-bit sender", tms.Message{From: 1 << 24, To: 2, Group: true, Text: "x"}, hbp.Timeslot2, 11, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frames, err := tms.Frames(tc.m, tc.slot, tc.cc, counter())
			if tc.wantOK {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if n := len(frames); n < tms.Preambles+2 {
					t.Fatalf("composed only %d frames", n)
				}
				return
			}
			if err == nil {
				t.Fatal("composed without complaint")
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("error %v, want %v", err, tc.is)
			}
		})
	}
}

// TestEveryComposedStreamIsFresh: two transmissions must never share a
// stream ID, or a hotspot joins them into one.
func TestEveryComposedStreamIsFresh(t *testing.T) {
	next := counter()
	a, err := tms.Frames(k9mls, hbp.Timeslot2, 11, next)
	if err != nil {
		t.Fatalf("%v", err)
	}
	b, err := tms.Frames(k9mls, hbp.Timeslot2, 11, next)
	if err != nil {
		t.Fatalf("%v", err)
	}
	seen := map[hbp.StreamID]bool{}
	for _, f := range a {
		seen[f.StreamID] = true
	}
	for _, f := range b {
		if seen[f.StreamID] {
			t.Fatalf("stream %d appears in both transmissions", f.StreamID)
		}
	}
	if !bytes.Equal(a[0].Payload[:], b[0].Payload[:]) {
		t.Error("the same message composed twice gave different first preambles")
	}
}
