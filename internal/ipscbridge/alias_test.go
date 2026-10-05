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

// aliasCapture is an XPR8300 passing on two key-ups from a radio that sends a
// Talker Alias. See testdata/ipsc/ipsc-talker-alias.md.
const aliasCapture = "../../testdata/ipsc/ipsc-talker-alias.pcap"

const (
	aliasRadio     = 3132910
	aliasTalkgroup = 2
	aliasRepeater  = 999999
)

// aliasKeyUps returns the capture's voice messages, one slice per key-up. A
// key-up starts at the frame the repeater flags as the first of a transmission.
func aliasKeyUps(tb testing.TB) [][]ipsc.Message {
	tb.Helper()
	raw, err := os.ReadFile(aliasCapture)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	var out [][]ipsc.Message
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < 42 {
			continue
		}
		msg, err := ipsc.Parse(rec[42:])
		if err != nil || !msg.Kind.IsVoice() {
			continue
		}
		v, _ := msg.AsVoice()
		if v.IsFirstFrame() || len(out) == 0 {
			out = append(out, nil)
		}
		out[len(out)-1] = append(out[len(out)-1], msg)
	}
	if len(out) != 2 {
		tb.Fatalf("the capture holds %d key-ups, want 2", len(out))
	}
	return out
}

// summary is what a converted key-up amounts to, as a routing core sees it.
type summary struct {
	streams    map[hbp.StreamID]bool
	sources    map[uint32]bool
	targets    map[uint32]bool
	headers    int
	terms      int
	voice      int
	sequenceOK bool
}

func summarise(frames []hbp.Data) summary {
	s := summary{
		streams: map[hbp.StreamID]bool{}, sources: map[uint32]bool{},
		targets: map[uint32]bool{}, sequenceOK: true,
	}
	for i, f := range frames {
		s.streams[f.StreamID] = true
		s.sources[f.SourceID] = true
		s.targets[f.TargetID] = true
		if f.Sequence != uint8(i) {
			s.sequenceOK = false
		}
		switch {
		case f.FrameType != hbp.FrameTypeSync:
			s.voice++
		case f.DataType == dmrfec.DataTypeVoiceLCHeader:
			s.headers++
		case f.DataType == dmrfec.DataTypeTerminatorWithLC:
			s.terms++
		}
	}
	return s
}

// TestAnOverWithATalkerAliasIsOneTransmission replays both captured key-ups.
//
// **Until 0.1.311 each became three transmissions**: the radio's own, one from
// "5002016" to "TG 4929869" — the letters "LS " and "K9M" out of the alias —
// and the radio's own again under a new stream ID. The first never ended, so
// every destination was held and refused the third until the hold ran out.
//
// To see it fail: make slotState.continues return false. Each key-up then
// reports three streams, two sources and three headers.
func TestAnOverWithATalkerAliasIsOneTransmission(t *testing.T) {
	keyUps := aliasKeyUps(t)
	tests := []struct {
		name      string
		keyUp     int
		wantVoice int
	}{
		// Voice frames received, less the ones before the first superframe
		// boundary, which Convert withholds by design.
		{"the longer over", 0, 102},
		{"the shorter over", 1, 48},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 11, SlotBitIsTimeslot2: true})
			if err != nil {
				t.Fatal(err)
			}
			var frames []hbp.Data
			for _, m := range keyUps[tc.keyUp] {
				frames = append(frames, c.Convert(m, aliasRepeater)...)
			}
			s := summarise(frames)

			if len(s.streams) != 1 {
				t.Errorf("%d stream IDs for one key-up, want 1", len(s.streams))
			}
			if len(s.sources) != 1 || !s.sources[aliasRadio] {
				t.Errorf("sources %v, want only %d; an alias is not a station",
					s.sources, aliasRadio)
			}
			if len(s.targets) != 1 || !s.targets[aliasTalkgroup] {
				t.Errorf("targets %v, want only TG %d", s.targets, aliasTalkgroup)
			}
			if s.headers != 1 || s.terms != 1 {
				t.Errorf("%d headers and %d terminators, want one of each",
					s.headers, s.terms)
			}
			if s.voice != tc.wantVoice {
				t.Errorf("%d voice frames, want %d; audio was dropped at the alias",
					s.voice, tc.wantVoice)
			}
			if !s.sequenceOK {
				t.Error("the Homebrew sequence restarted inside the over")
			}
			if last := frames[len(frames)-1]; last.DataType != dmrfec.DataTypeTerminatorWithLC ||
				last.FrameType != hbp.FrameTypeSync {
				t.Error("the over does not end on its terminator")
			}
		})
	}
}

// TestTheAudioAcrossAnAliasIsTheAudioTheRepeaterSent holds the voice path to
// its rule through the frames this patch re-labels: the vocoder bytes are
// copied, whatever the header around them says.
//
// To see it fail: have slotState.under return the frame untouched and the
// converter restart on the alias, which withholds frames until the next
// superframe boundary; the count below then falls short.
func TestTheAudioAcrossAnAliasIsTheAudioTheRepeaterSent(t *testing.T) {
	keyUp := aliasKeyUps(t)[0]
	c, _ := ipscbridge.New(ipscbridge.Config{ColourCode: 11, SlotBitIsTimeslot2: true})

	checked := 0
	for _, m := range keyUp {
		_, core, _, ok := m.Payload()
		for _, f := range c.Convert(m, aliasRepeater) {
			if f.FrameType == hbp.FrameTypeSync {
				continue
			}
			if !ok {
				t.Fatal("a voice burst came from a message with no vocoder payload")
			}
			got, _, gok := dmrfec.IPSCFromBurst(f.Payload[:])
			if !gok {
				t.Fatal("a produced burst does not read back")
			}
			if string(got) != string(core) {
				t.Fatalf("frame %d: the vocoder bytes changed crossing the bridge", checked)
			}
			checked++
		}
	}
	if checked != 102 {
		t.Errorf("%d voice frames compared, want 102", checked)
	}
}

// TestWhatStartsANewTransmission is the other side of the rule: a frame that
// describes itself differently is the open transmission's *unless the repeater
// marks a beginning*, and each mark must still work.
//
// To see each case fail, remove its clause from slotState.continues (or the
// body of Forget, for the last); the case then reports 1 header where it wants
// 2, because the second over was folded into the first.
func TestWhatStartsANewTransmission(t *testing.T) {
	keyUps := aliasKeyUps(t)
	first, second := keyUps[0], keyUps[1]
	// The first over with its terminator lost, which is when the rule matters.
	unfinished := first[:len(first)-1]

	headersOnly := func(ms []ipsc.Message) (out []ipsc.Message) {
		for _, m := range ms {
			if v, _ := m.AsVoice(); !v.IsFirstFrame() {
				out = append(out, m)
			}
		}
		return out
	}
	noHeaders := func(ms []ipsc.Message) (out []ipsc.Message) {
		for _, m := range ms {
			if len(m.Body) > 25 && ipsc.FrameKindOf(m.Body[25]) != ipsc.FrameHeader {
				out = append(out, m)
			}
		}
		return out
	}

	// A voice frame carrying the first-frame flag, which no capture holds: the
	// flag has only been seen on a header. It is honoured wherever it appears.
	flagged := func(ms []ipsc.Message) []ipsc.Message {
		out := append([]ipsc.Message(nil), ms...)
		body := append([]byte(nil), out[0].Body...)
		binary.BigEndian.PutUint16(body[13:15], 0x80dd)
		out[0].Body = body
		return out
	}

	tests := []struct {
		name        string
		next        []ipsc.Message
		forget      bool
		wantHeaders int
		wantStreams int
	}{
		{"the next over arrives whole", second, false, 2, 2},
		{"its flagged first header is lost", headersOnly(second), false, 2, 2},
		{"every header is lost and the slot went quiet", noHeaders(second), true, 2, 2},
		{"every header is lost but a voice frame is flagged first", flagged(noHeaders(second)), false, 2, 2},
		// What the timeout is for: with no mark at all and no pause, the
		// frames can only be read as the open over continuing.
		{"every header is lost and there was no pause", noHeaders(second), false, 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := ipscbridge.New(ipscbridge.Config{ColourCode: 11, SlotBitIsTimeslot2: true})
			var frames []hbp.Data
			for _, m := range unfinished {
				frames = append(frames, c.Convert(m, aliasRepeater)...)
			}
			if tc.forget {
				c.Forget(hbp.Timeslot2)
			}
			for _, m := range tc.next {
				frames = append(frames, c.Convert(m, aliasRepeater)...)
			}
			headers, streams := 0, map[hbp.StreamID]bool{}
			for _, f := range frames {
				streams[f.StreamID] = true
				if f.FrameType == hbp.FrameTypeSync && f.DataType == dmrfec.DataTypeVoiceLCHeader {
					headers++
				}
			}
			if headers != tc.wantHeaders || len(streams) != tc.wantStreams {
				t.Errorf("%d headers over %d streams, want %d over %d",
					headers, len(streams), tc.wantHeaders, tc.wantStreams)
			}
		})
	}
}

// TestResolveAgreesWithConvert keeps the listener's record and the converter's
// output describing the same call, frame by frame.
//
// To see it fail: have Resolve return the header as the repeater wrote it. The
// six alias frames then resolve to source 5002016.
func TestResolveAgreesWithConvert(t *testing.T) {
	c, _ := ipscbridge.New(ipscbridge.Config{ColourCode: 11, SlotBitIsTimeslot2: true})
	rewritten := 0
	for i, m := range aliasKeyUps(t)[0] {
		raw, _ := m.AsVoice()
		v, ok := c.Resolve(m)
		if !ok {
			t.Fatalf("frame %d did not resolve", i)
		}
		if v.SourceID != aliasRadio || v.Destination != aliasTalkgroup {
			t.Fatalf("frame %d resolves to %d calling %d, want %d calling TG %d",
				i, v.SourceID, v.Destination, aliasRadio, aliasTalkgroup)
		}
		if v.Flags != raw.Flags || v.Sequence != raw.Sequence {
			t.Fatalf("frame %d: Resolve changed what belongs to the frame", i)
		}
		if raw.SourceID != aliasRadio {
			rewritten++
		}
		for _, f := range c.Convert(m, aliasRepeater) {
			if f.SourceID != v.SourceID || uint32(f.StreamID>>16) != uint32(v.StreamID) {
				t.Fatalf("frame %d: Convert and Resolve disagree", i)
			}
		}
	}
	if rewritten != 6 {
		t.Errorf("%d frames carried the alias in their header, want 6; "+
			"the capture is not the one this test describes", rewritten)
	}
}
