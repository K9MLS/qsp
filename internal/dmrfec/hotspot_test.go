package dmrfec_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
	"unicode/utf16"

	"github.com/k9mls/qsp/internal/dmrfec"
)

// hotspotFixture is the capture that ends the argument.
//
// **It is the only capture anywhere of a Rate 3/4 burst as a radio produces
// it.** Every other Rate 3/4 fixture here holds blocks that a Motorola
// repeater had already decoded, which exercises the block layout and says
// nothing at all about the trellis. This one holds 54 coded bursts from
// MMDVMHost on a Pi-Star, sent while the operator typed a two-letter message
// into a handheld.
const hotspotFixture = "../../testdata/hbp/hbp-text-rate34.pcap"

// hotspotRate34Bursts reads the coded bursts out of the Homebrew frames.
//
// A DMRD frame is a four-byte magic, a sequence number, source, destination
// and repeater IDs, one byte of flags and then the 33-byte burst. Bit 4 to 5
// of the flags is the frame type and the low nibble is the data type.
func hotspotRate34Bursts(tb testing.TB) [][]byte {
	tb.Helper()
	raw, err := os.ReadFile(hotspotFixture)
	if err != nil {
		tb.Fatalf("%v", err)
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
		if len(rec) < 20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[20:]
		if len(ip) < 20 || ip[9] != 17 {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 8 {
			continue
		}
		d := udp[8:]
		if len(d) != 55 || string(d[:4]) != "DMRD" {
			continue
		}
		if d[15]&0x0f != dmrfec.DataTypeRate34 {
			continue
		}
		out = append(out, bytes.Clone(d[20:20+dmrfec.BurstBytes]))
	}
	// A reader that quietly returned nothing would make every assertion below
	// pass by having nothing to assert about.
	if len(out) != 54 {
		tb.Fatalf("read %d Rate 3/4 bursts from the hotspot capture, want 54", len(out))
	}
	return out
}

// TestEveryHotspotBurstDecodes is the assertion the trellis tables were
// missing for their whole existence.
//
// **Fifty-four bursts a radio actually transmitted, decoded by these tables.**
// Until this capture the tables were checked only by encode and decode
// agreeing with each other, which sixteen wrong constellation entries
// satisfied perfectly — see ADR-0047. A wrong table fails here on the first
// burst, because the decoder walks a path through the trellis and a point that
// is not in the current state's row has nowhere to go.
func TestEveryHotspotBurstDecodes(t *testing.T) {
	for i, burst := range hotspotRate34Bursts(t) {
		if _, _, ok := dmrfec.DecodeRate34Burst(burst); !ok {
			t.Fatalf("burst %d did not decode: %x", i, burst)
		}
	}
}

// TestTheHotspotPutsTheControlPairFirst settles Rate34AirOrder by measurement.
//
// ETSI figure 8.8 draws the block serial number and CRC at the front and IP
// Site Connect delivers them at the back. Which one goes on air decided
// whether anything QSP transmits could be read, and nothing had measured it.
//
// **Forty-nine of these 54 bursts verify their CRC-9 control-first, and none
// verify control-last.** The five that verify neither are one block arriving
// three ways, differing by single bits: errors the hotspot passed through,
// which is exactly why a block that fails its CRC is carried rather than
// dropped.
func TestTheHotspotPutsTheControlPairFirst(t *testing.T) {
	counts := map[dmrfec.Rate34Order]int{}
	for _, burst := range hotspotRate34Bursts(t) {
		_, order, ok := dmrfec.DecodeRate34Burst(burst)
		if !ok {
			t.Fatal("a burst did not decode")
		}
		counts[order]++
	}
	if counts[dmrfec.Rate34ControlLast] != 0 {
		t.Errorf("%d bursts verified control-last; none should",
			counts[dmrfec.Rate34ControlLast])
	}
	if counts[dmrfec.Rate34ControlFirst] != 49 {
		t.Errorf("%d bursts verified control-first, want 49",
			counts[dmrfec.Rate34ControlFirst])
	}
	if dmrfec.Rate34AirOrder != dmrfec.Rate34ControlFirst {
		t.Errorf("Rate34AirOrder is %s and the capture says control-first",
			dmrfec.Rate34AirOrder)
	}
}

// TestTheHotspotsMessageDecodes carries the proof past this repository.
//
// The blocks reassemble into the datagram the handheld built, and its payload
// is the word that was typed. Arithmetic that produces a well-formed IPv4
// header, addresses derived from the two radio IDs, a UDP length field that
// agrees with itself and a legible word is not arithmetic that got lucky.
func TestTheHotspotsMessageDecodes(t *testing.T) {
	// Blocks in serial order, first arrival of each, ignoring the corrupted
	// repeats the CRC rejects.
	blocks := map[uint8][]byte{}
	for _, burst := range hotspotRate34Bursts(t) {
		block, order, ok := dmrfec.DecodeRate34Burst(burst)
		if !ok || order == dmrfec.Rate34OrderUnknown {
			continue
		}
		serial, verified := dmrfec.Rate34Serial(block)
		if !verified {
			t.Fatalf("a block reported an order but failed its CRC: %x", block)
		}
		if _, seen := blocks[serial]; !seen {
			blocks[serial] = block
		}
	}

	var payload []byte
	for serial := uint8(0); serial < 3; serial++ {
		block, ok := blocks[serial]
		if !ok {
			t.Fatalf("no block with serial %d", serial)
		}
		payload = append(payload, block[:dmrfec.Rate34DataBytes]...)
	}

	total := int(binary.BigEndian.Uint16(payload[2:4]))
	if payload[0]>>4 != 4 || total != 42 || payload[9] != 17 {
		t.Fatalf("reassembled payload is not the expected IPv4 datagram: %x", payload[:20])
	}
	if src := binary.BigEndian.Uint32(payload[12:16]); src != 0x0c2fcdee {
		t.Errorf("source address is %#08x", src)
	}
	if dst := binary.BigEndian.Uint32(payload[16:20]); dst != 0x0c3025ad {
		t.Errorf("destination address is %#08x", dst)
	}

	udp := payload[20:total]
	if l := int(binary.BigEndian.Uint16(udp[4:6])); l != len(udp) {
		t.Fatalf("UDP length field is %d and the datagram is %d", l, len(udp))
	}

	const tmsHeaderLen = 10
	body := udp[8+tmsHeaderLen:]
	units := make([]uint16, 0, len(body)/2)
	for i := 0; i+1 < len(body); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(body[i:i+2]))
	}
	if got := string(utf16.Decode(units)); got != "Hi" {
		t.Errorf("the message reads %q, want %q", got, "Hi")
	}
}

// TestTheStreamIDsGroupExactlyTwoWays answers a question the journal raised.
//
// Relaying a text produced a `relaying transmission` line every 111 ms, each
// with a different stream ID, which looks exactly like each burst being
// treated as its own transmission — a defect that would explain how text
// behaves in Last-heard and the call records.
//
// **It is not one.** The 296 Homebrew frames in this capture carry 242 stream
// IDs, and they decompose without remainder into two shapes: 224 streams of a
// single preamble CSBK, and 18 streams of a data header followed by its three
// Rate 3/4 blocks. The message is grouped correctly and always was. A preamble
// CSBK is a standalone control block rather than part of the data
// transmission, so a fresh stream ID for each is what a hotspot is supposed to
// send, and 224 of them is what makes the journal look alarming.
//
// The arithmetic is the whole argument: 224 + 18 = 242, and 224 + 18×4 = 296.
// Any other grouping leaves a remainder.
func TestTheStreamIDsGroupExactlyTwoWays(t *testing.T) {
	raw, err := os.ReadFile(hotspotFixture)
	if err != nil {
		t.Fatalf("%v", err)
	}
	type stream struct {
		id     uint32
		frames []uint8
	}
	var order []uint32
	streams := map[uint32]*stream{}
	var total int
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < 20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[20:]
		if len(ip) < 20 || ip[9] != 17 {
			continue
		}
		// Only the hotspot's own frames: 192.168.1.155.
		if binary.BigEndian.Uint32(ip[12:16]) != 0xc0a8019b {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 8 {
			continue
		}
		d := udp[8:]
		if len(d) != 55 || string(d[:4]) != "DMRD" {
			continue
		}
		total++
		id := binary.BigEndian.Uint32(d[16:20])
		if _, ok := streams[id]; !ok {
			streams[id] = &stream{id: id}
			order = append(order, id)
		}
		streams[id].frames = append(streams[id].frames, d[15]&0x0f)
	}

	const (
		csbk       = 0x3
		dataHeader = 0x6
	)
	var preambles, transmissions int
	for _, id := range order {
		switch got := streams[id].frames; {
		case len(got) == 1 && got[0] == csbk:
			preambles++
		case len(got) == 4 && got[0] == dataHeader &&
			got[1] == dmrfec.DataTypeRate34 &&
			got[2] == dmrfec.DataTypeRate34 &&
			got[3] == dmrfec.DataTypeRate34:
			transmissions++
		default:
			t.Errorf("stream %#08x has an unexpected shape: %v", id, got)
		}
	}

	if total != 296 || len(order) != 242 {
		t.Fatalf("%d frames in %d streams, want 296 in 242", total, len(order))
	}
	if preambles != 224 {
		t.Errorf("%d single-CSBK streams, want 224", preambles)
	}
	if transmissions != 18 {
		t.Errorf("%d header-and-blocks streams, want 18", transmissions)
	}
}

// TestABurstQSPBuildsMatchesTheShapeAHotspotSends compares the two ends.
//
// Re-coding a block a hotspot sent must reproduce that hotspot's burst
// exactly. **This is the one test here that a wrong table cannot pass**: the
// bytes on the right-hand side were produced by somebody else's encoder.
func TestABurstQSPBuildsMatchesTheShapeAHotspotSends(t *testing.T) {
	var checked int
	for i, burst := range hotspotRate34Bursts(t) {
		block, order, ok := dmrfec.DecodeRate34Burst(burst)
		if !ok || order != dmrfec.Rate34ControlFirst {
			continue
		}
		cc, _, ok := dmrfec.SlotTypeOf(burst)
		if !ok {
			t.Fatalf("burst %d has an unreadable Slot Type", i)
		}
		rebuilt, err := dmrfec.BuildRate34Burst(cc, block)
		if err != nil {
			t.Fatalf("burst %d: %v", i, err)
		}
		if !bytes.Equal(rebuilt, burst) {
			t.Fatalf("burst %d rebuilt differently:\n got %x\nwant %x", i, rebuilt, burst)
		}
		checked++
	}
	if checked != 49 {
		t.Fatalf("rebuilt %d bursts, want 49", checked)
	}
}
