package dmrfec_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
)

// The three data headers measured on 2026-09-27, with the transmission each
// belongs to. Every field below is read off the wire; nothing is assumed.
var capturedHeaders = []struct {
	name       string
	block      string
	blocks     uint8
	pad        uint8
	sendSeq    uint8
	to         uint32
	from       uint32
	payload    int // the IPv4 total length of the packet these blocks carry
	blockBytes int // sixteen for Rate 3/4, twelve for Rate 1/2
	group      bool
	response   bool
}{
	{
		name: "six blocks, four pad", block: "434430 25ad2f cdee86 58fbaa",
		blocks: 6, pad: 4, sendSeq: 5, to: 3155373, from: 3132910, payload: 88,
		blockBytes: dmrfec.Rate34DataBytes, response: true,
	},
	{
		name: "four blocks, fourteen pad", block: "434e30 25ad2f cdee84 684403",
		blocks: 4, pad: 14, sendSeq: 6, to: 3155373, from: 3132910, payload: 46,
		blockBytes: dmrfec.Rate34DataBytes, response: true,
	},
	{
		name: "four blocks, four pad", block: "434430 25ad2f cdee84 38f16e",
		blocks: 4, pad: 4, sendSeq: 3, to: 3155373, from: 3132910, payload: 56,
		blockBytes: dmrfec.Rate34DataBytes, response: true,
	},
	// The four distinct group headers from the 2026-09-27 calibration
	// capture, read off the wire rather than transcribed by hand — the first
	// attempt at this table pasted one header's CRC onto another row, and the
	// test caught it. The group bit is set, no response is requested, the
	// send-sequence octet is zero, and the packet format is **unconfirmed**.
	// These are the first group text headers this project has seen, and they
	// are why the group bit's position is no longer "taken from ETSI and
	// never observed".
	{
		name: "group, five blocks, eight pad", block: "82480000022fcdee85004cd9",
		blocks: 5, pad: 8, sendSeq: 0, to: 2, from: 3132910, payload: 48,
		blockBytes: dmrfec.Rate12DataBytes, group: true,
	},
	{
		name: "group, five blocks, ten pad", block: "824a0000022fcdee85008abe",
		blocks: 5, pad: 10, sendSeq: 0, to: 2, from: 3132910, payload: 46,
		blockBytes: dmrfec.Rate12DataBytes, group: true,
	},
	{
		name: "group, four blocks, four pad", block: "82440000022fcdee8400caf8",
		blocks: 4, pad: 4, sendSeq: 0, to: 2, from: 3132910, payload: 40,
		blockBytes: dmrfec.Rate12DataBytes, group: true,
	},
	{
		name: "group, four blocks, two pad", block: "82420000022fcdee84009070",
		blocks: 4, pad: 2, sendSeq: 0, to: 2, from: 3132910, payload: 42,
		blockBytes: dmrfec.Rate12DataBytes, group: true,
	},
}

func decodeHeader(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(stripSpaces(s))
	if err != nil {
		t.Fatalf("fixture %q: %v", s, err)
	}
	return b
}

func stripSpaces(s string) string {
	out := make([]byte, 0, len(s))
	for i := range len(s) {
		if s[i] != ' ' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// TestEveryCapturedDataHeaderParsesToItsMeasuredFields is the differential:
// three headers, every field checked against what the transmission actually
// carried rather than against what the header claims about itself.
//
// The pad count is the interesting one. It is asserted here against
// [dmrfec.PadOctets] computed from the payload length and the block count, so
// the test fails if either the derivation or the field reading is wrong — and
// it agreeing three times for three different lengths is why octet 1's low
// nibble is called a pad count rather than guessed at.
func TestEveryCapturedDataHeaderParsesToItsMeasuredFields(t *testing.T) {
	for _, tc := range capturedHeaders {
		t.Run(tc.name, func(t *testing.T) {
			block := decodeHeader(t, tc.block)
			h, err := dmrfec.ParseDataHeader(block)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			if h.Blocks != tc.blocks {
				t.Errorf("blocks to follow %d, want %d", h.Blocks, tc.blocks)
			}
			if h.Pad != tc.pad {
				t.Errorf("pad %d, want %d", h.Pad, tc.pad)
			}
			if derived := dmrfec.PadOctets(tc.payload, int(tc.blocks), tc.blockBytes); derived != int(tc.pad) {
				t.Errorf("PadOctets(%d, %d, %d) is %d and the header says %d",
					tc.payload, tc.blocks, tc.blockBytes, derived, tc.pad)
			}
			if h.To != tc.to || h.From != tc.from {
				t.Errorf("addressed %d → %d, want %d → %d", h.From, h.To, tc.from, tc.to)
			}
			if h.SendSeq != tc.sendSeq {
				t.Errorf("send sequence %d, want %d", h.SendSeq, tc.sendSeq)
			}
			if h.Confirmed != (tc.blockBytes == dmrfec.Rate34DataBytes) {
				t.Errorf("confirmed is %v; every Rate 3/4 capture is confirmed and every Rate 1/2 one is not", h.Confirmed)
			}
			if h.SAP != dmrfec.SAPIPPacketData {
				t.Errorf("service access point %d, want %d (IP based packet data)", h.SAP, dmrfec.SAPIPPacketData)
			}
			if h.Group != tc.group {
				t.Errorf("group bit is %v, want %v", h.Group, tc.group)
			}
			if h.Response != tc.response {
				t.Errorf("response bit is %v, want %v", h.Response, tc.response)
			}
		})
	}
}

// TestARebuiltDataHeaderIsIdentical closes the loop. A builder that agreed
// with the parser and not with the wire would pass a round-trip test and fail
// on air, which is the failure §8a calls two mistakes cancelling out.
func TestARebuiltDataHeaderIsIdentical(t *testing.T) {
	for _, tc := range capturedHeaders {
		t.Run(tc.name, func(t *testing.T) {
			want := decodeHeader(t, tc.block)
			h, err := dmrfec.ParseDataHeader(want)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			got, err := dmrfec.BuildDataHeader(h)
			if err != nil {
				t.Fatalf("building: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("rebuilt header differs\n have %x\n want %x", got, want)
			}
		})
	}
}

// TestTheDataHeaderCRCMaskIsTheMeasuredOne guards the finding itself.
//
// ETSI clause B.3.9 gives 0xCCCC for a data header and the wire says 0x3333.
// A hundred and twenty-eight combinations were searched and one matched all
// three headers. **To see this fail, change dataHeaderCRCMask to 0xCCCC** —
// which is what a future reader who trusts the standard over the capture will
// try, and this test is the note that stops them.
func TestTheDataHeaderCRCMaskIsTheMeasuredOne(t *testing.T) {
	for _, tc := range capturedHeaders {
		block := decodeHeader(t, tc.block)
		got, err := dmrfec.DataHeaderCRC(block)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		want := uint16(block[10])<<8 | uint16(block[11])
		if got != want {
			t.Errorf("%s: CRC-16 computed %#04x, wire carries %#04x", tc.name, got, want)
		}
	}
}

// TestAHeaderOutsideItsFieldsIsRefused checks the guards rather than the
// happy path, because a header built from a bad block count is a transmission
// a radio gives up on with nothing in the log to say why.
func TestAHeaderOutsideItsFieldsIsRefused(t *testing.T) {
	good := dmrfec.DataHeader{To: 3155373, From: 3132910, Response: true, SAP: dmrfec.SAPIPPacketData, Blocks: 6, Pad: 4, SendSeq: 5}
	for _, tc := range []struct {
		name string
		edit func(h *dmrfec.DataHeader)
	}{
		{"no blocks", func(h *dmrfec.DataHeader) { h.Blocks = 0 }},
		{"too many blocks", func(h *dmrfec.DataHeader) { h.Blocks = 16 }},
		{"pad past a nibble", func(h *dmrfec.DataHeader) { h.Pad = 16 }},
		{"sap past a nibble", func(h *dmrfec.DataHeader) { h.SAP = 16 }},
		{"send sequence past three bits", func(h *dmrfec.DataHeader) { h.SendSeq = 8 }},
		{"destination past 24 bits", func(h *dmrfec.DataHeader) { h.To = 1 << 24 }},
		{"source past 24 bits", func(h *dmrfec.DataHeader) { h.From = 1 << 24 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := good
			tc.edit(&h)
			if _, err := dmrfec.BuildDataHeader(h); err == nil {
				t.Errorf("built a header with %s", tc.name)
			}
		})
	}
}

// TestACorruptDataHeaderIsRefused proves the CRC check is load-bearing rather
// than decorative: flip any octet and parsing must fail.
func TestACorruptDataHeaderIsRefused(t *testing.T) {
	block := decodeHeader(t, capturedHeaders[0].block)
	for i := range block {
		corrupt := append([]byte(nil), block...)
		corrupt[i] ^= 0x01
		if _, err := dmrfec.ParseDataHeader(corrupt); err == nil {
			t.Errorf("octet %d flipped and the header still parsed", i)
		}
	}
}
