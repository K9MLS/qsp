package tms_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/tms"
)

const (
	captureIn  = "../../testdata/ipsc/ipsc-text-rate34.pcap"
	captureOut = "../../testdata/ipsc/ipsc-text-rate34-out.pcap"
)

// TestEveryCapturedTextRoundTripsToTheSameOctets is the whole point of
// ADR-0067 phase 1.
//
// For each real transmission: reassemble the blocks, split off the pad and the
// packet CRC, parse the datagram, build a datagram from what was parsed, and
// compare. Then cut the rebuilt datagram back into blocks and compare those,
// control pair and CRC-9 included.
//
// **The packet CRC is carried, not computed**, because nothing solves it —
// see [dmrfec.ErrPacketCRCUnverified]. Everything else has to match to the
// octet, and if this test ever passes while that CRC is invented it is
// asserting the wrong thing.
//
// To see it fail: change `bodyPrefix` in tms.go, or flip a constant in
// `tmsConstants`, or swap `binary.LittleEndian` for big in either direction.
// Each breaks a different one of the assertions below.
func TestEveryCapturedTextRoundTripsToTheSameOctets(t *testing.T) {
	asserted := 0
	for _, path := range []string{captureIn, captureOut} {
		for _, tr := range readTransmissions(t, path) {
			asserted++
			blocks, ok := tr.ordered()
			if !ok {
				t.Fatalf("%s/%s: incomplete transmission reached the test", path, tr.label)
			}

			userData, err := dmrfec.Rate34UserData(blocks, dmrfec.Rate34ControlLast)
			if err != nil {
				t.Fatalf("%s/%s: reassembling: %v", path, tr.label, err)
			}
			total := int(binary.BigEndian.Uint16(userData[2:4]))
			payload, pad, packetCRC, err := dmrfec.SplitPacket(userData, total)
			if err != nil {
				t.Fatalf("%s/%s: splitting: %v", path, tr.label, err)
			}
			for i, o := range pad {
				if o != 0 {
					t.Errorf("%s/%s: pad octet %d is %#02x, and every captured pad octet is zero",
						path, tr.label, i, o)
				}
			}

			msg, err := tms.Parse(payload)
			if err != nil {
				t.Fatalf("%s/%s: parsing: %v", path, tr.label, err)
			}
			if msg.Text == "" {
				t.Errorf("%s/%s: parsed an empty message out of %d octets", path, tr.label, len(payload))
			}

			rebuilt, err := tms.Build(msg)
			if err != nil {
				t.Fatalf("%s/%s: building: %v", path, tr.label, err)
			}
			if !bytes.Equal(rebuilt, payload) {
				t.Errorf("%s/%s: rebuilt datagram differs\n have %x\n want %x", path, tr.label, rebuilt, payload)
				continue
			}

			joined, err := dmrfec.JoinPacket(rebuilt, len(blocks), dmrfec.Rate34DataBytes, packetCRC)
			if err != nil {
				t.Fatalf("%s/%s: joining: %v", path, tr.label, err)
			}
			if !bytes.Equal(joined, userData) {
				t.Errorf("%s/%s: rejoined user data differs\n have %x\n want %x", path, tr.label, joined, userData)
				continue
			}

			again, err := dmrfec.Rate34Blocks(joined, dmrfec.Rate34ControlLast)
			if err != nil {
				t.Fatalf("%s/%s: segmenting: %v", path, tr.label, err)
			}
			if len(again) != len(blocks) {
				t.Fatalf("%s/%s: rebuilt %d blocks, captured %d", path, tr.label, len(again), len(blocks))
			}
			for i := range blocks {
				if !bytes.Equal(again[i], blocks[i]) {
					t.Errorf("%s/%s: block %d differs\n have %x\n want %x",
						path, tr.label, i, again[i], blocks[i])
				}
			}
		}
	}
	// A round-trip test that round-trips nothing passes, which is the shape
	// of green test §8s warns about. The inbound capture holds six complete
	// transmissions of two distinct messages; anything less than that means
	// the reader stopped finding them.
	if asserted < 6 {
		t.Fatalf("only %d transmissions were asserted; the reader is finding fewer than the captures hold", asserted)
	}
}

// TestTheOutboundCaptureHoldsACorruptBurst records why the CRC-9 filter in
// the reader is not optional.
//
// The outbound fixture's only multi-block confirmed message has four blocks
// per its data header and one of them fails its own CRC-9. Because a block's
// serial number lives under that same CRC, the bad burst reads as some other
// serial and replaces a good block — and the transmission then reassembles
// into a datagram that parses, checksums and all, to the text "Hkgmgd1mg".
//
// That nearly became a fixture. It looked like a short message and it is
// noise, and the thing that caught it was checking the CRC rather than
// trusting the reassembly. Two mistakes cancelling out, exactly as §8s
// describes.
func TestTheOutboundCaptureHoldsACorruptBurst(t *testing.T) {
	if dropped := droppedBursts(t, captureOut); dropped == 0 {
		t.Error("no burst in the outbound capture fails its CRC-9; if the fixture was replaced, re-check what this test is for")
	}
	if dropped := droppedBursts(t, captureIn); dropped != 0 {
		t.Errorf("%d bursts in the inbound capture fail their CRC-9, and it was clean when this was written", dropped)
	}
	// And the consequence: no complete confirmed multi-block message survives
	// in the outbound capture at all.
	if got := len(readTransmissions(t, captureOut)); got != 0 {
		t.Errorf("the outbound capture now yields %d complete transmissions; it yielded none when this was written", got)
	}
}

// TestTheCapturedMessageSaysWhatItSays checks the decode against a sentence a
// human typed, because offsets that are subtly wrong still produce text.
//
// "I can't talk right now..." is one of Motorola's canned quick messages, and
// it is 25 characters, which is what makes it useful here: the six blocks
// carry 96 octets, the datagram is 88, the header is 6 and 25 characters of
// UTF-16 plus a CRLF is 54. Those numbers only add up one way.
func TestTheCapturedMessageSaysWhatItSays(t *testing.T) {
	want := map[string]bool{
		"I can't talk right now...": false,
		"Give":                      false,
	}
	for _, tr := range readTransmissions(t, captureIn) {
		blocks, _ := tr.ordered()
		userData, err := dmrfec.Rate34UserData(blocks, dmrfec.Rate34ControlLast)
		if err != nil {
			t.Fatalf("%s: %v", tr.label, err)
		}
		payload, _, _, err := dmrfec.SplitPacket(userData, int(binary.BigEndian.Uint16(userData[2:4])))
		if err != nil {
			t.Fatalf("%s: %v", tr.label, err)
		}
		msg, err := tms.Parse(payload)
		if err != nil {
			t.Fatalf("%s: %v", tr.label, err)
		}
		if _, ok := want[msg.Text]; ok {
			want[msg.Text] = true
		}
		if msg.From != 3132910 {
			t.Errorf("%s: source %d, want 3132910", tr.label, msg.From)
		}
	}
	for text, seen := range want {
		if !seen {
			t.Errorf("no captured transmission decoded to %q", text)
		}
	}
}

// TestABuiltMessageSurvivesItsOwnParser covers text the captures do not have:
// empty, long, non-ASCII and the punctuation a radio keypad produces.
func TestABuiltMessageSurvivesItsOwnParser(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"single character", "A"},
		{"canned message", "I can't talk right now..."},
		{"punctuation", "WX: 91F, SE 9mph, Mostly Clear"},
		{"non-ascii", "Grüße — naïve café"},
		{"outside the basic plane", "ok \U0001F4E1"},
		{"long", strings.Repeat("the quick brown fox ", 12)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tms.Message{From: 3132910, To: 3155373, IPID: 0xb471, Reference: 0x84, Text: tc.text}
			datagram, err := tms.Build(in)
			if err != nil {
				t.Fatalf("building: %v", err)
			}
			out, err := tms.Parse(datagram)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			if out != in {
				t.Errorf("round trip changed the message\n have %+v\n want %+v", out, in)
			}
		})
	}
}

// TestAnEmptyTextIsStillAMessage records a decision rather than an assumption:
// a zero-length text builds, because the CRLF body makes it well formed, and
// the dispatcher rather than this package decides whether it is worth sending.
func TestAnEmptyTextIsStillAMessage(t *testing.T) {
	datagram, err := tms.Build(tms.Message{From: 1, To: 2, Text: ""})
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	msg, err := tms.Parse(datagram)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if msg.Text != "" {
		t.Errorf("text is %q, want empty", msg.Text)
	}
}

// TestAMangledDatagramIsRefused is the deliberate-breakage half: every
// corruption below must be caught, because a parser on a service address is
// reachable by anybody with a radio and a mistake there is not a crash, it is
// a wrong answer sent back on the air.
func TestAMangledDatagramIsRefused(t *testing.T) {
	good, err := tms.Build(tms.Message{From: 3132910, To: 3155373, IPID: 1, Reference: 0x84, Text: "hello"})
	if err != nil {
		t.Fatalf("building: %v", err)
	}

	for _, tc := range []struct {
		name    string
		corrupt func(d []byte)
		why     string
	}{
		{"ip version", func(d []byte) { d[0] = 0x65 }, "a version other than 4"},
		{"ip options", func(d []byte) { d[0] = 0x46 }, "a header with options"},
		{"protocol", func(d []byte) { d[9] = 6 }, "TCP rather than UDP"},
		{"ip checksum", func(d []byte) { d[10] ^= 0xff }, "a broken header checksum"},
		{"radio ip prefix", func(d []byte) { d[12] = 10 }, "an address that is not radio-IP"},
		{"udp port", func(d []byte) { d[20] = 0 }, "a port other than 4007"},
		{"udp checksum", func(d []byte) { d[26] ^= 0xff }, "a broken UDP checksum"},
		{"tms length", func(d []byte) { d[28] ^= 0xff }, "a stated length that disagrees"},
		{"tms constant", func(d []byte) { d[30] = 0x00 }, "a header constant no capture has"},
		{"body prefix", func(d []byte) { d[34] = 0x41 }, "a body without its CRLF"},
		{"truncated", func(d []byte) {}, "a datagram cut short"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := append([]byte(nil), good...)
			tc.corrupt(d)
			if tc.name == "truncated" {
				d = d[:10]
			}
			// The checksums cover most of the header, so a corruption that is
			// meant to trip a later check will usually trip the checksum
			// first. That is fine: what matters is that nothing gets through.
			if _, err := tms.Parse(d); err == nil {
				t.Errorf("parsed %s without complaint", tc.why)
			}
		})
	}
}

// TestThePacketCRCIsHonestlyUnsolved keeps the gap from being quietly closed.
//
// If somebody implements the packet CRC, this test fails, and the right
// response is to delete it and to write the one that checks the real value
// against the three captured transmissions.
func TestThePacketCRCIsHonestlyUnsolved(t *testing.T) {
	if _, err := dmrfec.PacketCRC([]byte{1, 2, 3}); err == nil {
		t.Fatal("PacketCRC returned a value; if it is solved, replace this test with one that checks it against the captures")
	}
}
