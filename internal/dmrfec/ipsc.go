package dmrfec

// IP Site Connect packs the vocoder payload differently from a DMR burst, and
// this is where the two meet.

const (
	// IPSCCoreBytes is the vocoder payload in one IPSC voice frame.
	IPSCCoreBytes = 19
	// IPSCSlotBits is the space each vocoder frame occupies inside it.
	//
	// **Fifty, not forty-nine.** Three 49-bit frames would pack into 147 bits
	// and the stride would be 49; it is not. A silence transmission repeats one
	// vocoder frame, and in a captured core bits 0 to 99 match bits 50 to 149
	// exactly while every other offset matches nothing. So each frame sits in a
	// 50-bit slot with one bit to spare, and 152 - 150 leaves two more at the
	// end.
	IPSCSlotBits = 50
)

// UnpackIPSCCore extracts the three vocoder frames from an IPSC voice payload.
//
// # How the layout was established
//
// The stride came from a silence transmission, where one frame repeats: the
// self-similarity of a captured core peaks at exactly 50 bits and nowhere else.
//
// That left one question — whether the spare bit in each slot leads or trails —
// and it was settled by a cross-check between two protocols that have nothing
// to do with each other. Reading the frame as the **first** 49 bits of the slot
// gives 0x1F003533F19C1 for silence. Decoding the Homebrew captures in
// testdata/hbp/ through this package's FEC gives 0x1F003533F19C1 as by far the
// most common parameter frame, 236 times. The other reading gives a value that
// appears in the Homebrew captures not once.
//
// A Motorola XPR8300 speaking IP Site Connect and an MMDVM hotspot speaking
// Homebrew produce the identical vocoder frame for silence. That is the
// strongest evidence in this package: it confirms the packing, the FEC, and
// that the two protocols carry the same audio, in one observation.
func UnpackIPSCCore(core []byte) ([FramesPerBurst]Parameters, bool) {
	var out [FramesPerBurst]Parameters
	if len(core) != IPSCCoreBytes {
		return out, false
	}
	for i := 0; i < FramesPerBurst; i++ {
		out[i] = Parameters(readBits(core, i*IPSCSlotBits, ParameterBits))
	}
	return out, true
}

// PackIPSCCore is the inverse: three vocoder frames into a 19-byte payload.
//
// The spare bit in each slot and the two at the end are written as zero. That
// is what every captured core has in them, and a value nothing has ever
// observed would be a guess.
func PackIPSCCore(frames [FramesPerBurst]Parameters) []byte {
	out := make([]byte, IPSCCoreBytes)
	for i, f := range frames {
		writeBits(out, i*IPSCSlotBits, ParameterBits, uint64(f))
	}
	return out
}

// BurstFromIPSC builds a DMR burst from an IPSC vocoder payload, adding the FEC
// each frame needs and placing the 48-bit field that belongs in the middle.
//
// **The parameter bits are copied, never inspected.** Only the wrapper changes,
// so the audio a radio reproduces is the audio the originating radio encoded.
// See docs/adr/ADR-0037.
func BurstFromIPSC(core []byte, middle uint64) ([]byte, bool) {
	frames, ok := UnpackIPSCCore(core)
	if !ok {
		return nil, false
	}
	protected := make([][]byte, FramesPerBurst)
	for i, f := range frames {
		protected[i] = Encode(f)
	}
	return AssembleBurst(protected, middle)
}

// IPSCFromBurst is the reverse: a DMR burst to an IPSC vocoder payload.
//
// corrected reports how many bit errors the FEC repaired on the way. A burst
// that arrived damaged yields the parameters the radio *sent*, which is the
// point of the codes.
func IPSCFromBurst(burst []byte) (core []byte, corrected int, ok bool) {
	frames, ok := VocoderFrames(burst)
	if !ok {
		return nil, 0, false
	}
	var params [FramesPerBurst]Parameters
	for i, f := range frames {
		p, n, fok := Decode(f)
		if !fok {
			return nil, 0, false
		}
		params[i] = p
		corrected += n
	}
	return PackIPSCCore(params), corrected, true
}

func readBits(b []byte, off, n int) uint64 {
	var v uint64
	for i := 0; i < n; i++ {
		bit := off + i
		v = v<<1 | uint64(b[bit/8]>>uint(7-bit%8)&1)
	}
	return v
}

func writeBits(b []byte, off, n int, v uint64) {
	for i := 0; i < n; i++ {
		if v>>uint(n-1-i)&1 == 1 {
			bit := off + i
			b[bit/8] |= 1 << uint(7-bit%8)
		}
	}
}
