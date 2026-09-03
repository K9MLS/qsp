package ipscbridge_test

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// masterVoice is the first capture this project holds of a master sending voice
// rather than a repeater sending to one. Every other IPSC fixture is the peer's
// half of the conversation.
const masterVoice = "../../testdata/ipsc/ipsc-master-voice.pcap"

// masterFrames returns the voice datagrams the master sent, in order.
func masterFrames(tb testing.TB) [][]byte {
	tb.Helper()
	raw, err := os.ReadFile(masterVoice)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	if len(raw) < 24 || binary.LittleEndian.Uint32(raw[20:24]) != 1 {
		tb.Fatal("this fixture should be an Ethernet capture")
	}
	var out [][]byte
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < 34 || binary.BigEndian.Uint16(rec[12:14]) != 0x0800 {
			continue
		}
		ip := rec[14:]
		if ip[9] != 17 || ip[12] != 192 || ip[15] != 233 {
			continue // only what the master sent
		}
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 9 || udp[8] != byte(ipsc.KindVoice) {
			continue
		}
		out = append(out, udp[8:])
	}
	if len(out) < 200 {
		tb.Fatalf("only %d master voice frames read; the fixture reader is broken", len(out))
	}
	return out
}

// TestAMasterSendsTheShapeQSPBuilds is the assertion ADR-0041 could not make.
//
// The transmit path was built from peer captures alone, on the assumption that
// a repeater accepts what a repeater sends. This checks the stronger and more
// useful claim: that a *master* sends it too.
func TestAMasterSendsTheShapeQSPBuilds(t *testing.T) {
	frames := masterFrames(t)

	shapes := map[[2]int]int{}
	var cycle []byte
	for _, f := range frames {
		class := 0
		if f[30] == 0x8a {
			class = int(f[32])
			if f[31] != byte(len(f)-32) {
				t.Fatalf("byte 31 is %#02x on a %d-byte voice frame; want len-32", f[31], len(f))
			}
		}
		shapes[[2]int{int(f[30]), class}] = len(f)
		switch {
		case f[30] == 0x01:
			cycle = append(cycle, 'H')
		case f[30] == 0x02:
			cycle = append(cycle, 'T')
		case class == int(ipsc.PayloadSync):
			cycle = append(cycle, 'A')
		case class == int(ipsc.PayloadFragmentWithLC):
			cycle = append(cycle, 'L')
		default:
			cycle = append(cycle, 'f')
		}
	}

	for k, want := range map[[2]int]int{
		{0x01, 0}:                               ipsc.HeaderLenTotal,
		{0x02, 0}:                               ipsc.HeaderLenTotal,
		{0x8a, int(ipsc.PayloadSync)}:           52,
		{0x8a, int(ipsc.PayloadFragment)}:       57,
		{0x8a, int(ipsc.PayloadFragmentWithLC)}: 66,
	} {
		got, ok := shapes[k]
		if !ok {
			t.Errorf("the master never sent marker %#02x class %#02x", k[0], k[1])
			continue
		}
		if got != want {
			t.Errorf("master marker %#02x class %#02x is %d bytes, QSP builds %d", k[0], k[1], got, want)
		}
	}

	// Three headers, superframes, one terminator — the structure the encoder
	// produces, asserted against the thing it imitates.
	s := string(cycle)
	for len(s) > 0 {
		switch {
		case len(s) >= 3 && s[:3] == "HHH":
			s = s[3:]
		case len(s) >= 6 && s[:6] == "AfffLf":
			s = s[6:]
		case s[0] == 'T':
			s = s[1:]
		default:
			t.Fatalf("the master's frame order broke at %q", s[:min(len(s), 12)])
		}
	}
}

// TestQSPsHeaderIsAMastersHeader is the byte-for-byte differential.
//
// **Only bytes 52 and 53 differ**, and those are the two ADR-0042 recorded as
// underivable and wrote as zero. Everything else — the constants, the whole
// twelve-octet Link Control block including a Reed-Solomon parity that appears
// in no earlier capture, the Slot Type, the timeslot in byte 31 — QSP produces
// from the call's own facts and produces the same as the radio.
func TestQSPsHeaderIsAMastersHeader(t *testing.T) {
	// The first header of the first transmission in the fixture: source
	// 0x2fcdee to talkgroup 2, colour code 4, timeslot 2.
	const (
		source      = 0x2fcdee
		destination = 2
		colourCode  = 4
		masterID    = 31329
	)

	var captured []byte
	for _, f := range masterFrames(t) {
		if f[30] == 0x01 {
			captured = f
			break
		}
	}
	if captured == nil {
		t.Fatal("the fixture holds no voice header")
	}

	e := ipscbridge.NewEncoder(masterID, ipscbridge.Config{
		ColourCode:         colourCode,
		SlotBitIsTimeslot2: true,
	})
	var burst [33]byte
	for i := range burst {
		burst[i] = byte(i * 7)
	}
	msgs := e.Encode(hbp.Data{
		SourceID: source, TargetID: destination, Timeslot: hbp.Timeslot2,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0x1212, Payload: burst,
	})
	if len(msgs) == 0 {
		t.Fatal("the first frame of a transmission produced nothing")
	}
	got := msgs[0].Marshal()

	if len(got) != len(captured) {
		t.Fatalf("QSP's header is %d bytes, the master's is %d", len(got), len(captured))
	}
	for i := 30; i < 52; i++ {
		if got[i] != captured[i] {
			t.Errorf("byte %d: QSP %#02x, the master %#02x\n QSP    % x\n master % x",
				i, got[i], captured[i], got[30:54], captured[30:54])
		}
	}
	// Bytes 52 and 53 are deliberately not compared. They are not derivable
	// from any capture held, they differ between two sessions of the same
	// repeater on the same colour code, and a repeater accepted QSP's zeros on
	// air. Asserting the master's values here would be asserting a measurement
	// taken by somebody else's radio.
	if got[52] != 0 || got[53] != 0 {
		t.Errorf("QSP writes %02x %02x in the unresolved tail, want zero", got[52], got[53])
	}
}

// TestQSPOpensATransmissionWithThreeHeaders anchors the count to the radio
// rather than to a number somebody chose.
//
// TestATransmissionOpensWithThreeHeaders already requires three, and catches a
// change to two. What it cannot catch is three being wrong: it asserts against
// a constant written when the only evidence was peer captures. This reads the
// count out of the master fixture and requires QSP to match whatever the radio
// actually does, so if a different master opens with a different number the
// test says so instead of quietly agreeing with itself.
func TestQSPOpensATransmissionWithThreeHeaders(t *testing.T) {
	// What the master itself does, read from the fixture rather than assumed.
	headers, seen := 0, false
	for _, f := range masterFrames(t) {
		if f[30] == 0x01 && !seen {
			headers++
			continue
		}
		if headers > 0 {
			seen = true
			break
		}
	}
	if headers != 3 {
		t.Fatalf("the master opens with %d headers; this test assumed 3", headers)
	}

	e := ipscbridge.NewEncoder(31329, ipscbridge.Config{ColourCode: 4, SlotBitIsTimeslot2: true})
	var burst [33]byte
	for i := range burst {
		burst[i] = byte(i * 7)
	}
	msgs := e.Encode(hbp.Data{
		SourceID: 0x2fcdee, TargetID: 2, Timeslot: hbp.Timeslot2,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0x1212, Payload: burst,
	})

	got := 0
	for _, m := range msgs {
		if len(m.Body) > 25 && m.Body[25] == ipsc.FrameHeader {
			got++
		}
	}
	if got != headers {
		t.Errorf("QSP opens a transmission with %d headers, the master sends %d", got, headers)
	}
}
