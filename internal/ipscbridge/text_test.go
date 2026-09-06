package ipscbridge_test

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// textFixture is the only capture of text over IP Site Connect.
const textFixture = "../../testdata/ipsc/ipsc-text.pcap"

func textMessages(tb testing.TB) []ipsc.Message {
	tb.Helper()
	raw, err := os.ReadFile(textFixture)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	var out []ipsc.Message
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
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 9 || !ipsc.Kind(udp[8]).IsText() {
			continue
		}
		m, err := ipsc.Parse(udp[8:])
		if err != nil {
			tb.Fatalf("a text burst did not parse: %v", err)
		}
		out = append(out, m)
	}
	if len(out) < 100 {
		tb.Fatalf("only %d text messages read; the fixture reader is broken", len(out))
	}
	return out
}

// TestATextBurstSurvivesTheRoundTrip is the assertion that matters.
//
// The converter builds a 33-byte DMR data burst from a twelve-octet block.
// **Decoding that burst must give back the same twelve octets**, or a text has
// been quietly corrupted — and a corrupt text is worse than a dropped one,
// because it arrives.
//
// This is the same standard the voice path is held to: not "a burst was
// produced" but "the bytes a radio will read are the bytes that arrived".
func TestATextBurstSurvivesTheRoundTrip(t *testing.T) {
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 4, SlotBitIsTimeslot2: true})
	if err != nil {
		t.Fatalf("%v", err)
	}

	var checked int
	for _, m := range textMessages(t) {
		txt, ok := m.AsText()
		if !ok || len(txt.Block) != dmrfec.LinkControlBlockBytes {
			continue
		}
		out, ok := c.ConvertText(m, hbp.RepeaterID(999999))
		if !ok {
			t.Fatalf("a %d-octet block was refused; it is the size a burst carries",
				len(txt.Block))
		}

		payload, _, ok := dmrfec.DecodeBPTC(out.Payload[:])
		if !ok {
			t.Fatal("the burst this converter built does not decode")
		}
		got := dmrfec.BurstBytesFrom(payload)[:dmrfec.LinkControlBlockBytes]
		for i := range txt.Block {
			if got[i] != txt.Block[i] {
				t.Fatalf("octet %d came back %#02x, went in %#02x\n in:  % x\nout: % x",
					i, got[i], txt.Block[i], txt.Block, got[:len(txt.Block)])
			}
		}

		// The Slot Type inside the burst must say what the frame says, or a
		// radio and the network would disagree about which burst this is.
		cc, dt, ok := dmrfec.SlotTypeOf(out.Payload[:])
		if !ok {
			t.Fatal("the burst carries no readable Slot Type")
		}
		if dt != txt.DataType {
			t.Fatalf("the burst says data type %#x and the frame says %#x", dt, txt.DataType)
		}
		if cc != 4 {
			t.Fatalf("the burst says colour code %d, want the converter's 4", cc)
		}
		if out.DataType != txt.DataType {
			t.Fatalf("the Homebrew frame says data type %#x and the burst says %#x",
				out.DataType, txt.DataType)
		}
		checked++
	}
	if checked < 100 {
		t.Fatalf("only %d bursts round-tripped; the fixture held more", checked)
	}
}

// TestAPrivateTextStaysPrivate keeps the call type across the bridge.
//
// Getting this wrong delivers a private message to a talkgroup, which is the
// worst failure available here: it works, it is silent, and everyone sees it.
func TestAPrivateTextStaysPrivate(t *testing.T) {
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 4, SlotBitIsTimeslot2: true})
	if err != nil {
		t.Fatalf("%v", err)
	}

	var group, private int
	for _, m := range textMessages(t) {
		out, ok := c.ConvertText(m, hbp.RepeaterID(999999))
		if !ok {
			continue
		}
		switch m.Kind {
		case ipsc.KindTextGroup:
			group++
			if out.CallType != hbp.CallGroup {
				t.Fatal("a group text became a private call")
			}
		case ipsc.KindTextPrivate:
			private++
			if out.CallType != hbp.CallPrivate {
				t.Fatal("a private text became a group call")
			}
		}
	}
	if group == 0 || private == 0 {
		t.Fatalf("the fixture should hold both; group=%d private=%d", group, private)
	}
}

// TestARateThreeQuarterBurstIsRefusedRatherThanTruncated guards the case that
// would corrupt a message while appearing to work.
//
// Those bursts carry twenty-two octets and a data burst holds twelve. Placing
// the first twelve would deliver a text with a hole in it, which a radio would
// display as text — **half a message delivered is worse than none, because it
// looks like it worked.**
func TestARateThreeQuarterBurstIsRefusedRatherThanTruncated(t *testing.T) {
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 4})
	if err != nil {
		t.Fatalf("%v", err)
	}

	var seen int
	for _, m := range textMessages(t) {
		txt, ok := m.AsText()
		if !ok || len(txt.Block) == dmrfec.LinkControlBlockBytes {
			continue
		}
		seen++
		if _, ok := c.ConvertText(m, hbp.RepeaterID(999999)); ok {
			t.Fatalf("a %d-octet block was converted; it does not fit a burst",
				len(txt.Block))
		}
	}
	if seen == 0 {
		t.Fatal("the fixture should hold Rate 3/4 bursts")
	}
}

// TestTheSlotPolarityIsConfiguration checks the setting that has no counterpart
// anywhere else and so is easy to hardcode by accident.
//
// It caught a live defect on the voice path in 0195, where the encoder assumed
// one polarity while the converter read the operator's.
func TestTextTakesTheConfiguredSlotPolarity(t *testing.T) {
	msgs := textMessages(t)

	for _, tc := range []struct {
		name string
		flag bool
		want hbp.Timeslot
	}{
		{"slot bit means timeslot 2", true, hbp.Timeslot2},
		{"slot bit means timeslot 1", false, hbp.Timeslot1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 4, SlotBitIsTimeslot2: tc.flag})
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, m := range msgs {
				txt, ok := m.AsText()
				if !ok || !txt.SlotSet {
					continue
				}
				out, ok := c.ConvertText(m, hbp.RepeaterID(999999))
				if !ok {
					continue
				}
				if out.Timeslot != tc.want {
					t.Fatalf("a set slot bit gave %v, want %v", out.Timeslot, tc.want)
				}
				return
			}
			t.Fatal("no burst in the fixture had the slot bit set")
		})
	}
}

// TestATextGoesOutAsItCameIn is the whole bridge for one text burst.
//
// A real burst from a repeater is converted to Homebrew and encoded back to IP
// Site Connect, and **the frame that comes out must be the frame that went in**
// wherever this project understands the bytes: the same twelve-octet block, the
// same DMR data type, the same call type, the same Slot Type.
//
// This is the assertion the voice path earned in 0195 and did not have before
// it — that the output resembles what a repeater sends, rather than merely
// being produced.
func TestATextGoesOutAsItCameIn(t *testing.T) {
	const colourCode = 4
	cfg := ipscbridge.Config{ColourCode: colourCode, SlotBitIsTimeslot2: true}
	c, err := ipscbridge.New(cfg)
	if err != nil {
		t.Fatalf("%v", err)
	}
	e := ipscbridge.NewEncoder(31329, cfg)

	var checked int
	for _, m := range textMessages(t) {
		in, ok := m.AsText()
		if !ok || len(in.Block) != dmrfec.LinkControlBlockBytes {
			continue
		}
		burst, ok := c.ConvertText(m, hbp.RepeaterID(999999))
		if !ok {
			t.Fatal("a twelve-octet block was refused by the converter")
		}

		msgs, _ := e.Encode(burst)
		if len(msgs) != 1 {
			t.Fatalf("encoding a text produced %d messages, want exactly one; "+
				"a data burst has no headers and no superframe", len(msgs))
		}
		out := msgs[0]

		if out.Kind.IsText() != true {
			t.Fatalf("a text encoded as kind %#02x", byte(out.Kind))
		}
		wantKind := ipsc.KindTextGroup
		if in.Private {
			wantKind = ipsc.KindTextPrivate
		}
		if out.Kind != wantKind {
			t.Fatalf("a %s text encoded as kind %#02x",
				map[bool]string{true: "private", false: "group"}[in.Private], byte(out.Kind))
		}

		back, ok := out.AsText()
		if !ok {
			t.Fatal("the frame this encoder built does not decode as a text")
		}
		if back.DataType != in.DataType {
			t.Fatalf("data type went in %#x and came out %#x", in.DataType, back.DataType)
		}
		if back.ColourCode != colourCode {
			t.Fatalf("colour code came out %d, want the encoder's %d",
				back.ColourCode, colourCode)
		}
		for i := range in.Block {
			if back.Block[i] != in.Block[i] {
				t.Fatalf("octet %d went in %#02x and came out %#02x\n in:  % x\nout: % x",
					i, in.Block[i], back.Block[i], in.Block, back.Block)
			}
		}

		raw := out.Marshal()
		if len(raw) != 54 {
			t.Fatalf("a text frame is %d bytes; every captured one was 54", len(raw))
		}
		// Byte 12 separates data from voice, and it is the one byte the
		// preamble writes wrongly for this caller.
		if raw[12] != 0x01 {
			t.Fatalf("byte 12 is %#02x; a data burst reads 0x01 where voice reads 0x02",
				raw[12])
		}
		if raw[50] != 0 {
			t.Fatalf("byte 50 is %#02x, want zero as in a voice header", raw[50])
		}
		checked++
	}
	if checked < 100 {
		t.Fatalf("only %d bursts round-tripped through the bridge", checked)
	}
}

// TestATextStreamIsNumberedFromZero matches what a hotspot does with its own
// transmissions.
//
// Every stream a real MMDVM hotspot sends starts its sequence at 0 and counts
// up — checked across three streams in hbp-voice-live.pcap. The voice path has
// always done the same, resetting when the stream ID changes.
//
// **Text did not.** It used the raw IPSC sequence, a free-running counter
// shared by every transmission on the link, and a capture of KD9EJA's text
// crossing the bridge showed one stream starting at 69 and the next at 67.
// Whether MMDVM refuses on that is unproven; it is wrong on its own terms
// either way, and it was the one place text differed from the voice path that
// works.
func TestATextStreamIsNumberedFromZero(t *testing.T) {
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 4, SlotBitIsTimeslot2: true})
	if err != nil {
		t.Fatalf("%v", err)
	}

	seqByStream := map[hbp.StreamID][]uint8{}
	order := []hbp.StreamID{}
	for _, m := range textMessages(t) {
		out, ok := c.ConvertText(m, hbp.RepeaterID(999999))
		if !ok {
			continue
		}
		if _, seen := seqByStream[out.StreamID]; !seen {
			order = append(order, out.StreamID)
		}
		seqByStream[out.StreamID] = append(seqByStream[out.StreamID], out.Sequence)
	}
	if len(order) < 2 {
		t.Fatalf("the fixture yielded %d streams, want at least 2 so that a "+
			"second one can be checked for starting over", len(order))
	}

	for _, stream := range order {
		seqs := seqByStream[stream]
		if seqs[0] != 0 {
			t.Errorf("stream %#08x starts at %d; a hotspot's own streams all "+
				"start at 0", uint32(stream), seqs[0])
		}
		for i := 1; i < len(seqs); i++ {
			if seqs[i] != seqs[i-1]+1 {
				t.Errorf("stream %#08x jumped from %d to %d; the sequence "+
					"counts the bursts of one transmission",
					uint32(stream), seqs[i-1], seqs[i])
				break
			}
		}
	}
}
