package ipscbridge_test

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// slotFixture holds transmissions on both timeslots. It was captured for the
// slot bit and is the only fixture with traffic on more than one.
const slotFixture = "../../testdata/ipsc/ipsc-slot-tg.pcap"

func slotMessages(tb testing.TB) []ipsc.Message {
	tb.Helper()
	raw, err := os.ReadFile(slotFixture)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	lt := binary.LittleEndian.Uint32(raw[20:24])
	var out []ipsc.Message
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		var ip []byte
		switch lt {
		case 1:
			if len(rec) < 34 || binary.BigEndian.Uint16(rec[12:14]) != 0x0800 {
				continue
			}
			ip = rec[14:]
		case 276:
			if len(rec) < 20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
				continue
			}
			ip = rec[20:]
		default:
			continue
		}
		if len(ip) < 20 || ip[9] != 17 {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 9 || udp[8] != byte(ipsc.KindVoice) {
			continue
		}
		if m, err := ipsc.Parse(udp[8:]); err == nil {
			out = append(out, m)
		}
	}
	if len(out) < 200 {
		tb.Fatalf("only %d voice messages read; the fixture reader is broken", len(out))
	}
	return out
}

// TestAudioCrossesOnBothTimeslots is the defect that hid for the life of the
// IPSC listener.
//
// **The frame marker carries the timeslot in its high bit.** It was recorded as
// 0x8a, from captures that were all on one slot, so every voice frame on the
// other slot was refused by Payload and the converter emitted nothing for it.
//
// A whole timeslot of audio never crossed the bridge. It went unnoticed because
// this network carries its traffic on TG 2 timeslot 2, and it was found by an
// operator keying up on timeslot 1 and reading the journal: the IPSC listener
// logged "call started" and the DMR side logged nothing, because AsVoice reads
// the flags and Payload reads the marker.
func TestAudioCrossesOnBothTimeslots(t *testing.T) {
	msgs := slotMessages(t)

	var set, clear int
	for _, m := range msgs {
		if bit, ok := m.SlotBit(); ok && bit {
			set++
		} else {
			clear++
		}
	}
	if set == 0 || clear == 0 {
		t.Fatalf("the fixture should hold both slot bits; set=%d clear=%d", set, clear)
	}

	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11, SlotBitIsTimeslot2: true})
	if err != nil {
		t.Fatalf("%v", err)
	}

	produced := map[bool]int{}
	for _, m := range msgs {
		bit, _ := m.SlotBit()
		produced[bit] += len(c.Convert(m, hbp.RepeaterID(315544)))
	}

	for _, bit := range []bool{true, false} {
		if produced[bit] == 0 {
			t.Errorf("slot bit %v produced no bursts at all; one whole timeslot "+
				"of audio does not cross the bridge", bit)
		}
	}
}

// TestTheFrameMarkerCarriesTheTimeslot pins the reading the fix rests on.
//
// On a voice frame the high bit of the marker equals the slot bit in byte 17 —
// all 528 captured voice frames across four captures and two repeater models.
// On a header or terminator it is never set, which is why those were unaffected
// and the fault presented as a talkgroup problem rather than a timeslot one.
func TestTheFrameMarkerCarriesTheTimeslot(t *testing.T) {
	var voice, signalling int
	for _, m := range slotMessages(t) {
		raw := m.Marshal()
		marker := raw[30]
		bit, ok := m.SlotBit()
		if !ok {
			continue
		}
		switch ipsc.FrameKindOf(marker) {
		case ipsc.FrameVoice:
			voice++
			if (marker&ipsc.FrameSlotBit != 0) != bit {
				t.Fatalf("a voice frame's marker %#02x disagrees with the slot "+
					"bit %v in byte 17", marker, bit)
			}
		case ipsc.FrameHeader, ipsc.FrameTerminator:
			signalling++
			if marker&ipsc.FrameSlotBit != 0 {
				t.Fatalf("a header or terminator carries the slot bit in its "+
					"marker (%#02x); no captured one does", marker)
			}
		}
	}
	if voice == 0 || signalling == 0 {
		t.Fatalf("checked %d voice and %d signalling frames; wanted both", voice, signalling)
	}
}
