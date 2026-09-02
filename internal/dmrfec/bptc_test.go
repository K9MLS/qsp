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

// TestTheSlotTypeIsRebuiltExactly checks the twenty bits a receiver reads to
// learn what a burst is.
//
// QSP has to build these from scratch to emit a voice header or a terminator,
// and a wrong one is a burst that is silently ignored. Rebuilding the field
// from the colour code and data type alone and requiring the identical bits
// back, on every captured burst, is the same standard the BPTC round trip is
// held to.
func TestTheSlotTypeIsRebuiltExactly(t *testing.T) {
	var checked int
	for i, b := range dataBursts(t) {
		cc, dt, ok := dmrfec.SlotTypeOf(b.Burst)
		if !ok {
			t.Fatalf("burst %d has no readable slot type", i)
		}
		if dt != dmrfec.DataTypeVoiceLCHeader && dt != dmrfec.DataTypeTerminatorWithLC {
			t.Errorf("burst %d carries data type %#x; the capture should hold only "+
				"voice headers and terminators", i, dt)
			continue
		}
		built, err := dmrfec.SlotType(cc, dt)
		if err != nil {
			t.Fatalf("burst %d: %v", i, err)
		}
		bits := dmrfec.BurstBitsFrom(b.Burst)
		var want uint32
		for j := 0; j < 10; j++ {
			want = want<<1 | uint32(bits[98+j])
		}
		for j := 0; j < 10; j++ {
			want = want<<1 | uint32(bits[156+j])
		}
		if built != want {
			t.Errorf("burst %d slot type: built %#05x, capture carries %#05x", i, built, want)
		}
		checked++
	}
	t.Logf("%d slot types rebuilt from a colour code and a data type", checked)
}

// TestTheLinkControlChecksumVerifies is the Reed-Solomon (12,9) check.
//
// **The masks are not read from a table, they are measured.** Computing the
// parity of each captured burst and subtracting what the burst carries leaves
// one constant per data type. A wrong construction would leave 28 unrelated
// values, so agreement on all of them is the evidence — the same asymmetry
// that settled the vocoder interleave and the BPTC stride.
//
// It is also an oracle rather than a self-consistency check: the two masks
// partition the bursts into fourteen voice headers and fourteen terminators,
// and that partition must agree with the Slot Type's own data type, which is
// carried in a different part of the burst under a different code.
func TestTheLinkControlChecksumVerifies(t *testing.T) {
	counts := map[uint8]int{}
	for i, b := range dataBursts(t) {
		payload, _, ok := dmrfec.DecodeBPTC(b.Burst)
		if !ok {
			t.Fatalf("burst %d rejected", i)
		}
		_, dt, _ := dmrfec.SlotTypeOf(b.Burst)

		lc, ok := dmrfec.CheckLinkControl(payload, dt)
		if !ok {
			t.Errorf("burst %d: the Link Control checksum does not verify for data type %#x", i, dt)
			continue
		}
		counts[dt]++

		// The other data type's mask must not also verify, or the check would
		// be telling us nothing about which burst this is.
		other := dmrfec.DataTypeVoiceLCHeader
		if dt == dmrfec.DataTypeVoiceLCHeader {
			other = dmrfec.DataTypeTerminatorWithLC
		}
		if _, both := dmrfec.CheckLinkControl(payload, other); both {
			t.Errorf("burst %d verifies under both masks, so the mask distinguishes nothing", i)
		}

		// And the Link Control must still hold the addresses the protocol
		// header carried, which is the oracle the BPTC work already relies on.
		if got := uint32(lc[3])<<16 | uint32(lc[4])<<8 | uint32(lc[5]); got != 9999 {
			t.Errorf("burst %d Link Control destination is %d, want 9999", i, got)
		}
		if got := uint32(lc[6])<<16 | uint32(lc[7])<<8 | uint32(lc[8]); got != 3132910 {
			t.Errorf("burst %d Link Control source is %d, want 3132910", i, got)
		}
	}
	t.Logf("verified: %d voice headers, %d terminators",
		counts[dmrfec.DataTypeVoiceLCHeader], counts[dmrfec.DataTypeTerminatorWithLC])
	if counts[dmrfec.DataTypeVoiceLCHeader] == 0 || counts[dmrfec.DataTypeTerminatorWithLC] == 0 {
		t.Error("the capture should contain both voice headers and terminators")
	}
}

// TestARebuiltDataBurstIsIdentical is the strongest check available for a
// burst QSP has to originate.
//
// Nothing has ever captured a master sending a voice header, so there is no
// recording of the thing being built to compare against. Taking a real burst
// apart, keeping only the Link Control, and reconstructing the whole 33 bytes —
// forward error correction, Slot Type, synchronisation pattern — then requiring
// the identical bits back is the nearest substitute there is. It exercises
// every piece independently: the Reed-Solomon parity, the BPTC encoder, the
// Golay slot type and the sync field.
func TestARebuiltDataBurstIsIdentical(t *testing.T) {
	var rebuilt int
	for i, b := range dataBursts(t) {
		payload, _, ok := dmrfec.DecodeBPTC(b.Burst)
		if !ok {
			t.Fatalf("burst %d rejected", i)
		}
		cc, dt, _ := dmrfec.SlotTypeOf(b.Burst)
		lc, ok := dmrfec.CheckLinkControl(payload, dt)
		if !ok {
			t.Fatalf("burst %d: checksum does not verify", i)
		}

		got, err := dmrfec.BuildDataBurst(cc, dt, lc)
		if err != nil {
			t.Fatalf("burst %d: %v", i, err)
		}
		if !bytes.Equal(got, b.Burst) {
			t.Errorf("burst %d rebuilt differently:\n got %x\nwant %x", i, got, b.Burst)
			continue
		}
		rebuilt++
	}
	t.Logf("%d data bursts rebuilt from their Link Control alone, bit-exact", rebuilt)
	if rebuilt == 0 {
		t.Fatal("nothing was rebuilt")
	}
}

// TestLinkControlForRoundTrips checks the addresses survive into a burst.
func TestLinkControlForRoundTrips(t *testing.T) {
	lc := dmrfec.LinkControlFor(2, 3132910)
	burst, err := dmrfec.BuildDataBurst(11, dmrfec.DataTypeVoiceLCHeader, lc)
	if err != nil {
		t.Fatalf("%v", err)
	}
	cc, dt, ok := dmrfec.SlotTypeOf(burst)
	if !ok || cc != 11 || dt != dmrfec.DataTypeVoiceLCHeader {
		t.Fatalf("slot type reads cc=%d dt=%#x, want 11 and a voice header", cc, dt)
	}
	payload, rows, ok := dmrfec.DecodeBPTC(burst)
	if !ok {
		t.Fatal("a burst this package built could not be decoded")
	}
	if rows != 9 {
		t.Errorf("%d of 9 payload rows carry valid parity", rows)
	}
	back, ok := dmrfec.CheckLinkControl(payload, dmrfec.DataTypeVoiceLCHeader)
	if !ok {
		t.Fatal("the checksum of a burst this package built does not verify")
	}
	if got := uint32(back[3])<<16 | uint32(back[4])<<8 | uint32(back[5]); got != 2 {
		t.Errorf("destination came back as %d, want 2", got)
	}
	if got := uint32(back[6])<<16 | uint32(back[7])<<8 | uint32(back[8]); got != 3132910 {
		t.Errorf("source came back as %d, want 3132910", got)
	}
}
