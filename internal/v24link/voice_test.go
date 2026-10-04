package v24link

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/p25"
)

const voiceFixture = "../../testdata/quantar/stun-voice-three-calls.bin"

// stationFrames reads every serial frame in a capture.
func stationFrames(t *testing.T, path string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the capture: %v", err)
	}
	r := bytes.NewReader(raw)
	var out [][]byte
	for {
		f, err := ReadFrame(r)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("frame %d: %v", len(out), err)
		}
		if f.Op == OpData {
			out = append(out, f.Payload)
		}
	}
}

// The three transmissions keyed on 2026-10-04, read from the capture: each
// opened and closed by the repeater, each naming talkgroup 1 and radio
// 8080303, and their voice frames adding up to every one in the file.
//
// Break it: read the radio from frame 66 without looking at frame 64, and the
// first two calls, which end on Motorola's link control, are recorded against
// radio 12251096. Count a header as voice, and each call is two frames over.
func TestTheCapturedCallsAreReadWhole(t *testing.T) {
	want := []struct {
		frames uint64
	}{{135}, {81}, {297}}

	var (
		calls   []Call
		call    *Call
		records int
		now     = time.Date(2026, 10, 4, 16, 36, 0, 0, time.UTC)
	)
	for _, payload := range stationFrames(t, voiceFixture) {
		rec, ok := ReadRecord(payload)
		if !ok {
			continue
		}
		records++
		if rec.Kind == RecordUnknown {
			t.Fatalf("a record in the capture is not understood: % x", payload)
		}
		now = now.Add(20 * time.Millisecond)
		var finished *Call
		call, finished, _ = heard(call, rec, now)
		if finished != nil {
			calls = append(calls, *finished)
		}
	}
	if records != 528 {
		t.Errorf("read %d records, and the capture holds 528", records)
	}
	if call != nil {
		t.Error("a transmission is still open at the end of the capture")
	}
	if len(calls) != len(want) {
		t.Fatalf("heard %d transmissions, want %d", len(calls), len(want))
	}
	for i, c := range calls {
		if c.Frames != want[i].frames || c.Talkgroup != 1 || c.SourceID != 8080303 || !c.Marked {
			t.Errorf("transmission %d: %d frames, talkgroup %d, radio %d, closed by the repeater %v",
				i+1, c.Frames, c.Talkgroup, c.SourceID, c.Marked)
		}
	}
}

// Break it: accept a marker of any length, name a marker by its first byte
// alone, or take any frame with control 03 as voice, and a row fails.
func TestWhatARecordIs(t *testing.T) {
	voice := append([]byte{0x07, 0x03, 0x63}, make([]byte, 13)...)
	tests := []struct {
		name     string
		in       []byte
		isRecord bool
		kind     RecordKind
	}{
		{"the start marker as captured", []byte{0x07, 0x03, 0x00, 0x02, 0x02, 0x0C, 0x0B, 0, 0, 0, 0, 0}, true, RecordStart},
		{"the end marker as captured", []byte{0x07, 0x03, 0x00, 0x02, 0x02, 0x25, 0x0B, 0, 0, 0, 0, 0}, true, RecordEnd},
		{"a marker that is neither", []byte{0x07, 0x03, 0x00, 0x02, 0x02, 0x26, 0x0B, 0, 0, 0, 0, 0}, true, RecordUnknown},
		{"a marker cut short", []byte{0x07, 0x03, 0x00, 0x02, 0x02, 0x0C, 0x0B}, true, RecordUnknown},
		{"the first half of a header", append([]byte{0x07, 0x03, 0x60}, make([]byte, 29)...), true, RecordHeader},
		{"the second half of a header", append([]byte{0x07, 0x03, 0x61}, make([]byte, 21)...), true, RecordHeader},
		{"a header of the wrong length", append([]byte{0x07, 0x03, 0x60}, make([]byte, 21)...), true, RecordUnknown},
		{"a voice frame", voice, true, RecordVoice},
		{"a voice frame from another address", append([]byte{0xFD}, voice[1:]...), true, RecordVoice},
		{"a voice frame of the wrong length", voice[:len(voice)-1], true, RecordUnknown},
		{"the gateway protocol's terminator, which a repeater does not send",
			append([]byte{0x07, 0x03, 0x80}, make([]byte, 16)...), true, RecordUnknown},
		{"a record type nobody has seen", []byte{0x07, 0x03, 0x5F, 0x00}, true, RecordUnknown},
		{"a keepalive", []byte{0xFD, 0x01}, false, RecordUnknown},
		{"an introduction", stationIntroduction, false, RecordUnknown},
		{"an information frame with nothing in it", []byte{0x07, 0x03}, false, RecordUnknown},
		{"nothing", nil, false, RecordUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, ok := ReadRecord(tc.in)
			if ok != tc.isRecord || rec.Kind != tc.kind {
				t.Errorf("got kind %d, record %v", rec.Kind, ok)
			}
		})
	}
}

func voiceFrame(t *testing.T, kind p25.Kind, lc ...byte) p25.Frame {
	t.Helper()
	lengths := map[p25.Kind]int{p25.KindVoice1: 22, p25.KindVoice3: 17, p25.KindVoice4: 17, p25.KindVoice5: 17}
	raw := make([]byte, lengths[kind])
	raw[0] = byte(kind)
	copy(raw[1:], lc)
	f, err := p25.Parse(raw)
	if err != nil {
		t.Fatalf("building a frame: %v", err)
	}
	return f
}

// The link control as captured: the standard word and Motorola's own, turn
// and turn about.
//
// Break it: trust frames 65 and 66 whatever frame 64 said, or keep trusting
// them into the next voice unit, and a row fails.
func TestOnlyTheStandardLinkControlNamesTheTalker(t *testing.T) {
	type step struct {
		kind p25.Kind
		lc   []byte
	}
	standard := []step{
		{p25.KindVoice1, nil},
		{p25.KindVoice3, []byte{0x00, 0x00, 0x04}},
		{p25.KindVoice4, []byte{0x00, 0x00, 0x01}},
		{p25.KindVoice5, []byte{0x7B, 0x4B, 0xAF}},
	}
	motorola := []step{
		{p25.KindVoice1, nil},
		{p25.KindVoice3, []byte{0x06, 0x90, 0x01}},
		{p25.KindVoice4, []byte{0x2F, 0x24, 0xD9}},
		{p25.KindVoice5, []byte{0xBA, 0xEF, 0xD8}},
	}
	tests := []struct {
		name      string
		steps     []step
		talkgroup uint16
		source    uint32
	}{
		{"the standard word", standard, 1, 8080303},
		{"Motorola's word names nobody", motorola, 0, 0},
		{"standard then Motorola keeps what was said", append(append([]step{}, standard...), motorola...), 1, 8080303},
		{"Motorola then standard", append(append([]step{}, motorola...), standard...), 1, 8080303},
		{"the talkgroup and radio with no format before them", standard[2:], 0, 0},
		{"a format left over from the unit before does not carry",
			[]step{standard[0], standard[1], {p25.KindVoice1, nil}, standard[2], standard[3]}, 0, 0},
		{"a group call under another manufacturer",
			[]step{standard[0], {p25.KindVoice3, []byte{0x00, 0x90, 0x04}}, standard[2], standard[3]}, 0, 0},
		{"a talkgroup with its reserved byte set is not a talkgroup",
			[]step{standard[0], standard[1], {p25.KindVoice4, []byte{0x01, 0x00, 0x01}}, standard[3]}, 0, 8080303},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var call *Call
			now := time.Unix(0, 0)
			for _, s := range tc.steps {
				call, _, _ = heard(call, Record{Kind: RecordVoice, Voice: voiceFrame(t, s.kind, s.lc...)}, now)
			}
			if call.Talkgroup != tc.talkgroup || call.SourceID != tc.source {
				t.Errorf("talkgroup %d, radio %d", call.Talkgroup, call.SourceID)
			}
		})
	}
}

// Break it: let a second end marker close a call that is not open, lose the
// call a new start interrupts, or treat a header as silence, and a row fails.
func TestHowATransmissionBeginsAndEnds(t *testing.T) {
	voice := Record{Kind: RecordVoice, Voice: p25.Frame{Kind: p25.KindVoice2, Payload: make([]byte, 13)}}
	start, end, header := Record{Kind: RecordStart}, Record{Kind: RecordEnd}, Record{Kind: RecordHeader}
	tests := []struct {
		name     string
		records  []Record
		began    int
		finished int
		marked   int
		open     bool
		frames   uint64 // of the last transmission finished, or the open one
	}{
		{"as the repeater sends it", []Record{start, header, header, voice, voice, end, end}, 1, 1, 1, false, 2},
		{"the end marker alone", []Record{end, end}, 0, 0, 0, false, 0},
		{"voice with no start, as when the link opens mid-call", []Record{voice, voice, voice, end}, 1, 1, 1, false, 3},
		{"a start with no end yet", []Record{start, voice}, 1, 0, 0, true, 1},
		{"a start interrupting one whose end was lost", []Record{start, voice, voice, start, voice}, 2, 1, 0, true, 1},
		{"two in a row", []Record{start, voice, end, end, start, voice, voice, end, end}, 2, 2, 2, false, 2},
		{"a start and an end with nothing between", []Record{start, end}, 1, 1, 1, false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				call                    *Call
				began, finished, marked int
				last                    *Call
				now                     = time.Unix(0, 0)
			)
			for _, r := range tc.records {
				now = now.Add(20 * time.Millisecond)
				var done *Call
				var b bool
				call, done, b = heard(call, r, now)
				if b {
					began++
				}
				if done != nil {
					finished++
					last = done
					if done.Marked {
						marked++
					}
					if done.Ended.IsZero() {
						t.Error("a finished transmission has no end time")
					}
				}
			}
			if call != nil {
				last = call
			}
			if began != tc.began || finished != tc.finished || marked != tc.marked || (call != nil) != tc.open {
				t.Errorf("began %d, finished %d, closed by the repeater %d, open %v",
					began, finished, marked, call != nil)
			}
			if last != nil && last.Frames != tc.frames {
				t.Errorf("%d frames", last.Frames)
			}
		})
	}
}

// Break it: measure from the start instead of the last frame, and a long
// transmission is closed while it is still being heard.
func TestATransmissionThatGoesQuietIsStale(t *testing.T) {
	t0 := time.Unix(1000, 0)
	tests := []struct {
		name  string
		call  *Call
		after time.Duration
		stale bool
	}{
		{"none in progress", nil, time.Hour, false},
		{"heard a moment ago", &Call{Started: t0, last: t0}, 100 * time.Millisecond, false},
		{"exactly at the limit", &Call{Started: t0, last: t0}, CallTimeout, false},
		{"past it", &Call{Started: t0, last: t0}, CallTimeout + time.Millisecond, true},
		{"long, and still being heard", &Call{Started: t0.Add(-time.Minute), last: t0}, 100 * time.Millisecond, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.call.stale(t0.Add(tc.after)); got != tc.stale {
				t.Errorf("stale is %v", got)
			}
		})
	}
}
