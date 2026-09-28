// Package tms reads and writes the datagram a Motorola text message is.
//
// A DMR text message's payload is an IPv4 UDP datagram on port 4007 carrying
// Motorola's Text Messaging Service. `internal/protocol/ipsc` and
// `internal/ipscbridge` both say, deliberately, that QSP does not decode it:
// ADR-0037 settled that a bridge carries a payload and rebuilds the wrapper,
// and reassembling TMS would have been inventing a requirement.
//
// Somebody has now asked for it. A talkgroup that answers a question has to
// read the question, and a weather alert has to be composed from nothing. So
// this package exists, and the boundary that keeps ADR-0037 intact is that
// **it is only ever called for a message addressed to QSP's own service
// address.** Everything else still crosses the bridge without being read. See
// [ADR-0067](../../docs/adr/ADR-0067-qsp-originates-a-text-message.md) and
// [ADR-0068](../../docs/adr/ADR-0068-qsp-answers-a-text-and-weather-is-the-first-answer.md).
//
// # Measured, not read
//
// Everything below comes from the three complete transmissions in
// testdata/ipsc/ipsc-text-rate34.pcap and
// testdata/ipsc/ipsc-text-rate34-out.pcap. The first of them decodes to
// "I can't talk right now...", which is the sort of thing that tells you the
// offsets are right.
//
//	45 00 00 58 b4 71 00 00 40 11 ba 29   IPv4: 88 octets, TTL 64, UDP
//	0c 2f cd ee                           source      12 + radio 3132910
//	0c 30 25 ad                           destination 12 + radio 3155373
//	0f a7 0f a7 00 44 1a d6               UDP 4007 → 4007, 68 octets
//	00 3a e0 00 84 04                     TMS header, six octets
//	0d 00 0a 00 49 00 ...                 CRLF then UTF-16LE
//
// The addresses are Motorola's radio-IP scheme: 0x0c and then the radio's
// 24-bit identifier, which `internal/protocol/ipsc/text.go` already records.
// Both checksums are ordinary — the IPv4 header checksum and the UDP checksum
// over the usual pseudo-header both reproduce exactly — so they are computed
// here rather than carried.
//
// The TMS header's first two octets are the length of everything after them,
// which held for payloads of 68, 26 and 36 octets. Octets 2, 3 and 5 read
// `e0`, `00` and `04` in all three. **Octet 4 varied — 0x84, 0x85, 0x95 — and
// what it means is not known**; it looks like a message reference, it is
// carried on the way in and set by the caller on the way out, and nothing here
// depends on its value.
//
// The body is UTF-16 little-endian and begins with a carriage return and a
// line feed, in all three.
package tms

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

const (
	// Port is the UDP port both ends of a text message use.
	Port = 4007
	// radioIPPrefix is the first octet of Motorola's radio-IP addresses.
	radioIPPrefix = 0x0c

	ipHeaderBytes  = 20
	udpHeaderBytes = 8
	// tmsHeaderBytes is the header between the UDP payload and the text.
	tmsHeaderBytes = 6
	// defaultTTL is what the captures carry.
	defaultTTL = 64
	// protocolUDP is IANA 17.
	protocolUDP = 17

	// bodyPrefix precedes the text in every captured message.
	bodyPrefix = "\r\n"
)

// tmsConstants are octets 2, 3 and 5 of the TMS header, which did not vary.
var tmsConstants = [3]byte{0xe0, 0x00, 0x04}

// Message is a text message and the two identities it travels between.
type Message struct {
	// From and To are 24-bit radio identifiers. **To is a radio ID even for
	// a group message**: the call type lives in the IP Site Connect
	// envelope, not in here, so whether To names a talkgroup is the
	// envelope's business and this package does not guess.
	From, To uint32
	// IPID is the IPv4 identification field. It is arbitrary on the wire and
	// is carried so that a captured message rebuilds byte for byte; a
	// message QSP originates may set it to anything.
	IPID uint16
	// Reference is octet 4 of the TMS header, whose meaning is unverified.
	Reference uint8
	// Text is the message, with the leading CRLF removed.
	Text string
}

// Parse reads a text message out of the IPv4 datagram that carries it.
func Parse(datagram []byte) (Message, error) {
	if len(datagram) < ipHeaderBytes+udpHeaderBytes+tmsHeaderBytes {
		return Message{}, fmt.Errorf("tms: %d octets is too short to be a text message", len(datagram))
	}
	if v := datagram[0] >> 4; v != 4 {
		return Message{}, fmt.Errorf("tms: IP version %d, want 4", v)
	}
	ihl := int(datagram[0]&0x0f) * 4
	if ihl != ipHeaderBytes {
		return Message{}, fmt.Errorf("tms: a %d-octet IP header has options, which no captured message has", ihl)
	}
	total := int(binary.BigEndian.Uint16(datagram[2:4]))
	if total > len(datagram) {
		return Message{}, fmt.Errorf("tms: IP total length %d exceeds the %d octets in hand", total, len(datagram))
	}
	if p := datagram[9]; p != protocolUDP {
		return Message{}, fmt.Errorf("tms: IP protocol %d, want %d", p, protocolUDP)
	}
	if got, want := binary.BigEndian.Uint16(datagram[10:12]), ipChecksum(datagram[:ihl], 10); got != want {
		return Message{}, fmt.Errorf("tms: IP header checksum is %#04x, computed %#04x", got, want)
	}

	src, err := radioOf(datagram[12:16])
	if err != nil {
		return Message{}, fmt.Errorf("tms: source: %w", err)
	}
	dst, err := radioOf(datagram[16:20])
	if err != nil {
		return Message{}, fmt.Errorf("tms: destination: %w", err)
	}

	udp := datagram[ihl:total]
	if len(udp) < udpHeaderBytes {
		return Message{}, fmt.Errorf("tms: %d octets of UDP, want at least %d", len(udp), udpHeaderBytes)
	}
	if sp, dp := binary.BigEndian.Uint16(udp[0:2]), binary.BigEndian.Uint16(udp[2:4]); sp != Port || dp != Port {
		return Message{}, fmt.Errorf("tms: UDP %d → %d, want %d → %d", sp, dp, Port, Port)
	}
	ulen := int(binary.BigEndian.Uint16(udp[4:6]))
	if ulen != len(udp) {
		return Message{}, fmt.Errorf("tms: UDP length %d against %d octets", ulen, len(udp))
	}
	if got, want := binary.BigEndian.Uint16(udp[6:8]), udpChecksum(datagram[12:20], udp); got != want {
		return Message{}, fmt.Errorf("tms: UDP checksum is %#04x, computed %#04x", got, want)
	}

	payload := udp[udpHeaderBytes:]
	if len(payload) < tmsHeaderBytes {
		return Message{}, fmt.Errorf("tms: %d octets of payload, want at least %d", len(payload), tmsHeaderBytes)
	}
	if stated, want := int(binary.BigEndian.Uint16(payload[0:2])), len(payload)-2; stated != want {
		return Message{}, fmt.Errorf("tms: header states %d octets follow, and %d do", stated, want)
	}
	if payload[2] != tmsConstants[0] || payload[3] != tmsConstants[1] || payload[5] != tmsConstants[2] {
		return Message{}, fmt.Errorf("tms: header reads %02x %02x .. %02x where every captured one reads %02x %02x .. %02x",
			payload[2], payload[3], payload[5], tmsConstants[0], tmsConstants[1], tmsConstants[2])
	}

	body := payload[tmsHeaderBytes:]
	if len(body)%2 != 0 {
		return Message{}, fmt.Errorf("tms: %d octets of body is not whole UTF-16 code units", len(body))
	}
	units := make([]uint16, len(body)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(body[i*2:])
	}
	text := string(utf16.Decode(units))
	if !strings.HasPrefix(text, bodyPrefix) {
		return Message{}, fmt.Errorf("tms: body does not begin with the CRLF every captured message has")
	}

	return Message{
		From:      src,
		To:        dst,
		IPID:      binary.BigEndian.Uint16(datagram[4:6]),
		Reference: payload[4],
		Text:      strings.TrimPrefix(text, bodyPrefix),
	}, nil
}

// Build composes the IPv4 datagram that carries a text message.
//
// The result is the packet alone. Turning it into blocks is
// [dmrfec.Rate34Blocks]'s job, and putting those on the air needs the packet
// CRC that [dmrfec.PacketCRC] cannot yet compute.
func Build(m Message) ([]byte, error) {
	if m.From > 0xffffff || m.To > 0xffffff {
		return nil, fmt.Errorf("tms: %d and %d are not both 24-bit radio identifiers", m.From, m.To)
	}
	units := utf16.Encode([]rune(bodyPrefix + m.Text))
	body := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(body[i*2:], u)
	}

	payload := make([]byte, tmsHeaderBytes, tmsHeaderBytes+len(body))
	binary.BigEndian.PutUint16(payload[0:2], uint16(tmsHeaderBytes-2+len(body)))
	payload[2], payload[3] = tmsConstants[0], tmsConstants[1]
	payload[4] = m.Reference
	payload[5] = tmsConstants[2]
	payload = append(payload, body...)

	total := ipHeaderBytes + udpHeaderBytes + len(payload)
	if total > 0xffff {
		return nil, fmt.Errorf("tms: a %d-octet datagram does not fit an IPv4 total length", total)
	}

	out := make([]byte, ipHeaderBytes+udpHeaderBytes+len(payload))
	out[0] = 0x45
	binary.BigEndian.PutUint16(out[2:4], uint16(total))
	binary.BigEndian.PutUint16(out[4:6], m.IPID)
	out[8] = defaultTTL
	out[9] = protocolUDP
	putRadioIP(out[12:16], m.From)
	putRadioIP(out[16:20], m.To)
	binary.BigEndian.PutUint16(out[10:12], ipChecksum(out[:ipHeaderBytes], 10))

	udp := out[ipHeaderBytes:]
	binary.BigEndian.PutUint16(udp[0:2], Port)
	binary.BigEndian.PutUint16(udp[2:4], Port)
	binary.BigEndian.PutUint16(udp[4:6], uint16(udpHeaderBytes+len(payload)))
	copy(udp[udpHeaderBytes:], payload)
	binary.BigEndian.PutUint16(udp[6:8], udpChecksum(out[12:20], udp))

	return out, nil
}

// radioOf reads a radio identifier out of a Motorola radio-IP address.
func radioOf(addr []byte) (uint32, error) {
	if addr[0] != radioIPPrefix {
		return 0, fmt.Errorf("address %d.%d.%d.%d does not begin %d, Motorola's radio-IP prefix",
			addr[0], addr[1], addr[2], addr[3], radioIPPrefix)
	}
	return uint32(addr[1])<<16 | uint32(addr[2])<<8 | uint32(addr[3]), nil
}

func putRadioIP(dst []byte, radio uint32) {
	dst[0] = radioIPPrefix
	dst[1], dst[2], dst[3] = byte(radio>>16), byte(radio>>8), byte(radio)
}

// ipChecksum is the ones-complement sum an IPv4 header carries, computed with
// the two octets at skip treated as zero.
func ipChecksum(header []byte, skip int) uint16 {
	var sum uint32
	for i := 0; i+1 < len(header); i += 2 {
		if i == skip {
			continue
		}
		sum += uint32(binary.BigEndian.Uint16(header[i:]))
	}
	return fold(sum)
}

// udpChecksum is the same sum over the pseudo-header and the datagram, with
// the checksum field itself treated as zero.
func udpChecksum(addrs []byte, udp []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(addrs); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(addrs[i:]))
	}
	sum += protocolUDP
	sum += uint32(len(udp))
	for i := 0; i+1 < len(udp); i += 2 {
		if i == 6 {
			continue
		}
		sum += uint32(binary.BigEndian.Uint16(udp[i:]))
	}
	if len(udp)%2 != 0 {
		sum += uint32(udp[len(udp)-1]) << 8
	}
	return fold(sum)
}

func fold(sum uint32) uint16 {
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}
