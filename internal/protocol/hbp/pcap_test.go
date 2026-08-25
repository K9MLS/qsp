package hbp_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

// capturedPacket is one UDP payload recovered from a fixture.
type capturedPacket struct {
	Index   int
	SrcPort uint16
	DstPort uint16
	Payload []byte
	Micros  uint64
}

// Flow returns a stable identifier for the conversation a packet belongs to.
//
// Direction matters: the same logical stream traverses two links, so anything
// counting per-stream must key on flow as well.
func (p capturedPacket) Flow() string { return fmt.Sprintf("%d->%d", p.SrcPort, p.DstPort) }

// readCapture extracts UDP payloads from a classic pcap file with LINUX_SLL2
// framing, which is what `tcpdump -i any` produces on Linux.
//
// This is a deliberately small reader rather than a dependency: it needs to
// handle exactly one link type and one endianness, and a fixture that does not
// match should fail loudly rather than be silently accepted.
func readCapture(tb testing.TB, path string) []capturedPacket {
	tb.Helper()
	out, err := readCaptureRaw(path)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	return out
}

// readCaptureRaw is readCapture without a testing dependency, so that both
// tests and the fuzz seed corpus use one implementation.
func readCaptureRaw(path string) ([]capturedPacket, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read fixture %s: %w", path, err)
	}
	if len(raw) < 24 {
		return nil, fmt.Errorf("fixture %s is too small to contain a pcap header", path)
	}
	if magic := binary.LittleEndian.Uint32(raw[0:4]); magic != 0xa1b2c3d4 {
		return nil, fmt.Errorf("fixture %s: unexpected pcap magic %#08x; expected a little-endian microsecond capture", path, magic)
	}
	if link := binary.LittleEndian.Uint32(raw[20:24]); link != 276 {
		return nil, fmt.Errorf("fixture %s: link type %d, expected 276 (LINUX_SLL2)", path, link)
	}

	var out []capturedPacket
	off := 24
	for off+16 <= len(raw) {
		sec := uint64(binary.LittleEndian.Uint32(raw[off : off+4]))
		usec := uint64(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if incl < 0 || off+incl > len(raw) {
			return nil, fmt.Errorf("fixture %s: truncated record at offset %d", path, off)
		}
		rec := raw[off : off+incl]
		off += incl

		p, ok := decodeSLL2UDP(rec)
		if !ok {
			continue
		}
		p.Index = len(out)
		p.Micros = sec*1_000_000 + usec
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("fixture %s contained no UDP packets", path)
	}
	return out, nil
}

// decodeSLL2UDP unwraps SLL2, IPv4 and UDP. It returns false for anything else.
func decodeSLL2UDP(rec []byte) (capturedPacket, bool) {
	const sll2Header = 20
	if len(rec) < sll2Header+20 {
		return capturedPacket{}, false
	}
	if binary.BigEndian.Uint16(rec[0:2]) != 0x0800 { // IPv4
		return capturedPacket{}, false
	}
	ip := rec[sll2Header:]
	ihl := int(ip[0]&0x0F) * 4
	if ihl < 20 || len(ip) < ihl+8 {
		return capturedPacket{}, false
	}
	if ip[9] != 17 { // UDP
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
