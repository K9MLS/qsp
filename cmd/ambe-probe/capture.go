package main

import (
	"encoding/binary"
	"fmt"
	"os"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// Real audio, from a capture rather than from a signal generator.
//
// # Why a synthetic signal could not finish the job
//
// A steady sine proved the round trip and then proved the wrong path: tone
// detection is on at reset, so the encoder described the tone instead of
// coding it. Turning tone detection off gave the voice path — frames changing
// every 20 ms, no tone flag — and an output at about a third of the input
// amplitude, which is what a speech model does with something that is not
// speech. **Neither run says anything about how the chip handles a voice**,
// and no synthetic signal can, because the question is about a model fitted
// to human speech.
//
// testdata/ipsc/ipsc-master-voice.pcap is a real Motorola master sending real
// audio off the operator's own XPR8300. Its voice frames carry the 49-bit
// vocoder parameters this chip codes at rate index 33 — which Table 115 says
// is the rate interoperable with DMR. So the decisive test needs no new
// capture and no new hardware: take the frames, hand them to the dongle, and
// listen to what comes back.
//
// That is also exactly the direction Zello needs. DMR frames in, PCM out.

// pcapPacket is one captured datagram's payload.
type pcapPacket []byte

// readPcapUDP reads a pcap file and returns the UDP payload of every packet.
//
// **Deliberately small and deliberately strict.** It handles the two link
// types the project's own captures use and refuses anything else rather than
// guessing at an offset — an offset guessed wrong produces payloads that parse
// as something, which is worse than refusing. The capture files carry an md5
// in their companion documents, so a file that reads oddly is a file to check
// rather than a format to accommodate.
func readPcapUDP(path string) ([]pcapPacket, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 24 {
		return nil, fmt.Errorf("%s is %d bytes, too short for a pcap header", path, len(raw))
	}

	var order binary.ByteOrder = binary.LittleEndian
	magic := binary.LittleEndian.Uint32(raw[:4])
	nanos := false
	switch magic {
	case 0xa1b2c3d4:
	case 0xa1b23c4d:
		nanos = true
	case 0xd4c3b2a1:
		order = binary.BigEndian
	case 0x4d3cb2a1:
		order, nanos = binary.BigEndian, true
	default:
		return nil, fmt.Errorf("%s is not a pcap file (magic %#08x)", path, magic)
	}
	_ = nanos // timestamps are not used; frame order is what matters here

	// Link type decides where the IP header starts. 1 is Ethernet, 113 is
	// Linux cooked v1, 276 is Linux cooked v2 — the three that `tcpdump -i
	// any` and `-i eth0` produce on the machines this project captures on.
	var linkHeader int
	switch lt := order.Uint32(raw[20:24]); lt {
	case 1:
		linkHeader = 14
	case 113:
		linkHeader = 16
	case 276:
		linkHeader = 20
	default:
		return nil, fmt.Errorf("%s has link type %d, which this tool does not read", path, lt)
	}

	var out []pcapPacket
	for off := 24; off+16 <= len(raw); {
		caplen := int(order.Uint32(raw[off+8 : off+12]))
		off += 16
		if caplen < 0 || off+caplen > len(raw) {
			break
		}
		if payload, ok := udpPayload(raw[off:off+caplen], linkHeader); ok {
			out = append(out, payload)
		}
		off += caplen
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no UDP datagrams this tool could read", path)
	}
	return out, nil
}

// udpPayload finds the UDP payload inside one captured frame.
func udpPayload(frame []byte, linkHeader int) (pcapPacket, bool) {
	if len(frame) < linkHeader+20 {
		return nil, false
	}
	ip := frame[linkHeader:]
	switch ip[0] >> 4 {
	case 4:
		ihl := int(ip[0]&0x0F) * 4
		if ihl < 20 || len(ip) < ihl+8 || ip[9] != 17 {
			return nil, false
		}
		return ip[ihl+8:], true
	case 6:
		if len(ip) < 48 || ip[6] != 17 {
			return nil, false
		}
		return ip[48:], true
	}
	return nil, false
}

// vocoderFramesFromCapture pulls 72-bit vocoder frames out of an IPSC capture,
// in the order they were transmitted.
//
// Each voice frame carries a 19-byte core holding three 49-bit parameter
// frames, which `internal/dmrfec` established from a silence transmission and
// confirmed against Homebrew captures from a different radio on a different
// protocol. The FEC is then added back with `dmrfec.Encode`, because the chip
// wants 72 bits at rate index 33 — 49 of speech and 23 of forward error
// correction.
//
// **Whether the chip expects those 23 bits in ETSI's arrangement is the open
// question**, which is why form selects between two candidates and the dongle
// decides. Rate 33 exists for DMR interoperability, so the on-air form is the
// strong candidate; a chip that refuses it will say so through DATA_INVALID,
// and a chip that accepts the wrong one will produce noise rather than a
// voice. Either is an answer.
func vocoderFramesFromCapture(path, form string) ([][]byte, error) {
	packets, err := readPcapUDP(path)
	if err != nil {
		return nil, err
	}

	var out [][]byte
	voice := 0
	for _, p := range packets {
		msg, err := ipsc.Parse(p)
		if err != nil || !msg.Kind.IsVoice() {
			continue
		}
		_, core, _, ok := msg.Payload()
		if !ok || len(core) != dmrfec.IPSCCoreBytes {
			continue
		}
		params, ok := dmrfec.UnpackIPSCCore(core)
		if !ok {
			continue
		}
		voice++
		for _, p := range params {
			switch form {
			case "params":
				// The 49 parameter bits, then 23 zeros. If the chip computes
				// its own error correction over what it is given, this is
				// what it wants; if it expects correction bits, this is 23
				// bits of nothing and it will notice.
				bits := make([]byte, 72)
				for i := 0; i < dmrfec.ParameterBits; i++ {
					bits[i] = byte(p >> uint(dmrfec.ParameterBits-1-i) & 1)
				}
				out = append(out, packFrameBits(bits))
			default:
				out = append(out, packFrameBits(dmrfec.Encode(p)))
			}
		}
	}
	if voice == 0 {
		return nil, fmt.Errorf("%s holds no IPSC voice frames", path)
	}
	return out, nil
}

// packFrameBits turns 72 one-bit-per-byte values into nine bytes.
func packFrameBits(bits []byte) []byte {
	out := make([]byte, 9)
	for i := 0; i < 72 && i < len(bits); i++ {
		if bits[i]&1 == 1 {
			out[i/8] |= 1 << uint(7-i%8)
		}
	}
	return out
}

// writeWAV writes 8 kHz 16-bit mono samples as a RIFF file.
//
// **A WAV because the operator has ears and this project does not have a
// metric for voice quality.** Every other question about this chip has been
// settled by a differential or a byte count; "does it sound like a person" is
// settled by listening, and the only honest way to present it is a file that
// plays.
func writeWAV(path string, samples []int16) error {
	const (
		sampleRate = 8000
		channels   = 1
		bits       = 16
	)
	data := len(samples) * 2
	buf := make([]byte, 0, 44+data)

	buf = append(buf, "RIFF"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(36+data))
	buf = append(buf, "WAVEfmt "...)
	buf = binary.LittleEndian.AppendUint32(buf, 16)
	buf = binary.LittleEndian.AppendUint16(buf, 1) // PCM
	buf = binary.LittleEndian.AppendUint16(buf, channels)
	buf = binary.LittleEndian.AppendUint32(buf, sampleRate)
	buf = binary.LittleEndian.AppendUint32(buf, sampleRate*channels*bits/8)
	buf = binary.LittleEndian.AppendUint16(buf, channels*bits/8)
	buf = binary.LittleEndian.AppendUint16(buf, bits)
	buf = append(buf, "data"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(data))
	for _, s := range samples {
		buf = binary.LittleEndian.AppendUint16(buf, uint16(s))
	}

	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
