package ipscbridge_test

import (
	"bytes"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// rate34Blocks returns the Rate 3/4 messages in the fixture, in capture order.
func rate34Blocks(tb testing.TB) []ipsc.Message {
	tb.Helper()
	var out []ipsc.Message
	for _, m := range rate34TextMessages(tb) {
		if txt, ok := m.AsText(); ok && len(txt.Block) == dmrfec.Rate34BlockBytes {
			out = append(out, m)
		}
	}
	if len(out) != 30 {
		tb.Fatalf("the fixture holds %d Rate 3/4 messages, want 30", len(out))
	}
	return out
}

// TestATextBlockSurvivesBothDirectionsOfTheBridge is the end-to-end assertion.
//
// A Rate 3/4 block arrives from a Motorola repeater, is converted to the
// Homebrew burst a hotspot receives, and is then encoded back into the IP Site
// Connect datagram another repeater receives. **The block a repeater at the far
// end reads must be the block the radio sent**, and the datagram carrying it
// must be the sixty bytes a real one is, because a repeater that gets
// fifty-four will read the Slot Type out of the middle of the message.
func TestATextBlockSurvivesBothDirectionsOfTheBridge(t *testing.T) {
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 4})
	if err != nil {
		t.Fatalf("%v", err)
	}

	for i, m := range rate34Blocks(t) {
		txt, _ := m.AsText()

		frame, ok := c.ConvertText(m, hbp.RepeaterID(999999))
		if !ok {
			t.Fatalf("block %d was not converted", i)
		}
		if !ipscbridge.IsRate34(frame) {
			t.Fatalf("block %d: the converted burst does not read as Rate 3/4", i)
		}

		// A fresh encoder per block: this asserts the layout, not the
		// sequencing, and a shared one would carry stream state between
		// unrelated transmissions.
		e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{ColourCode: 4})
		msgs, understood := e.Encode(frame)
		if !understood || len(msgs) != 1 {
			t.Fatalf("block %d: the encoder produced %d messages (understood=%v)",
				i, len(msgs), understood)
		}

		out := msgs[0].Marshal()
		if len(out) != ipsc.TextRate34Total {
			t.Fatalf("block %d: datagram is %d bytes, want %d",
				i, len(out), ipsc.TextRate34Total)
		}
		if out[ipsc.TextDataTypeAt] != dmrfec.DataTypeRate34 {
			t.Errorf("block %d: byte 30 reads %#x", i, out[ipsc.TextDataTypeAt])
		}
		slotTypeAt := ipsc.TextSlotTypeFor(len(out))
		if slotTypeAt != 57 {
			t.Fatalf("the Slot Type of a 60-byte datagram is at %d, want 57", slotTypeAt)
		}
		if got := out[slotTypeAt]; got != dmrfec.SlotTypeInfo(4, dmrfec.DataTypeRate34) {
			t.Errorf("block %d: Slot Type reads %#x, want %#x",
				i, got, dmrfec.SlotTypeInfo(4, dmrfec.DataTypeRate34))
		}
		if got := out[slotTypeAt-1]; got != 0 {
			t.Errorf("block %d: the octet before the Slot Type is %#x, want zero", i, got)
		}
		if got := [6]byte(out[32:38]); got != ipsc.HeaderConstantsRate34 {
			t.Errorf("block %d: constants read %x, want %x",
				i, got, ipsc.HeaderConstantsRate34)
		}

		back, ok := msgs[0].AsText()
		if !ok {
			t.Fatalf("block %d: the datagram QSP built did not read back as text", i)
		}
		if !bytes.Equal(back.Block, txt.Block) {
			t.Errorf("block %d: round trip gave %x, want %x", i, back.Block, txt.Block)
		}
		if _, verified := dmrfec.Rate34Serial(back.Block); !verified {
			t.Errorf("block %d: the CRC-9 no longer verifies after the round trip", i)
		}
	}
}

// TestTheConstantsAreNotTheTwelveOctetOnes exists because copying them would
// have compiled, passed vet and announced ninety-six bits of payload in a
// datagram carrying a hundred and forty-four.
func TestTheConstantsAreNotTheTwelveOctetOnes(t *testing.T) {
	if ipsc.HeaderConstantsRate34 == ipsc.HeaderConstants {
		t.Fatal("the two constant blocks are identical; one of them is wrong")
	}
	if got := ipsc.HeaderConstantsFor(ipsc.TextRate34Len); got != ipsc.HeaderConstantsRate34 {
		t.Errorf("an 18-octet block got %x", got)
	}
	if got := ipsc.HeaderConstantsFor(ipsc.TextBlockLen); got != ipsc.HeaderConstants {
		t.Errorf("a 12-octet block got %x", got)
	}
}

// TestTheOrderOfARate34BlockIsReportedRatherThanAssumed pins the diagnostic
// that stands in for the capture nobody has taken.
//
// A burst QSP built itself reads back as whatever Rate34AirOrder says, which
// proves only that the reporting works. What matters on air is that a burst
// from a hotspot reports whichever arrangement it actually used, and that a
// burst carrying no CRC at all — an unconfirmed block — says so instead of
// being guessed at.
func TestTheOrderOfARate34BlockIsReportedRatherThanAssumed(t *testing.T) {
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 4})
	if err != nil {
		t.Fatalf("%v", err)
	}
	m := rate34Blocks(t)[0]
	frame, ok := c.ConvertText(m, hbp.RepeaterID(999999))
	if !ok {
		t.Fatal("the fixture block was not converted")
	}
	if got := ipscbridge.Rate34OrderOf(frame); got != dmrfec.Rate34AirOrder {
		t.Errorf("a burst QSP built reports %s, want %s", got, dmrfec.Rate34AirOrder)
	}

	// A block with no valid CRC either way round. Reported as unknown, and
	// still carried: an unconfirmed Rate 3/4 block is eighteen octets of user
	// data with no control pair, and refusing those would drop a legal text.
	txt, _ := m.AsText()
	unconfirmed := bytes.Clone(txt.Block)
	unconfirmed[dmrfec.Rate34DataBytes+1] ^= 0xff
	burst, err := dmrfec.BuildRate34Burst(4, unconfirmed)
	if err != nil {
		t.Fatalf("%v", err)
	}
	block, order, ok := dmrfec.DecodeRate34Burst(burst)
	if !ok {
		t.Fatal("a block with no valid CRC was refused rather than carried")
	}
	if order != dmrfec.Rate34OrderUnknown {
		t.Errorf("a block with no valid CRC reports %s", order)
	}
	if !bytes.Equal(block, unconfirmed) {
		t.Errorf("carrying it changed it: %x, want %x", block, unconfirmed)
	}
}
