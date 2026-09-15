package dmrfec

import (
	"strings"
	"testing"
)

// The embedded Link Control, against figure B.3's own printed bits.

// TestTheInterleaveReproducesFigureBThreesPrintedBursts is the check that
// decides whether the matrix was read correctly.
//
// **The standard prints its own answer.** Figure B.3 lists burst 1 as
// beginning LC(71), LC(60), LC(49), LC(39), LC(29), LC(19), LC(9), PC(15) and
// ending LC(16), LC(6), PC(12), and names the first bit of bursts 2, 3 and 4
// as LC(67), LC(63) and H1(3). A column reading that did not land on those is
// a reading to throw away.
//
// This checks it by position rather than by value: a Link Control with exactly
// one bit set must put that bit exactly where the figure says.
func TestTheInterleaveReproducesFigureBThreesPrintedBursts(t *testing.T) {
	// lcBit builds a Link Control with one bit set, numbered LC(71) down to
	// LC(0) as the figure numbers them.
	lcBit := func(n int) []byte {
		lc := make([]byte, LinkControlBytes)
		at := 71 - n
		lc[at/8] |= 1 << uint(7-at%8)
		return lc
	}
	// where reports which burst and offset a single LC bit lands at.
	where := func(t *testing.T, n int) (burst, offset int) {
		t.Helper()
		frags, err := EncodeEmbeddedLC(lcBit(n))
		if err != nil {
			t.Fatalf("encoding LC(%d): %v", n, err)
		}
		for b, v := range frags {
			for i := 0; i < EmbeddedLCFragmentBits; i++ {
				if v>>uint(EmbeddedLCFragmentBits-1-i)&1 == 1 {
					// The first set bit is the data bit; parity bits follow
					// from it, so the earliest position is the one the figure
					// names.
					return b, i
				}
			}
		}
		t.Fatalf("LC(%d) did not appear in any burst", n)
		return -1, -1
	}

	for _, tc := range []struct {
		lc            int
		burst, offset int
		note          string
	}{
		// Burst 1, the first eight bits: one per row of column 0.
		{71, 0, 0, "burst 1 bit 0"},
		{60, 0, 1, "burst 1 bit 1"},
		{49, 0, 2, "burst 1 bit 2"},
		{39, 0, 3, "burst 1 bit 3"},
		{29, 0, 4, "burst 1 bit 4"},
		{19, 0, 5, "burst 1 bit 5"},
		{9, 0, 6, "burst 1 bit 6"},
		// Column 1 follows immediately.
		{70, 0, 8, "burst 1 bit 8"},
		{59, 0, 9, "burst 1 bit 9"},
		// The end of burst 1, column 3.
		{16, 0, 29, "burst 1, third from last"},
		{6, 0, 30, "burst 1, second from last"},
		// The first bit of each following burst.
		{67, 1, 0, "burst 2 bit 0"},
		{56, 1, 1, "burst 2 bit 1"},
		{63, 2, 0, "burst 3 bit 0"},
		{52, 2, 1, "burst 3 bit 1"},
	} {
		gotBurst, gotOffset := where(t, tc.lc)
		if gotBurst != tc.burst || gotOffset != tc.offset {
			t.Errorf("LC(%d) lands at burst %d offset %d, and figure B.3 puts it "+
				"at burst %d offset %d (%s)",
				tc.lc, gotBurst+1, gotOffset, tc.burst+1, tc.offset, tc.note)
		}
	}
}

// TestTheChecksumIsTheSumOfTheOctetsModuloThirtyOne, §B.3.11.
//
// And it never reaches 31, which the modulus makes so and the standard states:
// a five-bit field with a hole at the top is worth knowing about rather than
// finding out from a decoder that rejects one frame in thirty-one.
func TestTheChecksumIsTheSumOfTheOctetsModuloThirtyOne(t *testing.T) {
	for _, tc := range []struct {
		lc   []byte
		want byte
	}{
		{make([]byte, 9), 0},
		{[]byte{1, 0, 0, 0, 0, 0, 0, 0, 0}, 1},
		{[]byte{31, 0, 0, 0, 0, 0, 0, 0, 0}, 0},
		{[]byte{30, 0, 0, 0, 0, 0, 0, 0, 0}, 30},
		// The largest sum the standard names: nine octets of 255 is 2295.
		{[]byte{255, 255, 255, 255, 255, 255, 255, 255, 255}, byte(2295 % 31)},
	} {
		got, err := EmbeddedLCChecksum(tc.lc)
		if err != nil {
			t.Errorf("%v: %v", tc.lc, err)
			continue
		}
		if got != tc.want {
			t.Errorf("the checksum of %v is %d, want %d", tc.lc, got, tc.want)
		}
		if got > 30 {
			t.Errorf("the checksum is %d; §B.3.11 gives the range 0 to 30", got)
		}
	}

	if _, err := EmbeddedLCChecksum(make([]byte, 8)); err == nil {
		t.Error("an eight-octet link control was accepted")
	}
}

// TestTheHammingGeneratorIsTableBSixteen.
//
// Transcribed rather than derived, so this is the transcription checked: each
// row of table B.16 is the codeword for a message with one bit set, and its
// last five columns are what is stored. The code corrects one error and
// detects two, so its minimum distance is four — which every pair of rows
// plus their identity columns must satisfy.
func TestTheHammingGeneratorIsTableBSixteen(t *testing.T) {
	// Table B.16's parity columns, transcribed a second time from the
	// standard rather than copied from the code under test.
	want := [11][5]byte{
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
	for i := range want {
		data := make([]byte, 11)
		data[i] = 1
		if got := hammingParity16(data); got != want[i] {
			t.Errorf("row %d of the generator gives parity %v, want %v", i, got, want[i])
		}
	}

	// Minimum distance four: every non-zero codeword has at least four ones.
	// Checking all 2048 of them is cheap and is what "(16,11,4)" claims.
	for msg := 1; msg < 1<<11; msg++ {
		data := make([]byte, 11)
		weight := 0
		for i := 0; i < 11; i++ {
			if msg>>uint(10-i)&1 == 1 {
				data[i] = 1
				weight++
			}
		}
		p := hammingParity16(data)
		for _, b := range p {
			if b == 1 {
				weight++
			}
		}
		if weight < 4 {
			t.Fatalf("the codeword for message %011b has weight %d; a (16,11,4) "+
				"code has minimum distance 4", msg, weight)
		}
	}
}

// TestEveryColumnHasEvenParity, per §B.2.1: the parity check bits shall be
// chosen so each column of the matrix has an even number of ones.
func TestEveryColumnHasEvenParity(t *testing.T) {
	lc := LinkControlFor(3148, 3132911, false)
	frags, err := EncodeEmbeddedLC(lc)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	// Undo the interleave and count each column.
	seq := make([]byte, 0, 128)
	for _, v := range frags {
		for i := EmbeddedLCFragmentBits - 1; i >= 0; i-- {
			seq = append(seq, byte(v>>uint(i))&1)
		}
	}
	for col := 0; col < 16; col++ {
		var parity byte
		for row := 0; row < 8; row++ {
			parity ^= seq[col*8+row]
		}
		if parity != 0 {
			t.Errorf("column %d has an odd number of ones", col)
		}
	}
}

// TestALinkControlSurvivesTheEmbeddedCarriage is the round trip.
//
// Group voice, private voice and a Talker Alias, which is the one this was
// built for. **The builder and the reader agreeing is not the same as either
// matching a radio** — nothing here has been compared against bytes a
// Motorola sent, and until it has this is a reading of annex B.
func TestALinkControlSurvivesTheEmbeddedCarriage(t *testing.T) {
	alias, err := TalkerAliasPDUs("K9MLS ZELLO", TalkerAlias7Bit)
	if err != nil {
		t.Fatalf("building an alias: %v", err)
	}

	cases := [][]byte{
		LinkControlFor(3148, 3132911, false),
		LinkControlFor(3132910, 999999, true),
		make([]byte, LinkControlBytes),
	}
	cases = append(cases, alias...)

	for _, lc := range cases {
		frags, err := EncodeEmbeddedLC(lc)
		if err != nil {
			t.Errorf("%x: %v", lc, err)
			continue
		}
		got, ok := DecodeEmbeddedLC(frags)
		if !ok {
			t.Errorf("%x did not decode", lc)
			continue
		}
		if string(got) != string(lc) {
			t.Errorf("%x came back as %x", lc, got)
		}
	}

	// A whole alias through the carriage and back out as text, which is the
	// journey a Zello user's callsign would make.
	var reassembled [][]byte
	for _, pdu := range alias {
		frags, err := EncodeEmbeddedLC(pdu)
		if err != nil {
			t.Fatalf("encoding an alias PDU: %v", err)
		}
		back, ok := DecodeEmbeddedLC(frags)
		if !ok {
			t.Fatal("an alias PDU did not decode")
		}
		reassembled = append(reassembled, back)
	}
	if got, ok := TalkerAliasFrom(reassembled); !ok || got != "K9MLS ZELLO" {
		t.Errorf("the alias came back as %q (ok=%v)", got, ok)
	}
}

// TestACorruptedFragmentNeverYieldsADifferentLinkControl records the
// deliberate absence of error correction, and states the property that
// actually matters.
//
// The Hamming rows and the column parity could locate and fix a single bit.
// QSP reads these from its own output and from captures, not off the air, so a
// corrector would be a corrector nothing exercises — and one nobody can trust
// is worse than none. The frames QSP relays keep their own FEC untouched,
// which is ADR-0034's reasoning applied a layer down.
//
// **The first version of this test was wrong and the code was right.** It
// required every single-bit flip to be refused, and four of the thirty-two
// bits in each burst are column parity, which carry no link control at all —
// flipping one leaves the LC unchanged and the checksum still correct, so it
// decoded to the same value and the test called that a silent correction. It
// was not: nothing was corrected and nothing was wrong.
//
// So the property is not "every corruption is refused". It is **never return a
// link control that differs from the one that was encoded** — either the
// original, or a refusal, and nothing in between.
func TestACorruptedFragmentNeverYieldsADifferentLinkControl(t *testing.T) {
	want := LinkControlFor(3148, 3132911, false)
	frags, err := EncodeEmbeddedLC(want)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	refused, unchanged := 0, 0
	for burst := 0; burst < EmbeddedLCBursts; burst++ {
		for i := 0; i < EmbeddedLCFragmentBits; i++ {
			bad := frags
			bad[burst] ^= 1 << uint(i)

			got, ok := DecodeEmbeddedLC(bad)
			switch {
			case !ok:
				refused++
			case string(got) == string(want):
				// A bit that carried no link control. Harmless.
				unchanged++
			default:
				t.Fatalf("flipping bit %d of burst %d produced a different link "+
					"control: %x rather than %x or a refusal",
					i, burst+1, got, want)
			}
		}
	}

	// **The split of the 128 bits is figure B.3's matrix showing through**, and
	// it is a better structural check than any single number:
	//
	//   - 77 information bits — the 72 of the link control plus the five-bit
	//     checksum — so flipping one changes what is carried and is refused
	//   - 51 parity bits — five Hamming bits on each of seven rows, plus
	//     sixteen column parity bits — so flipping one leaves the link control
	//     intact and harmless
	//
	// 77 and 51 add to 128, which is eight rows of sixteen. An earlier version
	// of this test expected 16 unchanged, having counted the column parity and
	// forgotten the Hamming rows; the code was right and the arithmetic in the
	// test was not.
	const (
		information = 77
		parity      = 51
		total       = EmbeddedLCBursts * EmbeddedLCFragmentBits
	)
	if information+parity != total {
		t.Fatalf("%d information and %d parity bits do not fill %d",
			information, parity, total)
	}
	if refused+unchanged != total {
		t.Fatalf("%d flips accounted for, want %d", refused+unchanged, total)
	}
	if unchanged != parity {
		t.Errorf("%d flips left the link control intact, want %d — five Hamming "+
			"bits on each of seven rows plus sixteen column parity bits",
			unchanged, parity)
	}
	if refused != information {
		t.Errorf("%d flips were refused, want %d — the 72 link control bits plus "+
			"the five checksum bits", refused, information)
	}
}

// TestTheMiddlesAreTheFourTheSuperframeWants ties the carriage to the burst
// assembly that already existed.
//
// Positions 1 to 4 are bursts B to E. Burst A carries the synchronisation
// pattern and burst F the Null message, so the ends are left alone — and the
// LCSS in each EMB says where the fragment sits, which is what lets a
// receiver reassemble.
func TestTheMiddlesAreTheFourTheSuperframeWants(t *testing.T) {
	const colourCode = 11 // the operator's XPR8300

	middles, err := EmbeddedLCMiddles(LinkControlFor(3148, 3132911, false), colourCode)
	if err != nil {
		t.Fatalf("building middles: %v", err)
	}

	wantLCSS := []uint8{LCSSFirst, LCSSContinuation, LCSSContinuation, LCSSLast}
	for i, middle := range middles {
		emb, fragment := SplitMiddle(middle)
		if !ValidEMB(emb) {
			t.Errorf("burst %c carries an EMB that does not verify", 'B'+rune(i))
		}
		if got := ColourCodeOf(emb); got != colourCode {
			t.Errorf("burst %c carries colour code %d, want %d", 'B'+rune(i), got, colourCode)
		}
		if got := LCSSOf(emb); got != wantLCSS[i] {
			t.Errorf("burst %c carries LCSS %d, want %d", 'B'+rune(i), got, wantLCSS[i])
		}
		_ = fragment
	}

	// And the fragments in those middles are the ones the encoder produced,
	// so nothing was lost between the two calls.
	frags, err := EncodeEmbeddedLC(LinkControlFor(3148, 3132911, false))
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	var back [EmbeddedLCBursts]uint32
	for i, middle := range middles {
		_, back[i] = SplitMiddle(middle)
	}
	if back != frags {
		t.Errorf("the middles carry %v and the encoder produced %v", back, frags)
	}

	if _, err := EmbeddedLCMiddles(make([]byte, 8), colourCode); err == nil {
		t.Error("an eight-octet link control was accepted")
	}
	if _, err := EmbeddedLCMiddles(LinkControlFor(1, 2, false), 16); err == nil {
		t.Error("colour code 16 was accepted; the field is four bits")
	} else if !strings.Contains(err.Error(), "burst") {
		t.Errorf("the failure does not say which burst: %v", err)
	}
}

// TestEveryRowsHammingParityCoversAllElevenInformationColumns closes a hole
// the break suite found.
//
// **Computing the parity over ten columns instead of eleven passed every
// other test in this file.** The checksum bit sits in column 10 of rows 3 to
// 7, inside the Hamming code's reach per figure B.3 — but this package does
// not correct errors, so a decode reads the checksum straight out of column 10
// and never consults the parity. The round trip was therefore blind to the
// checksum bit being left unprotected, and a receiver that *does* correct
// would have computed a different syndrome on every frame.
//
// So this verifies the matrix the way a receiver would: every row's parity
// recomputed over its eleven information columns.
func TestEveryRowsHammingParityCoversAllElevenInformationColumns(t *testing.T) {
	for _, lc := range [][]byte{
		LinkControlFor(3148, 3132911, false),
		LinkControlFor(3132910, 999999, true),
		// An LC whose checksum has every bit set that can be, so that a
		// parity not covering column 10 shows up rather than happening to
		// agree. 30 is the largest value the modulus yields.
		{30, 0, 0, 0, 0, 0, 0, 0, 0},
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
	} {
		frags, err := EncodeEmbeddedLC(lc)
		if err != nil {
			t.Fatalf("%x: %v", lc, err)
		}

		// Undo the interleave into the 8 by 16 matrix.
		seq := make([]byte, 0, 128)
		for _, v := range frags {
			for i := EmbeddedLCFragmentBits - 1; i >= 0; i-- {
				seq = append(seq, byte(v>>uint(i))&1)
			}
		}
		var m [8][16]byte
		for i, bit := range seq {
			m[i%8][i/8] = bit
		}

		for row := 0; row < 7; row++ {
			want := hammingParity16(m[row][:11])
			var got [5]byte
			copy(got[:], m[row][11:])
			if got != want {
				t.Errorf("%x row %d carries parity %v and its eleven "+
					"information columns give %v; the checksum bit in column 10 "+
					"is inside the code", lc, row+1, got, want)
			}
		}
	}
}

// TestTheChecksumSitsMostSignificantBitFirst, per figure B.3: CS(4) is in row
// 3 and CS(0) in row 7.
//
// A reversed checksum survives a round trip against this package's own decoder
// — both ends would agree — and fails against every radio. So it is checked
// against the position figure B.3 prints rather than against itself.
func TestTheChecksumSitsMostSignificantBitFirst(t *testing.T) {
	// A checksum of 16 is 10000 in binary, so CS(4) is the only bit set and
	// it must appear in row 3 and nowhere else.
	lc := []byte{16, 0, 0, 0, 0, 0, 0, 0, 0}
	cs, err := EmbeddedLCChecksum(lc)
	if err != nil {
		t.Fatalf("checksum: %v", err)
	}
	if cs != 16 {
		t.Fatalf("the checksum of %x is %d, want 16", lc, cs)
	}

	frags, err := EncodeEmbeddedLC(lc)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	seq := make([]byte, 0, 128)
	for _, v := range frags {
		for i := EmbeddedLCFragmentBits - 1; i >= 0; i-- {
			seq = append(seq, byte(v>>uint(i))&1)
		}
	}
	var m [8][16]byte
	for i, bit := range seq {
		m[i%8][i/8] = bit
	}

	for row := 2; row < 7; row++ {
		want := byte(0)
		if row == 2 {
			want = 1 // CS(4), in row 3
		}
		if got := m[row][10]; got != want {
			t.Errorf("row %d column 10 carries %d, want %d — CS(4) is in row 3 "+
				"and CS(0) in row 7", row+1, got, want)
		}
	}
}
