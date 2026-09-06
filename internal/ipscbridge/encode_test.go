package ipscbridge_test

import (
	"time"

	"bytes"
	"encoding/binary"
	"github.com/k9mls/qsp/internal/calls"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
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
			out, _ := e.Encode(burst)
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
	out, _ := e.Encode(hbp.Data{
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
			if out, _ := e.Encode(burst); len(out) > 0 {
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

// TestTwoOversFromOneRadioAreTwoStreams is a defect a capture found and no ear
// could have.
//
// streamFor packs a 16-bit IPSC stream into the top of a Homebrew stream ID and
// the sender's radio ID into the bottom. The encoder wrote the **low** half,
// which is the radio ID and nothing else, so every transmission a given radio
// ever made left here carrying one stream ID for ever. A capture on 2026-09-06
// caught two overs nineteen seconds apart, on different talkgroups and
// different timeslots, both relayed as 0x0cdee — the low half of 3132910.
//
// **A receiver tells one transmission from the next by this field.** QSP's own
// listener starts a new call when it changes and the converter does the same,
// so two consecutive overs from one radio arrive at a repeater with grounds to
// be read as one continuing transmission.
func TestTwoOversFromOneRadioAreTwoStreams(t *testing.T) {
	const source = 3132910

	// The shape streamFor produces: the IPSC stream on top, the radio ID
	// underneath. Two overs from one radio differ only in the top half, which
	// is exactly the case that used to collapse.
	relayed := func(ipscStream uint16) hbp.StreamID {
		return hbp.StreamID(uint32(ipscStream)<<16 | source&0xFFFF)
	}

	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	if err != nil {
		t.Fatalf("%v", err)
	}

	// One real burst, re-stamped with each transmission's stream, so the audio
	// is a capture's and only the field under test changes.
	var audio hbp.Data
	for _, m := range voiceMessages(t) {
		for _, burst := range c.Convert(m, hbp.RepeaterID(source)) {
			if burst.FrameType != hbp.FrameTypeSync {
				audio = burst
				break
			}
		}
		if audio.FrameType != 0 || audio.SourceID != 0 {
			break
		}
	}
	if audio.SourceID == 0 {
		t.Fatal("the fixture yielded no audio burst; this test would prove nothing")
	}

	seen := map[uint16]uint16{}
	for _, ipscStream := range []uint16{0x07db3, 0x079f5} {
		e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{ColourCode: 11})
		burst := audio
		burst.SourceID = source
		burst.StreamID = relayed(ipscStream)

		out, _ := e.Encode(burst)
		if len(out) == 0 {
			t.Fatal("a voice burst encoded to nothing")
		}
		last := out[len(out)-1]
		got := binary.BigEndian.Uint16(last.Body[10:12])

		if prev, ok := seen[got]; ok {
			t.Errorf("streams %#04x and %#04x both went out as %#04x, so a repeater "+
				"has grounds to hear two overs as one", prev, ipscStream, got)
		}
		seen[got] = ipscStream

		// **It carries the stream it arrived with.** A relayed transmission
		// that keeps its own identifier can be followed from one repeater to
		// the other in a single capture, which is how the defect above was
		// found in the first place.
		if got != ipscStream {
			t.Errorf("a transmission that arrived as %#04x was relayed as %#04x",
				ipscStream, got)
		}
	}
}

// TestAVoiceTransmissionGoesOutAsVoice is what three captures of real masters
// say and what this encoder did not.
//
// A voice Link Control header is carried in a data-sync burst, exactly like a
// text message, so dispatching on the frame type alone sent it to the text
// encoder — and it left as kind 0x83, the group text, carrying the voice
// transmission's own stream ID and flags. A capture on 2026-09-06 has two of
// them ahead of every outbound transmission.
//
// **No captured Motorola master sends 0x83 at all.** ipsc-master-voice,
// ipsc-probe-voice and ipsc-two-peers hold 288, 66 and 326 voice datagrams and
// every one is 0x80, marked in byte 30 as header, terminator or audio.
func TestAVoiceTransmissionGoesOutAsVoice(t *testing.T) {
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	if err != nil {
		t.Fatalf("%v", err)
	}
	e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{ColourCode: 11})

	kinds := map[ipsc.Kind]int{}
	markers := map[byte]int{}
	for _, m := range voiceMessages(t) {
		// Convert produces what a hotspot sends: a Link Control header and a
		// terminator in data-sync bursts, with the audio between them.
		for _, burst := range c.Convert(m, hbp.RepeaterID(3132910)) {
			msgs, _ := e.Encode(burst)
			for _, out := range msgs {
				kinds[out.Kind]++
				if len(out.Body) > 25 {
					// The slot bit shares byte 30 of the frame with the
					// marker, so audio reads 0x0a on one timeslot and 0x8a on
					// the other. Reading the raw byte is how a whole timeslot
					// of audio went missing for a fortnight.
					markers[ipsc.FrameKindOf(out.Body[25])]++
				}
			}
		}
	}

	if len(kinds) == 0 {
		t.Fatal("the transmission encoded to nothing; this test would prove nothing")
	}
	for kind, n := range kinds {
		if kind != ipsc.KindVoice {
			t.Errorf("%d datagrams of a voice transmission went out as %#02x, and no "+
				"captured master sends anything but %#02x for voice",
				n, byte(kind), byte(ipsc.KindVoice))
		}
	}

	// **And it is still a complete transmission.** Dropping the header burst
	// would be the wrong fix if the encoder did not build its own, so this
	// asserts the shape a master sends: a header, audio, a terminator.
	for marker, what := range map[byte]string{
		ipsc.FrameHeader:     "header",
		ipsc.FrameVoice:      "audio",
		ipsc.FrameTerminator: "terminator",
	} {
		if markers[marker] == 0 {
			t.Errorf("the transmission carries no %s", what)
		}
	}
}

// TestATextIsOneTransmission is the week-old defect a differential named.
//
// A text sent from a hotspot never arrived at a Motorola repeater, while a text
// from that repeater arrived perfectly. Two captures on 2026-09-06, the same
// two repeaters, opposite directions:
//
//	repeater sends   stream 6f08 throughout, flags 80dd then 805d, sequence
//	                 f396 f397 f398 f399 counting up across 21 frames
//	QSP sends        stream 7e5d 7e5d 72fc 72fc, counter a8 a8 a9 a9, flags
//	                 805d always, sequence 0 1 0 1 across 36 frames
//
// **MMDVMHost gives every data burst its own stream ID**, and this encoder kept
// its per-transmission state under that ID — so every burst restarted the
// transmission. QSP sent eighteen fragments of two frames, each announcing
// itself as the continuation of nothing, and the far repeater reassembled a
// message from none of them.
func TestATextIsOneTransmission(t *testing.T) {
	c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11})
	if err != nil {
		t.Fatalf("%v", err)
	}

	// Real text bursts, each restamped with its own stream as MMDVMHost does.
	var bursts []hbp.Data
	for _, m := range textMessages(t) {
		if out, ok := c.ConvertText(m, hbp.RepeaterID(3132910)); ok {
			bursts = append(bursts, out)
		}
	}
	if len(bursts) < 4 {
		t.Fatalf("the fixture yielded %d bursts; this test would prove nothing", len(bursts))
	}

	at := time.Unix(1757000000, 0).UTC()
	e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{ColourCode: 11})
	e.Now = func() time.Time { return at }

	streams := map[uint16]int{}
	counters := map[byte]int{}
	var sent int
	for i, b := range bursts {
		// A fresh stream per burst, in the shape streamFor produces: the
		// varying part in the high half, since that is what the encoder
		// writes. Ids that differed only in the low half made every outbound
		// stream 0x0000 and the first assertion below passed on nothing.
		b.StreamID = hbp.StreamID(uint32(0x1000+i)<<16 | 0xcdee)
		b.SourceID = 3132910
		b.TargetID = 3155373
		at = at.Add(60 * time.Millisecond)
		enc, _ := e.Encode(b)
		for _, m := range enc {
			streams[binary.BigEndian.Uint16(m.Body[10:12])]++
			counters[m.Body[0]]++
			sent++
		}
	}
	if sent == 0 {
		t.Fatal("the text encoded to nothing")
	}
	if len(streams) != 1 {
		t.Errorf("one text went out under %d stream IDs; a repeater sees that many "+
			"transmissions and reassembles a message from none of them", len(streams))
	}
	if len(counters) != 1 {
		t.Errorf("one text went out under %d call counters", len(counters))
	}

	// **And a text a minute later is a different transmission.** Merging on
	// source and target alone would join two messages into one, which is the
	// mistake in the other direction.
	at = at.Add(2 * calls.DataBurstWindow)
	b := bursts[0]
	b.StreamID = hbp.StreamID(uint32(0x9999)<<16 | 0xcdee)
	b.SourceID = 3132910
	b.TargetID = 3155373
	enc, _ := e.Encode(b)
	for _, m := range enc {
		streams[binary.BigEndian.Uint16(m.Body[10:12])]++
	}
	if len(streams) != 2 {
		t.Errorf("a text %s after the last is still the same transmission",
			2*calls.DataBurstWindow)
	}
}

// mustEncode returns the messages an encoder produced, ignoring whether the
// frame was understood. Tests that care assert on the flag themselves.
func mustEncode(e *ipscbridge.Encoder, frame hbp.Data) []ipsc.Message {
	msgs, _ := e.Encode(frame)
	return msgs
}

// TestADeliberateDropIsNotAFailure is a warning that fired on correct
// behaviour.
//
// This encoder drops a voice Link Control header on purpose, because it builds
// its own three headers from the voice stream. The caller warns about a frame
// it cannot carry, and with one answer for both cases it warned once per over
// per repeater on every transmission on the network — a wall of WARN lines
// during an ordinary rag-chew, on 2026-09-06, minutes after the warning was
// added.
//
// **Nothing to send and could not be read are different answers.** A warning
// that fires on correct behaviour is how a warning stops being read, which is
// the same defect the routing reaper had that afternoon.
func TestADeliberateDropIsNotAFailure(t *testing.T) {
	e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{ColourCode: 11})

	// The header burst MMDVMHost opens a transmission with.
	msgs, understood := e.Encode(hbp.Data{
		SourceID: 3132910, TargetID: 2, Timeslot: hbp.Timeslot2,
		CallType: hbp.CallGroup, FrameType: hbp.FrameTypeSync,
		DataType: dmrfec.DataTypeVoiceLCHeader, StreamID: 0xC0FFEE,
	})
	if len(msgs) != 0 {
		t.Errorf("a voice header produced %d messages; the encoder builds its own", len(msgs))
	}
	if !understood {
		t.Error("a voice header the encoder drops on purpose is reported as unreadable, " +
			"so every transmission on the network warns")
	}

	// **The other answer is unproven, and that is worth writing down.** No
	// frame could be constructed that this encoder refuses: zeroed, all ones
	// and patterned payloads were all accepted, on both voice frame types and
	// on a data burst, because the FEC decoders correct rather than reject.
	//
	// So the warning the caller emits for an unreadable frame may never fire
	// on this network, and asserting it here would be asserting something
	// nobody has demonstrated. The flag stays because the branch exists and
	// costs nothing; if a frame ever does come back false, that is a capture
	// worth taking.
}
