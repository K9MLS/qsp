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
	SrcIP   string
	DstIP   string
	SrcPort uint16
	DstPort uint16
	Payload []byte
	Micros  uint64
}

// captureSummary is a whole fixture: its UDP payloads, and how many ICMP
// packets accompanied them.
//
// ICMP is counted because it is evidence. The phase 1 captures record a kernel
// refusing a port that nothing had bound, and the repeater ignoring the refusal
// is the reason a QSP master will not be able to turn a peer away by staying
// silent.
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
	// **Two link types, because captures arrive two ways.** A capture taken on
	// one interface is Ethernet; one taken with `tcpdump -i any`, which is what
	// a server with several interfaces needs, is Linux cooked v2. Refusing the
	// second would mean refusing every capture taken on the production VM.
	link := binary.LittleEndian.Uint32(raw[20:24])
	switch link {
	case linkEthernet, linkCookedV2:
	default:
		return sum, fmt.Errorf("fixture %s: link type %d, expected %d (EN10MB) or %d (LINUX_SLL2)",
			path, link, linkEthernet, linkCookedV2)
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

		p, proto, ok := decodeIP(rec, link)
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

// Link types, as recorded in a pcap file header.
const (
	linkEthernet = 1
	linkCookedV2 = 276
)

// decodeIP strips whichever link header the capture carries and hands the rest
// to the IPv4 decoder.
//
// Linux cooked v2 puts the protocol in its first two bytes and is twenty bytes
// long; Ethernet puts it at byte twelve and is fourteen. Both then hold an
// ordinary IPv4 packet.
func decodeIP(rec []byte, link uint32) (capturedPacket, byte, bool) {
	if link == linkCookedV2 {
		if len(rec) < 20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			return capturedPacket{}, 0, false
		}
		return decodeIPv4(rec[20:])
	}
	return decodeEthernetIP(rec)
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
	return decodeIPv4(rec[ethHeader:])
}

// decodeIPv4 is the part common to both link types.
func decodeIPv4(ip []byte) (capturedPacket, byte, bool) {
	var p capturedPacket
	if len(ip) < 20 {
		return p, 0, false
	}
	ihl := int(ip[0]&0x0f) * 4
	if ihl < 20 || len(ip) < ihl {
		return p, 0, false
	}
	p.SrcIP = dotted(ip[12:16])
	p.DstIP = dotted(ip[16:20])
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

func dotted(b []byte) string {
	return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
}
