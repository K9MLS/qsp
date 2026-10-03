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
// Every octet has to match, the packet CRC included: [dmrfec.JoinPacket]
// computes it rather than carrying the captured one across.
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
			payload, pad, _, err := dmrfec.SplitPacket(userData, total)
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

			joined, err := dmrfec.JoinPacket(rebuilt, len(blocks), dmrfec.Rate34DataBytes)
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

// TestThePacketCRCHoldsForEveryCapturedTransmission is the evidence for
// [dmrfec.PacketCRC], kept where the captures can be read.
//
// Six group messages in Rate 1/2 found it; ten private messages in Rate 3/4,
// in three other captures, were never part of that search. Each row names how
// many transmissions it must find, so a reader that quietly finds none cannot
// pass.
//
// To see it fail, break PacketCRC in any of the three ways
// TestThePacketCRC in internal/dmrfec lists: every row fails at once.
func TestThePacketCRCHoldsForEveryCapturedTransmission(t *testing.T) {
	tests := []struct {
		path string
		want int
	}{
		{"../../testdata/ipsc/ipsc-text.pcap", 3},
		{captureIn, 6},
		{captureOut, 1}, // assembled by serial; see relayedMessage
		{captureGroup, 6},
	}
	for _, tc := range tests {
		t.Run(tc.path[len("../../testdata/ipsc/"):], func(t *testing.T) {
			streams := [][]byte{}
			if tc.path == captureGroup {
				for _, tr := range readGroupTransmissions(t, tc.path) {
					ud, err := dmrfec.Rate12UserData(tr.blocks)
					if err != nil {
						t.Fatalf("%s: %v", tr.sid, err)
					}
					streams = append(streams, ud)
				}
			} else if tc.path == captureOut {
				blocks, _ := relayedMessage(t)
				ud, err := dmrfec.Rate34UserData(blocks, dmrfec.Rate34ControlLast)
				if err != nil {
					t.Fatalf("relayed message: %v", err)
				}
				streams = append(streams, ud)
			} else {
				for _, tr := range readTransmissions(t, tc.path) {
					blocks, _ := tr.ordered()
					ud, err := dmrfec.Rate34UserData(blocks, dmrfec.Rate34ControlLast)
					if err != nil {
						t.Fatalf("%s: %v", tr.label, err)
					}
					streams = append(streams, ud)
				}
			}
			if len(streams) != tc.want {
				t.Fatalf("found %d transmissions, want %d", len(streams), tc.want)
			}
			for i, ud := range streams {
				if err := dmrfec.VerifyPacket(ud); err != nil {
					t.Errorf("transmission %d: %v", i, err)
				}
			}
		})
	}
}

// TestThePacketCRCRefusesTheCorruptBurst puts the one block in the outbound
// capture that fails its CRC-9 back where it came from, the last block of
// the relayed message, and requires the packet CRC to refuse it too. QSP
// relayed that copy under ADR-0047; a receiver checking the packet CRC would
// have discarded it, and a later copy of the same block would have completed
// the message.
func TestThePacketCRCRefusesTheCorruptBurst(t *testing.T) {
	blocks, bad := relayedMessage(t)
	if len(bad) != 1 {
		t.Fatalf("found %d corrupt bursts, want the 1 the fixture records", len(bad))
	}
	serial := bad[0].serial
	if serial != len(blocks)-1 {
		t.Fatalf("the corrupt burst carries serial %d, and the message ends at %d", serial, len(blocks)-1)
	}

	good, err := dmrfec.Rate34UserData(blocks, dmrfec.Rate34ControlLast)
	if err != nil {
		t.Fatalf("reassembling: %v", err)
	}
	if err := dmrfec.VerifyPacket(good); err != nil {
		t.Fatalf("the message with a verified last block: %v", err)
	}
	corrupt := bytes.Clone(good)
	copy(corrupt[serial*dmrfec.Rate34DataBytes:], bad[0].block[:dmrfec.Rate34DataBytes])
	if bytes.Equal(corrupt, good) {
		t.Fatal("the corrupt burst carries the same sixteen octets as the good one")
	}
	if err := dmrfec.VerifyPacket(corrupt); err == nil {
		t.Error("the packet CRC accepted the block its own CRC-9 refused")
	}
}

// An IP total length shorter than the header it sits in is refused, not
// sliced with. Nothing calls Parse on bytes from the air today; this keeps it
// safe for when something does.
//
// To see it fail: remove the `total < ihl` check from Parse.
func TestParseRefusesATotalLengthShorterThanTheHeader(t *testing.T) {
	d := make([]byte, 34)
	d[0] = 0x45
	d[3] = 5
	d[9] = 17
	copy(d[12:16], []byte{12, 0, 0, 1})
	copy(d[16:20], []byte{12, 0, 0, 2})
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(d[i])<<8 | uint32(d[i+1])
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	d[10], d[11] = byte(^sum>>8), byte(^sum)
	if _, err := tms.Parse(d); err == nil {
		t.Fatal("a datagram whose total length is 5 was parsed")
	}
}
