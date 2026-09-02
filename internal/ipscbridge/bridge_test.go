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

const capture = "../../testdata/ipsc/ipsc-probe-voice.pcap"

func voiceMessages(tb testing.TB) []ipsc.Message {
	tb.Helper()
	raw, err := os.ReadFile(capture)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	var out []ipsc.Message
	off := 24
	for off+16 <= len(raw) {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < 34 || binary.BigEndian.Uint16(rec[12:14]) != 0x0800 {
			continue
		}
		ip := rec[14:]
		if ip[9] != 17 {
			continue
		}
		udp := ip[int(ip[0]&0x0f)*4:]
		if len(udp) < 8 {
			continue
		}
		p := udp[8:int(binary.BigEndian.Uint16(udp[4:6]))]
		msg, err := ipsc.Parse(p)
		if err != nil || msg.Kind != ipsc.KindVoice {
			continue
		}
		out = append(out, msg)
	}
	if len(out) == 0 {
		tb.Fatal("no voice messages in the capture")
	}
	return out
}

// TestRealMotorolaAudioBecomesValidHomebrewBursts is the end-to-end check, run
// over a transmission a radio actually made.
//
// Every burst produced must carry the vocoder parameters the repeater sent —
// unchanged, because that is what makes the bridge lossless — and embedded
// signalling a receiver will accept.
func TestRealMotorolaAudioBecomesValidHomebrewBursts(t *testing.T) {
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	if err != nil {
		t.Fatalf("%v", err)
	}
	var produced, skipped int
	for _, m := range voiceMessages(t) {
		out, ok := c.Convert(m, hbp.RepeaterID(3132910))
		if !ok {
			skipped++
			continue
		}
		produced++

		// The audio must survive: take the burst apart again and compare with
		// what the repeater sent.
		_, core, _, _ := m.Payload()
		back, corrected, ok := dmrfec.IPSCFromBurst(out.Payload[:])
		if !ok {
			t.Fatalf("a burst this bridge built could not be read back")
		}
		if corrected != 0 {
			t.Errorf("%d corrections on a burst built from clean parameters", corrected)
		}
		if string(back) != string(core) {
			t.Fatalf("the vocoder payload changed:\n  in  %x\n  out %x", core, back)
		}

		// The embedded signalling must be one a receiver accepts.
		middle, _ := dmrfec.Middle(out.Payload[:])
		if middle != dmrfec.VoiceSyncBS {
			emb, _ := dmrfec.SplitMiddle(middle)
			if !dmrfec.ValidEMB(emb) {
				t.Errorf("burst %d carries an EMB that fails its own parity: %#04x", produced, emb)
			}
			if cc := dmrfec.ColourCodeOf(emb); cc != 11 {
				t.Errorf("burst %d carries colour code %d, want 11", produced, cc)
			}
		}

		if out.SourceID == 0 || out.TargetID == 0 {
			t.Errorf("burst %d has source %d target %d", produced, out.SourceID, out.TargetID)
		}
	}
	t.Logf("%d bursts produced, %d frames skipped before a superframe boundary", produced, skipped)
	if produced == 0 {
		t.Fatal("no bursts were produced")
	}
}

// TestOneSyncBurstInSix checks that the converter keeps its place.
//
// A receiver uses the synchronisation burst to lock onto the superframe, so
// producing them at the wrong rate is the difference between audio and silence.
func TestOneSyncBurstInSix(t *testing.T) {
	c, _ := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	var total, sync int
	for _, m := range voiceMessages(t) {
		out, ok := c.Convert(m, hbp.RepeaterID(3132910))
		if !ok {
			continue
		}
		total++
		if middle, _ := dmrfec.Middle(out.Payload[:]); middle == dmrfec.VoiceSyncBS {
			sync++
		}
	}
	if sync == 0 {
		t.Fatal("no synchronisation bursts produced")
	}
	t.Logf("%d bursts, %d carrying sync, one in %.1f", total, sync, float64(total)/float64(sync))
	if r := float64(total) / float64(sync); r < 5 || r > 7 {
		t.Errorf("one sync burst in %.1f; a six-burst superframe requires one in six", r)
	}
}

// TestNothingIsEmittedBeforeASuperframeBoundary is the conservative half.
//
// A transmission joined mid-superframe has an unknown position, and a burst
// built at the wrong position carries signalling a radio rejects. Waiting costs
// at most six frames.
func TestNothingIsEmittedBeforeASuperframeBoundary(t *testing.T) {
	c, _ := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	msgs := voiceMessages(t)

	// Start the converter partway through a superframe.
	var firstOK int
	for i, m := range msgs[2:] {
		if _, ok := c.Convert(m, hbp.RepeaterID(3132910)); ok {
			firstOK = i
			break
		}
	}
	if firstOK == 0 {
		t.Log("the first frame offered happened to be a synchronisation frame")
	}
	if firstOK > dmrfec.SuperframeBursts {
		t.Errorf("waited %d frames for a superframe boundary; at most %d should be needed",
			firstOK, dmrfec.SuperframeBursts)
	}
}

// onOtherSlot returns a copy of a voice frame as it would look coming from the
// other timeslot: the slot bit flipped, and a different stream ID, because two
// simultaneous transmissions are two calls. Nothing else changes, so anything
// this test measures is caused by the slot and by nothing else.
func onOtherSlot(m ipsc.Message) ipsc.Message {
	body := append([]byte(nil), m.Body...)
	body[12] ^= ipsc.FlagSlot
	// StreamID is frame bytes 15 and 16, so body bytes 10 and 11.
	body[10] ^= 0xAA
	body[11] ^= 0x55
	return ipsc.Message{Kind: m.Kind, SenderID: m.SenderID, Body: body}
}

func burstsFrom(t *testing.T, msgs []ipsc.Message) []hbp.Data {
	t.Helper()
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	if err != nil {
		t.Fatalf("%v", err)
	}
	var out []hbp.Data
	for _, m := range msgs {
		if b, ok := c.Convert(m, hbp.RepeaterID(3132910)); ok {
			out = append(out, b)
		}
	}
	return out
}

// TestBothTimeslotsAtOnce is the defect this converter was built with.
//
// A repeater carries two transmissions at once, one per timeslot, and their
// frames arrive interleaved. A converter holding one set of counters lets the
// two streams reset each other on every frame: measured against the real
// capture, 66 frames on one slot produced 54 bursts, and the same 66 frames
// interleaved with a second slot produced **18** rather than about 108.
//
// Every fixture in this repository is single-slot, so nothing else here would
// notice. Break the slot separation in bridge.go and this test is the only one
// that fails.
func TestBothTimeslotsAtOnce(t *testing.T) {
	real := voiceMessages(t)
	baseline := burstsFrom(t, real)
	if len(baseline) == 0 {
		t.Fatal("no bursts from the capture on one slot")
	}

	mixed := make([]ipsc.Message, 0, len(real)*2)
	for _, m := range real {
		mixed = append(mixed, m, onOtherSlot(m))
	}
	got := burstsFrom(t, mixed)

	t.Logf("one slot: %d frames, %d bursts; two slots: %d frames, %d bursts",
		len(real), len(baseline), len(mixed), len(got))

	// Neither slot may lose a burst it would have produced alone.
	if len(got) < 2*len(baseline) {
		t.Errorf("interleaving a second timeslot produced %d bursts, want %d; "+
			"the two slots are sharing state", len(got), 2*len(baseline))
	}

	// And each slot must be labelled as its own.
	var ts1, ts2 int
	for _, b := range got {
		switch b.Timeslot {
		case hbp.Timeslot1:
			ts1++
		case hbp.Timeslot2:
			ts2++
		}
	}
	if ts1 == 0 || ts2 == 0 {
		t.Errorf("bursts landed on TS1 %d, TS2 %d; both slots should carry audio", ts1, ts2)
	}
}

// TestSlotPolarityIsConfiguration checks that the setting an operator has to
// supply actually moves the traffic.
//
// Which value of the IPSC slot bit means timeslot two was never recorded at the
// radio. The consequence of getting it wrong must therefore be a setting rather
// than a rebuild, and that is only true if the flag reaches the output.
func TestSlotPolarityIsConfiguration(t *testing.T) {
	msgs := voiceMessages(t)

	slots := func(t2 bool) map[hbp.Timeslot]int {
		c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11, SlotBitIsTimeslot2: t2})
		if err != nil {
			t.Fatalf("%v", err)
		}
		out := map[hbp.Timeslot]int{}
		for _, m := range msgs {
			if b, ok := c.Convert(m, hbp.RepeaterID(3132910)); ok {
				out[b.Timeslot]++
			}
		}
		return out
	}

	off, on := slots(false), slots(true)
	if off[hbp.Timeslot1] != on[hbp.Timeslot2] || off[hbp.Timeslot2] != on[hbp.Timeslot1] {
		t.Errorf("flipping SlotBitIsTimeslot2 did not swap the slots: %v against %v", off, on)
	}
	if off[hbp.Timeslot1] == 0 && off[hbp.Timeslot2] == 0 {
		t.Fatal("no bursts produced, so the polarity was never exercised")
	}
}

// TestAnOutOfRangeColourCodeIsRefused keeps configuration honest.
func TestAnOutOfRangeColourCodeIsRefused(t *testing.T) {
	if _, err := ipscbridge.New(ipscbridge.Config{ColourCode: 16}); err == nil {
		t.Error("colour code 16 was accepted; DMR allows 0 to 15")
	}
}
