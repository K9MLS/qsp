package p25link_test

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/p25"
)

// Frames for the tests come from the real captures rather than from bytes
// typed here. A listener tested against invented frames is a listener tested
// against my understanding of P25 instead of against P25.

const captureFile = "../../testdata/p25/p25-talkgroups.pcap"

// capturedFrames returns the P25 network frames on the gateway's inbound path.
func capturedFrames(tb testing.TB) [][]byte {
	tb.Helper()

	raw, err := os.ReadFile(captureFile)
	if err != nil {
		tb.Fatalf("cannot read %s: %v", captureFile, err)
	}
	if len(raw) < 24 || binary.LittleEndian.Uint32(raw[0:4]) != 0xa1b2c3d4 {
		tb.Fatalf("%s is not a little-endian pcap", captureFile)
	}
	if link := binary.LittleEndian.Uint32(raw[20:24]); link != 276 {
		tb.Fatalf("%s: link type %d, want 276 (LINUX_SLL2)", captureFile, link)
	}

	var out [][]byte
	off := 24
	for off+16 <= len(raw) {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if incl < 0 || off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl

		const sll2 = 20
		if len(rec) < sll2+28 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[sll2:]
		ihl := int(ip[0]&0x0F) * 4
		if ihl < 20 || len(ip) < ihl+8 || ip[9] != 17 {
			continue
		}
		udp := ip[ihl:]
		src := binary.BigEndian.Uint16(udp[0:2])
		dst := binary.BigEndian.Uint16(udp[2:4])
		length := int(binary.BigEndian.Uint16(udp[4:6]))
		if src != 32010 || dst != 42020 || length <= 8 || length > len(udp) {
			continue
		}
		payload := append([]byte(nil), udp[8:length]...)
		if len(payload) > 0 {
			out = append(out, payload)
		}
	}
	if len(out) == 0 {
		tb.Fatalf("%s held no inbound frames", captureFile)
	}
	return out
}

// voiceFrame returns one real voice frame.
func voiceFrame(tb testing.TB) []byte {
	tb.Helper()

	for _, f := range capturedFrames(tb) {
		parsed, err := p25.Parse(f)
		if err == nil && parsed.Voice() {
			return f
		}
	}
	tb.Fatal("no voice frame in the capture")
	return nil
}

// linkControlFrames returns one frame of each kind that carries a link control
// field, so a test can drive both the talkgroup and the source in one go.
func linkControlFrames(tb testing.TB) [][]byte {
	tb.Helper()

	var talkgroup, source []byte
	for _, f := range capturedFrames(tb) {
		parsed, err := p25.Parse(f)
		if err != nil {
			continue
		}
		if _, _, err := parsed.Talkgroup(); err == nil && talkgroup == nil {
			talkgroup = f
		}
		if _, err := parsed.SourceID(); err == nil && source == nil {
			source = f
		}
		if talkgroup != nil && source != nil {
			break
		}
	}
	if talkgroup == nil || source == nil {
		tb.Fatal("the capture holds no frames carrying a talkgroup and a source")
	}
	return [][]byte{talkgroup, source}
}
