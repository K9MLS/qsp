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
