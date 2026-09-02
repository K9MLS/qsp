package dmrfec_test

import (
	"bytes"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
)

func dataBursts(tb testing.TB) []dmrdBurst {
	tb.Helper()
	var out []dmrdBurst
	for _, b := range readBursts(tb) {
		if b.FrameType == 2 {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		tb.Fatal("no data-sync bursts in the capture")
	}
	return out
}

// TestEveryDataBurstDecodesWithValidParity is the measurement that found the
// interleave.
//
// Taking the deinterleaved bit at (i*181)%196 gives correct Hamming(15,11,3)
// parity on every payload row of every data burst. The inverse mapping gives
// none. A wrong stride scores zero, so a hundred per cent is not a coincidence.
func TestEveryDataBurstDecodesWithValidParity(t *testing.T) {
	var rows, valid int
	for _, b := range dataBursts(t) {
		_, n, ok := dmrfec.DecodeBPTC(b.Burst)
		if !ok {
			t.Fatalf("a 33-byte data burst was rejected")
		}
		rows += 9
		valid += n
	}
	t.Logf("%d of %d payload rows carried valid parity", valid, rows)
	if valid != rows {
		t.Errorf("%d of %d rows valid; the deinterleave or the row code is wrong", valid, rows)
	}
}

// TestTheLinkControlMatchesTheProtocolHeader is the oracle that makes the
// decode trustworthy rather than merely self-consistent.
//
// A voice header burst carries the same source and destination as the Homebrew
// header that delivered it. Two independent encodings of the same fact, and
// they agree on all 28 bursts. Nothing about the BPTC layout could be wrong
// while this held.
func TestTheLinkControlMatchesTheProtocolHeader(t *testing.T) {
	for i, b := range dataBursts(t) {
		payload, _, ok := dmrfec.DecodeBPTC(b.Burst)
		if !ok {
			t.Fatalf("burst %d rejected", i)
		}
		dst := bitsToUint(payload[24:48])
		src := bitsToUint(payload[48:72])
		if dst != b.Destination || src != b.Source {
			t.Errorf("burst %d: link control says %d to %d, the header says %d to %d",
				i, src, dst, b.Source, b.Destination)
		}
	}
}

// TestTheBPTCRoundTripIsBitExact is the property the bridge depends on in the
// direction no capture can check.
//
// QSP has to *build* voice headers and terminators, and nothing captured shows
// a master sending one. Rebuilding a real burst from its own payload and
// getting the identical bits back is the strongest available substitute.
func TestTheBPTCRoundTripIsBitExact(t *testing.T) {
	for i, b := range dataBursts(t) {
		payload, _, _ := dmrfec.DecodeBPTC(b.Burst)
		coded, err := dmrfec.EncodeBPTC(payload)
		if err != nil {
			t.Fatalf("burst %d: %v", i, err)
		}
		out, ok := dmrfec.AssembleDataBurst(coded, b.Burst)
		if !ok {
			t.Fatalf("burst %d: assembly rejected", i)
		}
		if !bytes.Equal(out, b.Burst) {
			t.Fatalf("burst %d changed:\n  in  %x\n  out %x", i, b.Burst, out)
		}
	}
	t.Logf("%d data bursts rebuilt from their own payload, unchanged", len(dataBursts(t)))
}

// TestAVoiceBurstIsNotMistakenForABPTCBlock checks the signal a caller uses to
// tell one burst type from another.
func TestAVoiceBurstIsNotMistakenForABPTCBlock(t *testing.T) {
	var voiceRows, voiceValid int
	for _, b := range readBursts(t) {
		if !b.isVoice() {
			continue
		}
		_, n, ok := dmrfec.DecodeBPTC(b.Burst)
		if !ok {
			t.Fatal("a voice burst was rejected on length rather than content")
		}
		voiceRows += 9
		voiceValid += n
	}
	pct := voiceValid * 100 / voiceRows
	t.Logf("voice bursts read as BPTC: %d%% of rows pass parity", pct)
	if pct > 50 {
		t.Errorf("%d%% of rows in voice bursts pass the BPTC row code; the check cannot "+
			"distinguish a data burst from a voice burst", pct)
	}
}

// TestAShortPayloadIsRefused keeps the boundary honest.
func TestAShortPayloadIsRefused(t *testing.T) {
	if _, err := dmrfec.EncodeBPTC(make([]byte, 95)); err == nil {
		t.Error("a 95-bit payload was accepted")
	}
}

func bitsToUint(b []byte) uint32 {
	var v uint32
	for _, x := range b {
		v = v<<1 | uint32(x&1)
	}
	return v
}
