package ipsc_test

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

// icmpCount is how many ICMP packets a fixture contains.
//
// It matters here because the IPSC captures were taken with a host filter
// rather than a UDP one, so they record the kernel's port-unreachable replies
// as well as the repeater's requests. That the repeater ignored them is a
// finding, and a finding a fixture demonstrates should have a test.
type captureSummary struct {
	UDP  []capturedPacket
	ICMP int
}

// readCapture extracts UDP payloads and counts ICMP from a classic pcap file
// with EN10MB framing, which is what `tcpdump -i <interface>` produces.
//
// The HBP fixtures are LINUX_SLL2 because they were taken with `-i any`; these
// are Ethernet because a named interface preserves the link header. A small
// reader per link type is preferred to a dependency: it handles exactly what
// the fixtures contain, and a file that does not match fails loudly instead of
// being quietly accepted.
func readCapture(tb testing.TB, path string) captureSummary {
	tb.Helper()
	out, err := readCaptureRaw(path)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	return out
}

func readCaptureRaw(path string) (captureSummary, error) {
	var sum captureSummary
	raw, err := os.ReadFile(path)
	if err != nil {
		return sum, fmt.Errorf("cannot read fixture %s: %w", path, err)
	}
	if len(raw) < 24 {
		return sum, fmt.Errorf("fixture %s is too small to contain a pcap header", path)
	}
	if magic := binary.LittleEndian.Uint32(raw[0:4]); magic != 0xa1b2c3d4 {
		return sum, fmt.Errorf("fixture %s: unexpected pcap magic %#08x; expected a little-endian microsecond capture", path, magic)
	}
	if link := binary.LittleEndian.Uint32(raw[20:24]); link != 1 {
		return sum, fmt.Errorf("fixture %s: link type %d, expected 1 (EN10MB)", path, link)
	}

	off := 24
	for off+16 <= len(raw) {
		sec := uint64(binary.LittleEndian.Uint32(raw[off : off+4]))
		usec := uint64(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if incl < 0 || off+incl > len(raw) {
			return sum, fmt.Errorf("fixture %s: truncated record at offset %d", path, off)
		}
		rec := raw[off : off+incl]
		off += incl

		p, proto, ok := decodeEthernetIP(rec)
		if !ok {
			continue
		}
		switch proto {
		case 1:
			sum.ICMP++
		case 17:
			p.Index = len(sum.UDP)
			p.Micros = sec*1_000_000 + usec
			sum.UDP = append(sum.UDP, p)
		}
	}
	if len(sum.UDP) == 0 {
		return sum, fmt.Errorf("fixture %s contained no UDP packets", path)
	}
	return sum, nil
}

// decodeEthernetIP pulls an IPv4 packet out of an Ethernet frame, returning the
// UDP ports and payload when the protocol is UDP. ARP and IPv6 are skipped.
func decodeEthernetIP(rec []byte) (capturedPacket, byte, bool) {
	var p capturedPacket
	const ethHeader = 14
	if len(rec) < ethHeader {
		return p, 0, false
	}
	if binary.BigEndian.Uint16(rec[12:14]) != 0x0800 {
		return p, 0, false
	}
	ip := rec[ethHeader:]
	if len(ip) < 20 {
		return p, 0, false
	}
	ihl := int(ip[0]&0x0f) * 4
	if ihl < 20 || len(ip) < ihl {
		return p, 0, false
	}
	proto := ip[9]
	if proto != 17 {
		return p, proto, true
	}
	udp := ip[ihl:]
	if len(udp) < 8 {
		return p, proto, false
	}
	length := int(binary.BigEndian.Uint16(udp[4:6]))
	if length < 8 || length > len(udp) {
		return p, proto, false
	}
	p.SrcPort = binary.BigEndian.Uint16(udp[0:2])
	p.DstPort = binary.BigEndian.Uint16(udp[2:4])
	p.Payload = udp[8:length]
	return p, proto, true
}
