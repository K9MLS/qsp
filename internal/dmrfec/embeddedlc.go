package dmrfec

import "fmt"

// The embedded Link Control: a 9-byte LC spread across four bursts of a voice
// superframe.
//
// # What this is for
//
// A Talker Alias is a Link Control PDU (see talkeralias.go), and a Link
// Control PDU travelling during a voice call travels here — in the 32-bit
// fragment at the centre of bursts B, C, D and E of a superframe, which
// `MiddleForPosition` already knows where to put.
//
// # Source
//
// ETSI TS 102 361-1 V1.4.5 (2007-12), annex B. **A different part of the
// standard from the Talker Alias itself**, which is in part 2: the alias says
// what the bits mean and this says how they survive the air. §B.2.1 and figure
// B.3 are the matrix and the interleave, table B.16 is the Hamming (16,11,4)
// generator matrix, and §B.3.11 is the five-bit checksum.
//
// The coding predates the alias by a decade and has not changed, which is why
// a 2007 part 1 is the right document for it.
//
// # The shape, from figure B.3
//
// An 8-row by 16-column encode matrix carrying 77 information bits:
//
//   - rows 1 and 2: eleven LC bits each, then five Hamming parity bits
//   - rows 3 to 7: ten LC bits, one checksum bit, then five Hamming parity
//   - row 8: sixteen column parity bits, chosen so each column has an even
//     number of ones
//
// 11 + 11 + 5×10 is the 72 bits of the LC, and the five checksum bits sit in
// column 10 of rows 3 to 7 — most significant first, CS(4) in row 3.
//
// The transmit matrix is four rows of 32 bits, filled by **reading the encode
// matrix down each column and across**, then writing along each transmit row.
// So burst 1 is columns 0 to 3, burst 2 columns 4 to 7, and so on.
//
// **The standard prints its own answer for that**, which is the check worth
// having: figure B.3 lists burst 1 as beginning LC(71), LC(60), LC(49),
// LC(39), LC(29), LC(19), LC(9), PC(15) and ending LC(16), LC(6), PC(12), and
// the column reading reproduces it. A reading that did not match those printed
// bits would be a reading to throw away.

// EmbeddedLCBursts is how many bursts carry one embedded Link Control.
//
// Four: bursts B to E of a superframe. Burst A carries the synchronisation
// pattern and burst F carries the Null message.
const EmbeddedLCBursts = 4

// EmbeddedLCFragmentBits is the width of one burst's fragment.
const EmbeddedLCFragmentBits = 32

// hamming16_11_4 is table B.16's generator matrix, as the five parity bits
// each of the eleven data positions contributes.
//
// Row i of the table is the codeword for a message with only bit i set, and
// its first eleven columns are the identity — so the code is systematic and
// only the last five columns matter. Those five are what is stored.
//
// **Transcribed, not derived.** The table is generated from the primitive
// (15,11,3) code with G(x) = x⁴ + x + 1, and reproducing that derivation would
// be a second thing to get right where the standard has already printed the
// answer.
var hamming16_11_4 = [11][5]byte{
	{1, 0, 0, 1, 1},
	{1, 1, 0, 1, 0},
	{1, 1, 1, 1, 1},
	{1, 1, 1, 0, 0},
	{0, 1, 1, 1, 0},
	{1, 0, 1, 0, 1},
	{0, 1, 0, 1, 1},
	{1, 0, 1, 1, 0},
	{1, 1, 0, 0, 1},
	{0, 1, 1, 0, 1},
	{0, 0, 1, 1, 1},
}

// hammingParity16 returns the five parity bits for eleven data bits.
func hammingParity16(data []byte) [5]byte {
	var p [5]byte
	for i, bit := range data {
		if bit&1 == 0 {
			continue
		}
		for j := 0; j < 5; j++ {
			p[j] ^= hamming16_11_4[i][j]
		}
	}
	return p
}

// EmbeddedLCChecksum is the five-bit checksum over a nine-octet Link Control,
// §B.3.11.
//
// The sum of the nine octets modulo 31, which yields a value from 0 to 30.
// **Not 0 to 31**: the standard says so and the modulus makes it so, and a
// five-bit field that never reaches 31 is worth knowing about rather than
// discovering from a decoder that rejects one frame in thirty-one.
func EmbeddedLCChecksum(lc []byte) (byte, error) {
	if len(lc) != LinkControlBytes {
		return 0, fmt.Errorf("dmrfec: a link control is %d octets, got %d",
			LinkControlBytes, len(lc))
	}
	sum := 0
	for _, b := range lc {
		sum += int(b)
	}
	return byte(sum % 31), nil
}

// EncodeEmbeddedLC turns a nine-octet Link Control into four 32-bit fragments,
// one per burst, in the order they are transmitted.
//
// Each fragment is returned as a uint32 ready for `EmbeddedMiddle` alongside
// an EMB, or for `MiddleForPosition` which supplies the EMB itself.
func EncodeEmbeddedLC(lc []byte) ([EmbeddedLCBursts]uint32, error) {
	var out [EmbeddedLCBursts]uint32

	cs, err := EmbeddedLCChecksum(lc)
	if err != nil {
		return out, err
	}

	// LC(71) down to LC(0), most significant bit of octet 0 first.
	bits := make([]byte, 0, LinkControlBytes*8)
	for _, b := range lc {
		for i := 7; i >= 0; i-- {
			bits = append(bits, (b>>uint(i))&1)
		}
	}

	// The encode matrix: 8 rows of 16.
	var m [8][16]byte
	at := 0
	take := func(n int) []byte {
		out := bits[at : at+n]
		at += n
		return out
	}

	// Rows 1 and 2 carry eleven LC bits and nothing else before their parity.
	for row := 0; row < 2; row++ {
		data := take(11)
		copy(m[row][:11], data)
		p := hammingParity16(data)
		copy(m[row][11:], p[:])
	}
	// Rows 3 to 7 carry ten LC bits, then one checksum bit in column 10.
	// CS(4) is in row 3, so the bits go most significant first.
	for row := 2; row < 7; row++ {
		data := take(10)
		copy(m[row][:10], data)
		m[row][10] = (cs >> uint(4-(row-2))) & 1
		// The Hamming code protects all eleven information columns, the
		// checksum bit included.
		p := hammingParity16(m[row][:11])
		copy(m[row][11:], p[:])
	}
	if at != LinkControlBytes*8 {
		return out, fmt.Errorf("dmrfec: placed %d of %d link control bits",
			at, LinkControlBytes*8)
	}

	// Row 8: a parity bit per column, chosen so each column holds an even
	// number of ones.
	for col := 0; col < 16; col++ {
		var parity byte
		for row := 0; row < 7; row++ {
			parity ^= m[row][col]
		}
		m[7][col] = parity
	}

	// The interleave: read down each column and across, write along each
	// transmit row. Four rows of 32 bits, so each row takes four columns.
	seq := make([]byte, 0, 128)
	for col := 0; col < 16; col++ {
		for row := 0; row < 8; row++ {
			seq = append(seq, m[row][col])
		}
	}
	for burst := 0; burst < EmbeddedLCBursts; burst++ {
		var v uint32
		for i := 0; i < EmbeddedLCFragmentBits; i++ {
			v = v<<1 | uint32(seq[burst*EmbeddedLCFragmentBits+i])
		}
		out[burst] = v
	}
	return out, nil
}

// DecodeEmbeddedLC reassembles a Link Control from four fragments.
//
// It reverses the interleave and verifies the checksum, and reports whether
// the result is a Link Control rather than four fragments of something else.
// **It does not correct errors**: the Hamming rows and the column parity could
// locate and fix a single bit, and QSP reads these from its own output and from
// a capture rather than off the air, where nothing has corrupted them. A
// corrector nothing exercises is a corrector nobody can trust — the frames QSP
// relays keep their own FEC untouched, which is the same reasoning ADR-0034
// applies to audio.
func DecodeEmbeddedLC(fragments [EmbeddedLCBursts]uint32) ([]byte, bool) {
	seq := make([]byte, 0, 128)
	for _, v := range fragments {
		for i := EmbeddedLCFragmentBits - 1; i >= 0; i-- {
			seq = append(seq, byte(v>>uint(i))&1)
		}
	}

	var m [8][16]byte
	for i, bit := range seq {
		m[i%8][i/8] = bit
	}

	bits := make([]byte, 0, LinkControlBytes*8)
	for row := 0; row < 2; row++ {
		bits = append(bits, m[row][:11]...)
	}
	var cs byte
	for row := 2; row < 7; row++ {
		bits = append(bits, m[row][:10]...)
		cs = cs<<1 | m[row][10]
	}
	if len(bits) != LinkControlBytes*8 {
		return nil, false
	}

	lc := make([]byte, LinkControlBytes)
	for i, bit := range bits {
		if bit == 1 {
			lc[i/8] |= 1 << uint(7-i%8)
		}
	}
	want, err := EmbeddedLCChecksum(lc)
	if err != nil || want != cs {
		return nil, false
	}
	return lc, true
}

// EmbeddedLCMiddles turns a Link Control into the four 48-bit burst middles
// that carry it, EMB included.
//
// This is the whole journey in one call: a 9-byte LC in, and the four values
// `AssembleBurst` wants for bursts B to E of a superframe. The EMB carries the
// colour code and the LCSS that says where each fragment sits, which
// `MiddleForPosition` already derives from the position.
//
// Positions 1 to 4 are bursts B to E. Position 0 is burst A, which carries the
// synchronisation pattern instead, and position 5 is burst F, which carries
// the Null message — so a caller iterating a superframe uses these for the
// four in the middle and leaves the ends alone.
func EmbeddedLCMiddles(lc []byte, colourCode uint8) ([EmbeddedLCBursts]uint64, error) {
	var out [EmbeddedLCBursts]uint64

	fragments, err := EncodeEmbeddedLC(lc)
	if err != nil {
		return out, err
	}
	for i, fragment := range fragments {
		middle, err := MiddleForPosition(i+1, colourCode, fragment)
		if err != nil {
			return out, fmt.Errorf("dmrfec: burst %c of the superframe: %w",
				'B'+rune(i), err)
		}
		out[i] = middle
	}
	return out, nil
}
