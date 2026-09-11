package p25

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

// A small pcap reader, for the reason the hbp and ipsc packages have one: it
// handles exactly one link type and one endianness, and a fixture that does not
// match should fail loudly rather than be quietly accepted.

type capturedPacket struct {
	SrcPort uint16
	DstPort uint16
	Micros  uint64
	Payload []byte
}

func readCapture(tb testing.TB, path string) []capturedPacket {
	tb.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("cannot read fixture %s: %v", path, err)
	}
	if len(raw) < 24 {
		tb.Fatalf("fixture %s is too small to contain a pcap header", path)
	}
	if magic := binary.LittleEndian.Uint32(raw[0:4]); magic != 0xa1b2c3d4 {
		tb.Fatalf("fixture %s: unexpected pcap magic %#08x", path, magic)
	}
	if link := binary.LittleEndian.Uint32(raw[20:24]); link != 276 {
		tb.Fatalf("fixture %s: link type %d, expected 276 (LINUX_SLL2)", path, link)
	}

	var out []capturedPacket
	off := 24
	for off+16 <= len(raw) {
		sec := uint64(binary.LittleEndian.Uint32(raw[off : off+4]))
		usec := uint64(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if incl < 0 || off+incl > len(raw) {
			tb.Fatalf("fixture %s: truncated record at offset %d", path, off)
		}
		rec := raw[off : off+incl]
		off += incl

		p, ok := decodeSLL2UDP(rec)
		if !ok {
			continue
		}
		p.Micros = sec*1_000_000 + usec
		out = append(out, p)
	}
	if len(out) == 0 {
		tb.Fatalf("fixture %s contained no UDP packets", path)
	}
	return out
}

func decodeSLL2UDP(rec []byte) (capturedPacket, bool) {
	const sll2Header = 20
	if len(rec) < sll2Header+20 {
		return capturedPacket{}, false
	}
	if binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
		return capturedPacket{}, false
	}
	ip := rec[sll2Header:]
	ihl := int(ip[0]&0x0F) * 4
	if ihl < 20 || len(ip) < ihl+8 || ip[9] != 17 {
		return capturedPacket{}, false
	}
	udp := ip[ihl:]
	length := int(binary.BigEndian.Uint16(udp[4:6]))
	if length < 8 || length > len(udp) {
		return capturedPacket{}, false
	}
	return capturedPacket{
		SrcPort: binary.BigEndian.Uint16(udp[0:2]),
		DstPort: binary.BigEndian.Uint16(udp[2:4]),
		Payload: udp[8:length],
	}, true
}

// gatewayFrames returns the P25 network frames flowing from the reflector side
// towards MMDVMHost, which is the direction QSP would receive.
func gatewayFrames(tb testing.TB) []capturedPacket {
	tb.Helper()

	var out []capturedPacket
	for _, p := range readCapture(tb, "../../../testdata/p25/p25-voice.pcap") {
		if p.SrcPort == 32010 && p.DstPort == 42020 && len(p.Payload) > 0 {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		tb.Fatal("the fixture holds no frames on the gateway's inbound path")
	}
	return out
}

func describe(p capturedPacket) string {
	return fmt.Sprintf("0x%02x/%d", p.Payload[0], len(p.Payload))
}

// transmissions groups the talkgroup capture's inbound frames into calls,
// splitting on a gap of more than a second.
//
// A P25 transmission ends in a terminator, but grouping by silence rather than
// by that frame means a capture missing one still yields sensible calls — and
// the assertion that there are fourteen would otherwise be an assertion about
// terminators instead of about transmissions.
func transmissions(tb testing.TB) [][]capturedPacket {
	tb.Helper()

	var inbound []capturedPacket
	for _, p := range readCapture(tb, "../../../testdata/p25/p25-talkgroups.pcap") {
		if p.SrcPort == 32010 && p.DstPort == 42020 && len(p.Payload) > 0 {
			inbound = append(inbound, p)
		}
	}
	if len(inbound) == 0 {
		tb.Fatal("the talkgroup fixture holds no inbound frames")
	}

	const gap = 1_000_000 // one second, in microseconds
	var out [][]capturedPacket
	current := []capturedPacket{inbound[0]}
	for i := 1; i < len(inbound); i++ {
		if inbound[i].Micros-inbound[i-1].Micros > gap {
			out = append(out, current)
			current = nil
		}
		current = append(current, inbound[i])
	}
	return append(out, current)
}
