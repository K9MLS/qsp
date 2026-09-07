package peers_test

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// textFrames returns the Homebrew frames of one whole text from a hotspot, in
// capture order: sixteen preamble CSBKs, a data header, five Rate 1/2 blocks.
func textFrames(tb testing.TB) []hbp.Data {
	tb.Helper()
	raw, err := os.ReadFile("../../testdata/hbp/hbp-text-preambles.pcap")
	if err != nil {
		tb.Fatalf("%v", err)
	}
	var out []hbp.Data
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < 20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[20:]
		if len(ip) < 20 || ip[9] != 17 {
			continue
		}
		if binary.BigEndian.Uint32(ip[12:16]) != 0xc0a8019b {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		d := udp[8:]
		if len(d) != 55 || string(d[:4]) != "DMRD" {
			continue
		}
		msg, err := hbp.Parse(d)
		if err != nil {
			continue
		}
		frame, ok := msg.(hbp.Data)
		if !ok {
			continue
		}
		out = append(out, frame)
	}
	if len(out) != 22 {
		tb.Fatalf("read %d frames of the text, want 22", len(out))
	}
	return out
}

// TestOneTextIsOneRowRatherThanTwo is the defect the operator saw.
//
// Two texts produced four rows in Last heard, and the longer, more prominent
// row of each pair carried no message: **sixteen preamble CSBKs at 1.86s and
// fifteen frames, beside the actual message at 140ms and six**. Each preamble
// has its own stream ID, so the call tracker was grouping them correctly and
// there really were two runs. Nothing was broken except what an operator was
// being shown.
//
// A preamble exists so receiving radios wake up. Nobody sent it.
func TestOneTextIsOneRowRatherThanTwo(t *testing.T) {
	frames := textFrames(t)

	var preambles, recorded int
	for _, f := range frames {
		if peers.IsPreambleForTest(f) {
			preambles++
			continue
		}
		recorded++
	}

	if preambles != 16 {
		t.Errorf("%d frames were taken for preambles, want 16", preambles)
	}
	// The header and its five content blocks. They share one stream ID and
	// arrive in 142 ms, so the tracker makes them one entry — the row that
	// means something.
	if recorded != 6 {
		t.Errorf("%d frames would be recorded, want 6", recorded)
	}
}

// TestTheMessageItselfIsNeverSuppressed is the guard on the test above, which
// would pass just as well if everything were suppressed.
func TestTheMessageItselfIsNeverSuppressed(t *testing.T) {
	for i, f := range textFrames(t) {
		if f.DataType == 0x3 {
			continue
		}
		if peers.IsPreambleForTest(f) {
			t.Errorf("frame %d carries data type %#x and was taken for a preamble",
				i, f.DataType)
		}
	}
}
