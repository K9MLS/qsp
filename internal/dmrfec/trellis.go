package dmrfec

// Rate 3/4 Trellis coding, ETSI TS 102 361-1 Annex B.2.4.
//
// # Why this exists
//
// **Every data block of a text message was dropped, silently, in both
// directions.** A confirmed data packet carries its payload in Rate 3/4 blocks,
// this package could read only BPTC(196,96), and the encoder returned nothing
// for a burst it could not decode. The preamble crossed the bridge, the data
// header crossed, and the message never did — so a radio at the far end saw a
// header promising blocks that never arrived, and nobody's text ever assembled.
//
// ADR-0045 recorded the gap when the text work was done: Rate 3/4 bursts are
// refused rather than truncated, and none appeared in the captured texts, so it
// had never yet mattered. The text captured for that ADR was short enough to
// fit in bursts BPTC can read; a real one is not.
//
// # Where the constants come from
//
// ETSI TS 102 361-1, a free download, on the same terms as the Golay,
// Reed-Solomon and BPTC constants already in this package. ADR-0040 settled
// that the DMR air interface is specified rather than guessed, and ADR-0029 —
// which forbids inventing a protocol — governs IP Site Connect, which has no
// published specification. The two sides of this bridge get different evidence
// standards on purpose.
//
// Table B.6 gives the sizes, B.7 the encoder state transitions, B.8 the
// constellation-to-dibit mapping, B.9 the interleave schedule and B.10 the
// transmit ordering.
//
// # What has been proved, and what has not
//
// **No capture anywhere contains a Rate 3/4 coded burst.** Every Homebrew
// fixture in testdata/hbp is voice — data types 0x1 and 0x2 and nothing else —
// and the IPSC captures carry decoded blocks rather than coded bursts, because
// the texts that exposed this went repeater to repeater and never crossed a
// hotspot. So the tables here are transcribed from the standard and checked
// only by this package agreeing with itself: encode then decode returns what
// went in, which a transcription error would satisfy just as well.
//
// **The block layout either side of them is proved.** Feeding the 18-octet
// blocks from qsp-session.pcap00 through the structure this file assumes —
// two octets of block serial number and CRC-9, then sixteen of payload —
// yields a UDP datagram carrying UTF-16 text, and the text is a message the
// operator typed: "I can't talk right now...". That settles the sizes, the
// prefix and the encoding; it says nothing about the trellis.
//
// This ships on the terms ADR-0041 set for the outbound IPSC path: reasoned
// rather than measured, marked plainly as such, and replaced by a measurement
// the moment one exists. **The capture that would settle it is a text sent
// from a hotspot**, which produces coded bursts from MMDVMHost; decoding one
// into the message that was typed is the proof, and it takes five minutes to
// record.

// Rate 3/4 sizes, Table B.6. 48 tribits in, 98 dibits out, and a flushing
// tribit of zero appended to empty the final state.
const (
	trellisTribits = 49 // 48 payload plus the flush
	trellisDibits  = 98
	// TrellisPayloadBytes is the information a Rate 3/4 block carries: 144
	// bits, where a BPTC block carries 96.
	//
	// **This is the six octets a capture shows.** A datagram carrying a Rate
	// 3/4 block is 60 bytes where one carrying a BPTC block is 54, and that
	// difference is how the defect was found.
	TrellisPayloadBytes = 18
)

// trellisTransitions is Table B.7: the constellation point produced by each
// input tribit in each of the eight FSM states.
//
// **The state is the previous input.** The specification notes that this
// encoder has the property that the current input becomes the next state, and
// that is what makes decoding a lookup rather than a Viterbi search: knowing
// the state, the observed point names the tribit that produced it.
var trellisTransitions = [8][8]byte{
	{0, 8, 4, 12, 2, 10, 6, 14},
	{4, 12, 2, 10, 6, 14, 0, 8},
	{1, 9, 5, 13, 3, 11, 7, 15},
	{5, 13, 3, 11, 7, 15, 1, 9},
	{3, 11, 7, 15, 1, 9, 5, 13},
	{7, 15, 1, 9, 5, 13, 3, 11},
	{2, 10, 6, 14, 0, 8, 4, 12},
	{6, 14, 0, 8, 4, 12, 2, 10},
}

// trellisPoints is Table B.8, the constellation point to dibit pair mapping,
// with the specification's ±1 and ±3 amplitudes written as the two-bit values
// they are transmitted as.
//
// The spec tabulates amplitudes because they are what the modulator emits.
// On the wire each is a dibit: +1 is 01, -1 is 00, +3 is 11, -3 is 10.
var trellisPoints = [16][2]byte{
	0:  {0b01, 0b00}, // +1 -1
	1:  {0b00, 0b00}, // -1 -1
	2:  {0b11, 0b10}, // +3 -3
	3:  {0b10, 0b10}, // -3 -3
	4:  {0b10, 0b00}, // -3 -1
	5:  {0b11, 0b00}, // +3 -1
	6:  {0b00, 0b10}, // -1 -3
	7:  {0b01, 0b10}, // +1 -3
	8:  {0b10, 0b11}, // -3 +3
	9:  {0b11, 0b11}, // +3 +3
	10: {0b00, 0b01}, // -1 +1
	11: {0b01, 0b01}, // +1 +1
	12: {0b01, 0b11}, // +1 +3
	13: {0b00, 0b11}, // -1 +3
	14: {0b11, 0b01}, // +3 +1
	15: {0b10, 0b01}, // -3 +1
}

// trellisInterleave is Table B.9, read as Table B.10 uses it: the dibit at
// transmit position i is the encoder's output dibit at trellisInterleave[i].
var trellisInterleave = [trellisDibits]int{
	0, 1, 8, 9, 16, 17, 24, 25, 32, 33, 40, 41, 48, 49, 56, 57,
	64, 65, 72, 73, 80, 81, 88, 89, 96, 97,
	2, 3, 10, 11, 18, 19, 26, 27, 34, 35, 42, 43, 50, 51, 58, 59,
	66, 67, 74, 75, 82, 83, 90, 91,
	4, 5, 12, 13, 20, 21, 28, 29, 36, 37, 44, 45, 52, 53, 60, 61,
	68, 69, 76, 77, 84, 85, 92, 93,
	6, 7, 14, 15, 22, 23, 30, 31, 38, 39, 46, 47, 54, 55, 62, 63,
	70, 71, 78, 79, 86, 87, 94, 95,
}

// pointOf inverts Table B.8: it names the constellation point a dibit pair
// stands for, or reports that the pair is not one the encoder can produce.
func pointOf(a, b byte) (byte, bool) {
	for p, pair := range trellisPoints {
		if pair[0] == a && pair[1] == b {
			return byte(p), true
		}
	}
	return 0, false
}

// DecodeTrellis recovers the 18 information octets from a Rate 3/4 data burst.
//
// It reports false for a burst whose dibits are not constellation points, or
// whose state sequence has no input that could have produced them. **There is
// no error correction here**: the trellis code corrects errors by searching
// paths, and a search that guesses at a member's message is worse than
// admitting the burst was unreadable. A burst that arrives over IP has already
// been corrected by the repeater that received it off the air.
func DecodeTrellis(burst []byte) ([]byte, bool) {
	raw, ok := dataBurstHalves(burst)
	if !ok {
		return nil, false
	}

	// Deinterleave: transmit position i carries encoder dibit
	// trellisInterleave[i], as Table B.10 tabulates.
	var dibits [trellisDibits][2]byte
	for i := 0; i < trellisDibits; i++ {
		dibits[trellisInterleave[i]] = [2]byte{raw[i*2], raw[i*2+1]}
	}

	tribits := make([]byte, 0, trellisTribits)
	state := byte(0)
	for i := 0; i < trellisTribits; i++ {
		a := dibits[i*2][0]<<1 | dibits[i*2][1]
		b := dibits[i*2+1][0]<<1 | dibits[i*2+1][1]
		point, ok := pointOf(a, b)
		if !ok {
			return nil, false
		}
		input := -1
		for in, want := range trellisTransitions[state] {
			if want == point {
				input = in
				break
			}
		}
		if input < 0 {
			return nil, false
		}
		tribits = append(tribits, byte(input))
		state = byte(input)
	}

	// The last tribit is the flush and carries no payload.
	bits := make([]byte, 0, 144)
	for _, t := range tribits[:trellisTribits-1] {
		bits = append(bits, (t>>2)&1, (t>>1)&1, t&1)
	}
	out := make([]byte, TrellisPayloadBytes)
	for i := 0; i < TrellisPayloadBytes*8; i++ {
		if bits[i] == 1 {
			out[i/8] |= 0x80 >> (i % 8)
		}
	}
	return out, true
}

// EncodeTrellis builds the 196 payload bits of a Rate 3/4 data burst from 18
// information octets, as one bit per byte in transmit order.
func EncodeTrellis(payload []byte) ([]byte, bool) {
	if len(payload) != TrellisPayloadBytes {
		return nil, false
	}
	bits := make([]byte, 0, TrellisPayloadBytes*8)
	for _, o := range payload {
		for i := 7; i >= 0; i-- {
			bits = append(bits, (o>>uint(i))&1)
		}
	}

	var dibits [trellisDibits][2]byte
	state := byte(0)
	for i := 0; i < trellisTribits; i++ {
		var input byte
		if i < trellisTribits-1 {
			input = bits[i*3]<<2 | bits[i*3+1]<<1 | bits[i*3+2]
		}
		point := trellisTransitions[state][input]
		pair := trellisPoints[point]
		dibits[i*2] = [2]byte{(pair[0] >> 1) & 1, pair[0] & 1}
		dibits[i*2+1] = [2]byte{(pair[1] >> 1) & 1, pair[1] & 1}
		state = input
	}

	out := make([]byte, trellisDibits*2)
	for i := 0; i < trellisDibits; i++ {
		d := dibits[trellisInterleave[i]]
		out[i*2], out[i*2+1] = d[0], d[1]
	}
	return out, true
}

// TrellisPoint returns the constellation point Table B.7 gives for an input
// tribit in a state. Exported for the tests that check the table's shape.
func TrellisPoint(state, input byte) byte { return trellisTransitions[state][input] }

// TrellisDibits returns the dibit pair Table B.8 maps a constellation point to.
func TrellisDibits(point byte) [2]byte { return trellisPoints[point] }

// TrellisInterleaveSchedule returns a copy of Table B.9.
func TrellisInterleaveSchedule() []int {
	out := make([]int, len(trellisInterleave))
	copy(out, trellisInterleave[:])
	return out
}
