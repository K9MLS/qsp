package p25

import (
	"bytes"
	"testing"
)

// FuzzParse asserts the two properties that matter for code reading hostile
// UDP, in the shape the hbp package established.
//
// **The second is the stronger one.** A parser that accepts a frame and cannot
// reproduce it byte for byte has lost information — and QSP relays P25 rather
// than interpreting it, so a lossy parse would corrupt audio passing through
// rather than merely mis-report it. That is the whole promise of this package
// and the reason ADR-0034 exists.
//
// This boundary was unfuzzed when it was written, which was found by pass two of
// the code review: `hbp`, `ipsc`, `peers`, `routing` and `access` all had a
// fuzz target and the two newest parsers did not.
func FuzzParse(f *testing.F) {
	// Every real frame from the capture, so the corpus starts from traffic that
	// actually crossed a network.
	for _, p := range readCapture(f, "../../../testdata/p25/p25-voice.pcap") {
		if len(p.Payload) > 0 {
			f.Add(p.Payload)
		}
	}

	// Plus the shapes a capture cannot contain.
	f.Add([]byte{})
	f.Add([]byte{0x62})
	f.Add([]byte{0x80})
	f.Add([]byte{0xF0})
	f.Add(bytes.Repeat([]byte{0x62}, 22))
	f.Add(bytes.Repeat([]byte{0xFF}, 1500))
	f.Add(append([]byte{0x62}, bytes.Repeat([]byte{0x00}, 21)...))

	f.Fuzz(func(t *testing.T, data []byte) {
		frame, err := Parse(data)
		if err != nil {
			// A refusal must not also return something usable, or a caller
			// checking the value rather than the error relays a frame the
			// parser rejected.
			if frame.Kind != 0 || frame.Payload != nil {
				t.Fatalf("Parse refused %x and still returned %+v", data, frame)
			}
			return
		}

		// Accepted: it must reproduce exactly.
		if got := frame.Marshal(); !bytes.Equal(got, data) {
			t.Fatalf("accepted and could not reproduce:\n in  %x\n out %x", data, got)
		}

		// And the length must be the one the table promises, or the table is
		// describing something other than what Parse enforces.
		if want := frameLength[frame.Kind]; len(data) != want {
			t.Fatalf("accepted 0x%02x at %d bytes; the table says %d",
				byte(frame.Kind), len(data), want)
		}
	})
}

// FuzzParsePoll checks the keepalive path separately, because it reads a
// fixed-width text field and trims padding — the one place this package looks
// inside a payload at all.
func FuzzParsePoll(f *testing.F) {
	f.Add([]byte{0xF0, 'K', '9', 'M', 'L', 'S', ' ', ' ', ' ', ' ', ' '})
	f.Add([]byte{0xF0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0xF0, ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '})
	f.Add([]byte{0xF0})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		poll, err := ParsePoll(data)
		if err != nil {
			return
		}
		// **A parsed poll must round-trip too.** The callsign is trimmed of
		// padding on the way in and re-padded on the way out, and a callsign
		// that came back different would mean QSP announcing a station under a
		// name nobody chose.
		if got := poll.Marshal(); !bytes.Equal(got, data) {
			t.Fatalf("a poll changed:\n in  %x\n out %x", data, got)
		}
	})
}
