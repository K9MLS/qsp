package dmrfec

import "fmt"

// A DMR data burst — a voice header, a terminator, or signalling — carries 96
// bits of payload inside a BPTC(196,96) block. That is what tells a receiving
// radio who is talking to whom, and a bridge has to produce one at the start
// and end of every transmission.

// BPTC sizes.
const (
	// BPTCPayloadBits is what a data burst carries: 72 bits of Link Control
	// and a 24-bit checksum.
	BPTCPayloadBits = 96
	// BPTCCodedBits is the block those 96 bits are protected into.
	BPTCCodedBits = 196
	// bptcRows and bptcCols are the block's shape: rows carry Hamming(15,11,3)
	// and columns Hamming(13,9,3), which is what "block product turbo code"
	// means here — every bit is covered twice, once along each axis.
	bptcRows = 13
	bptcCols = 15
	// bptcInterleave is the stride the 196 bits are interleaved by.
	//
	// **Measured, not assumed.** Taking the deinterleaved bit at (i*181)%196
	// gives valid Hamming(15,11) parity on 252 of 252 rows across the 28 real
	// data bursts in testdata/hbp/. The inverse mapping gives zero of 252.
	bptcInterleave = 181
)

// dataBurstHalves returns the two 98-bit payload regions of a 264-bit burst,
// which sit either side of the slot type and synchronisation fields.
func dataBurstHalves(burst []byte) ([]byte, bool) {
	if len(burst) != BurstBytes {
		return nil, false
	}
	b := BurstBitsFrom(burst)
	out := make([]byte, 0, BPTCCodedBits)
	out = append(out, b[0:98]...)
	out = append(out, b[166:264]...)
	return out, true
}

func hamming15Parity(d []byte) [4]byte {
	return [4]byte{
		d[0] ^ d[1] ^ d[2] ^ d[3] ^ d[5] ^ d[7] ^ d[8],
		d[1] ^ d[2] ^ d[3] ^ d[4] ^ d[6] ^ d[8] ^ d[9],
		d[2] ^ d[3] ^ d[4] ^ d[5] ^ d[7] ^ d[9] ^ d[10],
		d[0] ^ d[1] ^ d[2] ^ d[4] ^ d[6] ^ d[7] ^ d[10],
	}
}

func hamming13Parity(d []byte) [4]byte {
	return [4]byte{
		d[0] ^ d[1] ^ d[3] ^ d[5] ^ d[6],
		d[0] ^ d[1] ^ d[2] ^ d[4] ^ d[6] ^ d[7],
		d[0] ^ d[1] ^ d[2] ^ d[3] ^ d[5] ^ d[7] ^ d[8],
		d[0] ^ d[2] ^ d[4] ^ d[5] ^ d[8],
	}
}

// DecodeBPTC extracts the 96 payload bits from a data burst.
//
// rowsValid reports how many of the nine payload rows carried correct
// Hamming(15,11,3) parity, out of nine. A burst that is not a BPTC block at all
// scores near zero, which is how a caller tells signalling from noise without
// this package having to guess at burst types.
func DecodeBPTC(burst []byte) (payload []byte, rowsValid int, ok bool) {
	raw, ok := dataBurstHalves(burst)
	if !ok {
		return nil, 0, false
	}
	block := make([]byte, BPTCCodedBits)
	for i := range block {
		block[i] = raw[(i*bptcInterleave)%BPTCCodedBits]
	}

	// Bit 0 is reserved; the block proper starts at 1.
	info := make([]byte, 0, 99)
	for r := 0; r < 9; r++ {
		row := block[1+r*bptcCols : 1+r*bptcCols+bptcCols]
		if hamming15Parity(row[:11]) == [4]byte(row[11:15]) {
			rowsValid++
		}
		info = append(info, row[:11]...)
	}
	// The first three information bits are reserved, leaving 96.
	return info[3:99], rowsValid, true
}

// EncodeBPTC builds the 196-bit coded block for 96 payload bits, ready to be
// placed in a data burst.
//
// EncodeBPTC(DecodeBPTC(b)) reproduces b for every data burst in
// testdata/hbp/, which is what makes it usable in the direction no capture can
// check directly.
func EncodeBPTC(payload []byte) ([]byte, error) {
	if len(payload) != BPTCPayloadBits {
		return nil, fmt.Errorf("dmrfec: BPTC payload is %d bits, want %d",
			len(payload), BPTCPayloadBits)
	}
	block := make([]byte, BPTCCodedBits)
	info := make([]byte, 3, 99)
	info = append(info, payload...)

	for r := 0; r < 9; r++ {
		row := block[1+r*bptcCols : 1+r*bptcCols+bptcCols]
		copy(row[:11], info[r*11:(r+1)*11])
		p := hamming15Parity(row[:11])
		copy(row[11:15], p[:])
	}
	// Rows 9 to 12 hold the column parity, and each of those rows must itself
	// satisfy the row code — which is why the columns are computed first and
	// the last four rows are then closed over.
	for c := 0; c < bptcCols; c++ {
		col := make([]byte, 9)
		for r := 0; r < 9; r++ {
			col[r] = block[1+r*bptcCols+c]
		}
		p := hamming13Parity(col)
		for r := 0; r < 4; r++ {
			block[1+(9+r)*bptcCols+c] = p[r]
		}
	}

	out := make([]byte, BPTCCodedBits)
	for i := range block {
		out[(i*bptcInterleave)%BPTCCodedBits] = block[i]
	}
	return out, nil
}

// AssembleDataBurst places a coded BPTC block into a 264-bit burst, around the
// slot type and synchronisation fields taken from a burst that already exists.
func AssembleDataBurst(coded []byte, template []byte) ([]byte, bool) {
	if len(coded) != BPTCCodedBits || len(template) != BurstBytes {
		return nil, false
	}
	b := BurstBitsFrom(template)
	copy(b[0:98], coded[0:98])
	copy(b[166:264], coded[98:196])
	return BurstBytesFrom(b), true
}
