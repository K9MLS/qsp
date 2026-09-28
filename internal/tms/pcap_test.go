package tms_test

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
)

// Reading the text captures.
//
// The fixtures were taken with `tcpdump -i any`, so they are LINUX_SLL2 like
// every other capture in this project, and each packet is an IP Site Connect
// datagram inside UDP. Offsets within that datagram come from
// `internal/protocol/ipsc/text.go`, which measured them across 163 bursts.

const (
	sll2HeaderBytes = 20
	// dataTypeAt, blockAt: ipsc.TextDataTypeAt and ipsc.TextBlockAt, repeated
	// here rather than imported so this helper has no dependency on the
	// protocol package it is checking the output of.
	dataTypeAt = 30
	blockAt    = 38

	dataTypeHeader = 6
	dataTypeRate34 = 8

	headerBlockBytes = 12
	rate34BlockBytes = 18

	rate34DatagramBytes = 60
	bptcDatagramBytes   = 54
)

// transmission is one text message as it appeared on the wire.
type transmission struct {
	header  []byte           // the twelve-octet data header block
	blocks  map[uint8][]byte // Rate 3/4 blocks by serial number
	label   string
	dropped int // bursts whose CRC-9 did not verify
}

// ordered returns the blocks in serial order, or false where one is missing.
// Incomplete transmissions are real — the captures hold several — and a test
// that silently concatenated whatever arrived would be asserting about the
// wrong bytes.
func (t transmission) ordered() ([][]byte, bool) {
	serials := make([]int, 0, len(t.blocks))
	for s := range t.blocks {
		serials = append(serials, int(s))
	}
	sort.Ints(serials)
	for i, s := range serials {
		if s != i {
			return nil, false
		}
	}
	out := make([][]byte, 0, len(serials))
	for _, s := range serials {
		out = append(out, t.blocks[uint8(s)])
	}
	return out, len(out) > 0
}

// readTransmissions pulls every complete text transmission out of a capture.
func readTransmissions(tb testing.TB, path string) []transmission {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("reading %s: %v", path, err)
	}
	if len(raw) < 24 {
		tb.Fatalf("%s: %d octets is not a pcap", path, len(raw))
	}
	if magic := binary.LittleEndian.Uint32(raw[:4]); magic != 0xa1b2c3d4 {
		tb.Fatalf("%s: magic %#08x, want 0xa1b2c3d4", path, magic)
	}
	if link := binary.LittleEndian.Uint32(raw[20:24]); link != 276 {
		tb.Fatalf("%s: link type %d, want 276 (LINUX_SLL2)", path, link)
	}

	byStream := map[string]*transmission{}
	order := []string{}
	for off := 24; off+16 <= len(raw); {
		caplen := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+caplen > len(raw) {
			break
		}
		packet := raw[off : off+caplen]
		off += caplen

		if len(packet) < sll2HeaderBytes+20 {
			continue
		}
		ip := packet[sll2HeaderBytes:]
		ihl := int(ip[0]&0x0f) * 4
		if len(ip) < ihl+8 {
			continue
		}
		udp := ip[ihl:]
		ulen := int(binary.BigEndian.Uint16(udp[4:6]))
		if ulen < 8 || ulen > len(udp) {
			continue
		}
		dg := udp[8:ulen]
		if len(dg) != rate34DatagramBytes && len(dg) != bptcDatagramBytes {
			continue
		}

		key := fmt.Sprintf("%02x-%s-%s", dg[0], hex.EncodeToString(dg[6:12]), hex.EncodeToString(dg[12:16]))
		t, ok := byStream[key]
		if !ok {
			t = &transmission{blocks: map[uint8][]byte{}, label: key}
			byStream[key] = t
			order = append(order, key)
		}
		switch dg[dataTypeAt] & 0x0f {
		case dataTypeHeader:
			if len(dg) == bptcDatagramBytes {
				t.header = append([]byte(nil), dg[blockAt:blockAt+headerBlockBytes]...)
			}
		case dataTypeRate34:
			if len(dg) == rate34DatagramBytes {
				block := append([]byte(nil), dg[blockAt:blockAt+rate34BlockBytes]...)
				// **Keep only blocks whose own CRC-9 verifies.** The outbound
				// capture holds one burst that does not, and because a
				// block's serial number lives under that same CRC, a corrupt
				// block otherwise lands on some other block's serial and
				// quietly replaces it. That is how a capture with a single
				// bad burst produced a full-looking transmission that
				// reassembled to rubbish.
				pair := binary.BigEndian.Uint16(block[16:18])
				serial := uint8(pair >> 9)
				if pair&0x1ff != dmrfec.CRC9(block[:16], serial) {
					t.dropped++
					continue
				}
				t.blocks[serial] = block
			}
		}
	}

	// **The data header is the authority on how many blocks a transmission
	// has, and most streams in these captures are not whole text messages.**
	// The outbound fixture holds response packets, continuation fragments
	// whose header says one block, and headers with no blocks at all. A test
	// that took whatever arrived would assert about whichever of those it
	// happened to find.
	out := []transmission{}
	for _, key := range order {
		t := byStream[key]
		if t.header == nil {
			continue
		}
		h, err := dmrfec.ParseDataHeader(t.header)
		if err != nil {
			continue // not a confirmed data header
		}
		if h.SAP != dmrfec.SAPIPPacketData || int(h.Blocks) < 2 {
			continue // not an IP packet, or a single-block fragment
		}
		blocks, ok := t.ordered()
		if !ok || len(blocks) != int(h.Blocks) {
			continue // incomplete against what the header states
		}
		out = append(out, *t)
	}
	return out
}

// droppedBursts is how many bursts in a capture failed their own CRC-9.
func droppedBursts(tb testing.TB, path string) int {
	tb.Helper()
	n := 0
	for _, b := range rate34Bursts(tb, path) {
		if !b.ok {
			n++
		}
	}
	return n
}

// burst is one Rate 3/4 block as captured, eighteen octets with the control
// pair last, and whether its own CRC-9 verified.
type burst struct {
	block  []byte
	serial int
	ok     bool
}

// rate34Bursts returns every Rate 3/4 block in a capture, in capture order,
// without grouping them into streams.
func rate34Bursts(tb testing.TB, path string) []burst {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("reading %s: %v", path, err)
	}
	var out []burst
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
		udp := ip[int(ip[0]&0x0f)*4:]
		ulen := int(binary.BigEndian.Uint16(udp[4:6]))
		if ulen < 8 || ulen > len(udp) {
			continue
		}
		dg := udp[8:ulen]
		if len(dg) != rate34DatagramBytes || dg[dataTypeAt]&0x0f != dataTypeRate34 {
			continue
		}
		block := append([]byte(nil), dg[blockAt:blockAt+rate34BlockBytes]...)
		pair := binary.BigEndian.Uint16(block[16:18])
		serial := uint8(pair >> 9)
		out = append(out, burst{
			block:  block,
			serial: int(serial),
			ok:     pair&0x1ff == dmrfec.CRC9(block[:16], serial),
		})
	}
	return out
}

// relayedMessage assembles the one message in the outbound capture.
//
// **It cannot be read stream by stream.** The sender repeats its last block
// until it gives up, and the repeats arrive under different stream keys from
// the header, so the one copy of the last block that shares the header's
// stream is the copy whose CRC-9 fails, and [readTransmissions] rightly sees
// that stream as incomplete. The capture holds serials 0, 1 and 2 once and 3
// nine times, all one message, so this takes the first verified copy of each
// serial instead.
func relayedMessage(tb testing.TB) (blocks [][]byte, corrupt []burst) {
	tb.Helper()
	bySerial := map[int][]byte{}
	for _, b := range rate34Bursts(tb, captureOut) {
		if !b.ok {
			corrupt = append(corrupt, b)
			continue
		}
		if _, seen := bySerial[b.serial]; !seen {
			bySerial[b.serial] = b.block
		}
	}
	for i := range len(bySerial) {
		b, ok := bySerial[i]
		if !ok {
			tb.Fatalf("%s: no verified copy of serial %d", captureOut, i)
		}
		blocks = append(blocks, b)
	}
	return blocks, corrupt
}
