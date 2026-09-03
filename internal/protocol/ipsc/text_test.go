package ipsc_test

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// textCapture holds the first and only capture of text over IP Site Connect.
const textCapture = "../../../testdata/ipsc/ipsc-text.pcap"

// textBursts returns every text datagram the repeater sent, in order.
func textBursts(tb testing.TB) [][]byte {
	tb.Helper()
	raw, err := os.ReadFile(textCapture)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	if len(raw) < 24 || binary.LittleEndian.Uint32(raw[20:24]) != 276 {
		tb.Fatal("this fixture should be a Linux cooked v2 capture")
	}
	var out [][]byte
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		// Linux cooked v2: twenty bytes, then the network layer.
		if len(rec) < 20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[20:]
		if len(ip) < 20 || ip[9] != 17 {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 9 {
			continue
		}
		if k := ipsc.Kind(udp[8]); k.IsText() {
			out = append(out, udp[8:])
		}
	}
	if len(out) < 100 {
		tb.Fatalf("only %d text bursts read; the fixture reader is broken", len(out))
	}
	return out
}

// TestTheTwoTextKindsAreTheCallType is the finding the whole decoder rests on.
//
// **0x83 and 0x84 differ only in who the text is for.** The destination in the
// envelope and the destination inside the payload agree, and both read a
// talkgroup for one kind and a radio ID for the other. Getting this backwards
// would deliver every private message to a talkgroup.
func TestTheTwoTextKindsAreTheCallType(t *testing.T) {
	var sawGroup, sawPrivate bool
	for _, raw := range textBursts(t) {
		m, err := ipsc.Parse(raw)
		if err != nil {
			t.Fatalf("a text burst did not parse: %v", err)
		}
		txt, ok := m.AsText()
		if !ok {
			continue // the 34-byte frame, whose marker is not a data type
		}
		// **On a CSBK burst only**, the destination inside the payload agrees
		// with the envelope's. The first draft of this test checked every
		// burst, having read the offsets off a single CSBK and assumed they
		// held everywhere; a Data Header and a Rate 1/2 block lay their
		// twelve octets out differently and the test failed on the first one
		// it met. That is the failure this project keeps finding, caught here
		// by the test rather than on air.
		const csbk = 0x3
		if txt.DataType == csbk && len(raw) > 45 {
			inner := uint32(raw[42])<<16 | uint32(raw[43])<<8 | uint32(raw[44])
			if inner != txt.Destination {
				t.Fatalf("kind %#02x CSBK: envelope destination %d, payload says %d",
					byte(m.Kind), txt.Destination, inner)
			}
		}
		switch m.Kind {
		case ipsc.KindTextGroup:
			sawGroup = true
			if txt.Private {
				t.Error("a group text decoded as private")
			}
		case ipsc.KindTextPrivate:
			sawPrivate = true
			if !txt.Private {
				t.Error("a private text decoded as a group text")
			}
		}
	}
	if !sawGroup || !sawPrivate {
		t.Fatalf("the fixture should hold both kinds; group=%v private=%v",
			sawGroup, sawPrivate)
	}
}

// TestATextBurstIsLaidOutLikeAVoiceHeader checks the structural claim that
// makes this cheap to build.
//
// Byte 50 is zero and byte 51 is the DMR Slot Type — colour code high, data
// type low — exactly as in a voice header. If that stopped being true, the
// twelve-octet block would not be where this package reads it and every text
// would carry the wrong bytes.
func TestATextBurstIsLaidOutLikeAVoiceHeader(t *testing.T) {
	var checked int
	for _, raw := range textBursts(t) {
		if len(raw) != 54 {
			continue // Rate 3/4 bursts are 60 and the odd one is 34
		}
		m, err := ipsc.Parse(raw)
		if err != nil {
			t.Fatalf("%v", err)
		}
		txt, ok := m.AsText()
		if !ok {
			continue
		}
		if raw[50] != 0 {
			t.Fatalf("byte 50 is %#02x, want zero as in a voice header", raw[50])
		}
		want := txt.ColourCode<<4 | txt.DataType
		if raw[ipsc.TextSlotTypeAt] != want {
			t.Fatalf("Slot Type is %#02x; colour code %d and data type %d make %#02x",
				raw[ipsc.TextSlotTypeAt], txt.ColourCode, txt.DataType, want)
		}
		if len(txt.Block) != ipsc.TextBlockLen {
			t.Fatalf("a 54-byte burst gave a %d-octet block, want %d",
				len(txt.Block), ipsc.TextBlockLen)
		}
		checked++
	}
	if checked < 100 {
		t.Fatalf("only %d bursts checked; the fixture or the reader is wrong", checked)
	}
}

// TestTheDataTypesAreTheOnesDMRDefines records what a text is made of.
//
// CSBK, Data Header, Rate 1/2 Data and Rate 3/4 Data are ETSI data types, not
// Motorola inventions. Seeing exactly those is what says a text is ordinary DMR
// data wrapped in the IPSC envelope, and it is why QSP can carry one without
// understanding the message inside.
func TestTheDataTypesAreTheOnesDMRDefines(t *testing.T) {
	const (
		csbk       = 0x3
		dataHeader = 0x6
		rateHalf   = 0x7
		rateThreeQ = 0x8
	)
	seen := map[uint8]int{}
	for _, raw := range textBursts(t) {
		m, err := ipsc.Parse(raw)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if txt, ok := m.AsText(); ok {
			seen[txt.DataType]++
		}
	}
	for _, want := range []uint8{csbk, dataHeader, rateHalf, rateThreeQ} {
		if seen[want] == 0 {
			t.Errorf("no burst carried DMR data type %#x; the fixture held one", want)
		}
		delete(seen, want)
	}
	for got, n := range seen {
		t.Errorf("unexpected data type %#x on %d bursts; only the four DMR data "+
			"types for messaging were captured", got, n)
	}
}

// TestARateThreeQuarterBurstCarriesMore keeps the two block lengths apart.
//
// Those bursts are 60 bytes where the rest are 54, and the extra is more of the
// same payload rather than a new field. Reading twelve octets from one would
// silently truncate a text.
func TestARateThreeQuarterBurstCarriesMore(t *testing.T) {
	var long, short int
	for _, raw := range textBursts(t) {
		m, err := ipsc.Parse(raw)
		if err != nil {
			t.Fatalf("%v", err)
		}
		txt, ok := m.AsText()
		if !ok {
			continue
		}
		switch len(raw) {
		case 60:
			long++
			if len(txt.Block) != ipsc.TextRate34Len {
				t.Fatalf("a 60-byte burst gave %d octets, want %d",
					len(txt.Block), ipsc.TextRate34Len)
			}
		case 54:
			short++
			if len(txt.Block) != ipsc.TextBlockLen {
				t.Fatalf("a 54-byte burst gave %d octets, want %d",
					len(txt.Block), ipsc.TextBlockLen)
			}
		}
	}
	if long == 0 || short == 0 {
		t.Fatalf("the fixture should hold both lengths; 60-byte=%d 54-byte=%d",
			long, short)
	}
}

// TestTheUnknownFrameIsRefusedRatherThanGuessedAt covers the 34-byte burst.
//
// Its marker is 0x13, which is not a DMR data type, and it appeared once at the
// end of a transmission. **Nothing in this project knows what it is**, so
// AsText refuses it rather than returning a Text whose fields would be invented.
func TestTheUnknownFrameIsRefusedRatherThanGuessedAt(t *testing.T) {
	var found bool
	for _, raw := range textBursts(t) {
		if len(raw) != 34 {
			continue
		}
		found = true
		m, err := ipsc.Parse(raw)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if _, ok := m.AsText(); ok {
			t.Error("the 34-byte frame decoded as a text burst; its marker is " +
				"not a DMR data type and its purpose is unknown")
		}
	}
	if !found {
		t.Fatal("the fixture should hold one 34-byte frame")
	}
}

// TestTheBlockIsReadFromTheRightOffset pins where the twelve octets begin.
//
// **Every other test here passed with the offset moved by one.** They check the
// block's length and its surroundings, and a block read one byte early is still
// twelve bytes long — it is simply the wrong twelve, and a text built from it
// would be corrupt in a way no length check can see.
//
// A CSBK burst makes the offset checkable: its fifth to seventh octets are the
// destination and its eighth to tenth the source, and both must equal what the
// envelope says. Nothing else in the block is understood, and nothing else
// needs to be.
func TestTheBlockIsReadFromTheRightOffset(t *testing.T) {
	const csbk = 0x3
	var checked int
	for _, raw := range textBursts(t) {
		m, err := ipsc.Parse(raw)
		if err != nil {
			t.Fatalf("%v", err)
		}
		txt, ok := m.AsText()
		if !ok || txt.DataType != csbk || len(txt.Block) < 10 {
			continue
		}
		dst := uint32(txt.Block[4])<<16 | uint32(txt.Block[5])<<8 | uint32(txt.Block[6])
		src := uint32(txt.Block[7])<<16 | uint32(txt.Block[8])<<8 | uint32(txt.Block[9])
		if dst != txt.Destination {
			t.Fatalf("the block says destination %d and the envelope says %d; "+
				"the block is being read from the wrong offset", dst, txt.Destination)
		}
		if src != txt.Source {
			t.Fatalf("the block says source %d and the envelope says %d; "+
				"the block is being read from the wrong offset", src, txt.Source)
		}
		checked++
	}
	if checked < 50 {
		t.Fatalf("only %d CSBK bursts checked; the fixture held more", checked)
	}
}
