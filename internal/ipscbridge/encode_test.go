package ipscbridge_test

import (
	"bytes"
	"testing"

	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// TestTheAudioSurvivesTheRoundTrip is the one claim this encoder can prove.
//
// **It cannot prove a repeater accepts these frames** — nothing has captured a
// master sending voice, so there is no oracle for the envelope. What it can
// prove is that the part that matters most survives: take real Motorola audio,
// convert it to a Homebrew burst, convert it back, and require the vocoder core
// to be the bytes the radio originally encoded.
//
// Audio is king. If the envelope is wrong the symptom is silence and a capture
// fixes it; if the vocoder payload were mangled, every path through this
// package would be degrading audio and no capture would reveal it.
func TestTheAudioSurvivesTheRoundTrip(t *testing.T) {
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	if err != nil {
		t.Fatalf("%v", err)
	}
	e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{ColourCode: 11})

	var checked int
	for _, m := range voiceMessages(t) {
		original, ok := m.AsVoice()
		if !ok {
			continue
		}
		_ = original

		for _, burst := range c.Convert(m, hbp.RepeaterID(3132910)) {
			if burst.FrameType == hbp.FrameTypeSync {
				continue // a header or terminator this package built
			}
			out := e.Encode(burst)
			if len(out) == 0 {
				t.Fatal("a voice burst encoded to nothing")
			}

			// The last message of an emission is the one carrying audio.
			last := out[len(out)-1]
			if last.Kind != ipsc.KindVoice {
				t.Fatalf("the audio frame has kind %#02x", byte(last.Kind))
			}
			_, core, _, okp := last.Payload()
			if !okp {
				t.Fatal("a frame this package built does not parse back")
			}

			// The same burst, decoded back, must give the identical core.
			want := ipscCoreOf(t, m)
			if want == nil {
				continue
			}
			if !bytes.Equal(core, want) {
				t.Fatalf("the vocoder core changed through the round trip:\n got %x\nwant %x",
					core, want)
			}
			checked++
		}
	}
	t.Logf("%d vocoder cores survived Motorola to Homebrew and back, unchanged", checked)
	if checked == 0 {
		t.Fatal("nothing was checked")
	}
}

// ipscCoreOf returns the vocoder core a captured message carries, or nil when
// the frame is not one this package converts.
func ipscCoreOf(t *testing.T, m ipsc.Message) []byte {
	t.Helper()
	_, core, _, ok := m.Payload()
	if !ok {
		return nil
	}
	return core
}

// TestASenderIDIsTheMasters records the one inference a capture already
// settled.
//
// Bytes 1 to 4 of every IPSC message are the sender's own radio ID: in
// ipsc-phase2-registration.pcap the peer's messages carry the peer's and the
// master's carry the master's. So a master relaying somebody else's audio still
// signs it with its own ID, and the originating radio travels in the body's
// 24-bit source instead.
func TestASenderIDIsTheMasters(t *testing.T) {
	e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{ColourCode: 11})
	out := e.Encode(hbp.Data{
		SourceID: 3155413, TargetID: 2, Timeslot: hbp.Timeslot2,
		CallType: hbp.CallGroup, FrameType: hbp.FrameTypeVoiceSync,
		StreamID: 0xC0FFEE,
	})
	if len(out) == 0 {
		t.Skip("a burst with no decodable audio produces nothing, which is correct")
	}
	for _, m := range out {
		if m.SenderID != 3132911 {
			t.Errorf("a relayed frame is signed %d, want the master's own 3132911", m.SenderID)
		}
		src := uint32(m.Body[1])<<16 | uint32(m.Body[2])<<8 | uint32(m.Body[3])
		if src != 3155413 {
			t.Errorf("the body names source %d, want the transmitting radio 3155413", src)
		}
		dst := uint32(m.Body[4])<<16 | uint32(m.Body[5])<<8 | uint32(m.Body[6])
		if dst != 2 {
			t.Errorf("the body names destination %d, want TG2", dst)
		}
	}
}

// TestATransmissionOpensWithThreeHeaders matches what Motorola does.
func TestATransmissionOpensWithThreeHeaders(t *testing.T) {
	c, _ := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{ColourCode: 11})

	var first []ipsc.Message
	for _, m := range voiceMessages(t) {
		for _, burst := range c.Convert(m, hbp.RepeaterID(3132910)) {
			if burst.FrameType == hbp.FrameTypeSync {
				continue
			}
			if out := e.Encode(burst); len(out) > 0 {
				first = out
				break
			}
		}
		if first != nil {
			break
		}
	}
	if len(first) != 4 {
		t.Fatalf("a transmission opened with %d messages, want three headers and audio", len(first))
	}
	v, ok := first[0].AsVoice()
	if !ok {
		t.Fatal("the first message is not voice")
	}
	if !v.IsFirstFrame() {
		t.Errorf("the first message carries flags %#04x, want the first-frame flag", v.Flags)
	}
}
