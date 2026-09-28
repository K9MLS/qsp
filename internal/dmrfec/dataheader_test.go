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
	name    string
	block   string
	blocks  uint8
	pad     uint8
	sendSeq uint8
	to      uint32
	from    uint32
	payload int // the IPv4 total length of the packet these blocks carry
}{
	{
		name: "six blocks, four pad", block: "434430 25ad2f cdee86 58fbaa",
		blocks: 6, pad: 4, sendSeq: 5, to: 3155373, from: 3132910, payload: 88,
	},
	{
		name: "four blocks, fourteen pad", block: "434e30 25ad2f cdee84 684403",
		blocks: 4, pad: 14, sendSeq: 6, to: 3155373, from: 3132910, payload: 46,
	},
	{
		name: "four blocks, four pad", block: "434430 25ad2f cdee84 38f16e",
		blocks: 4, pad: 4, sendSeq: 3, to: 3155373, from: 3132910, payload: 56,
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
			if derived := dmrfec.PadOctets(tc.payload, int(tc.blocks)); derived != int(tc.pad) {
				t.Errorf("PadOctets(%d, %d) is %d and the header says %d",
					tc.payload, tc.blocks, derived, tc.pad)
			}
			if h.To != tc.to || h.From != tc.from {
				t.Errorf("addressed %d → %d, want %d → %d", h.From, h.To, tc.from, tc.to)
			}
			if h.SendSeq != tc.sendSeq {
				t.Errorf("send sequence %d, want %d", h.SendSeq, tc.sendSeq)
			}
			if h.SAP != dmrfec.SAPIPPacketData {
				t.Errorf("service access point %d, want %d (IP based packet data)", h.SAP, dmrfec.SAPIPPacketData)
			}
			if h.Group {
				t.Error("group bit set; every captured text is a private message")
			}
			if !h.Response {
				t.Error("response bit clear; it is set in all three captures")
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
