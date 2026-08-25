package hbp_test

import (
	"bytes"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// FuzzParse asserts the two properties that matter for code reading hostile
// UDP: it never panics, and anything it accepts re-serialises exactly.
//
// The second property is the stronger one. A parser that accepts a frame but
// cannot reproduce it has lost information, and QSP relays frames — a lossy
// parse would corrupt traffic passing through rather than merely mis-report it.
func FuzzParse(f *testing.F) {
	// Seed with real frames from the fixtures.
	for _, fixture := range []string{loginFixture, voiceFixture} {
		for _, p := range readCapture(f, fixture) {
			f.Add(p.Payload)
		}
	}
	// Plus edge cases the fixtures cannot contain.
	f.Add([]byte{})
	f.Add([]byte("RPTL"))
	f.Add([]byte("RPTCL\x00\x00\x00\x00"))
	f.Add([]byte("DMRD"))
	f.Add(bytes.Repeat([]byte{0xFF}, 1500))
	f.Add(append([]byte("DMRD"), bytes.Repeat([]byte{0x00}, 60)...))

	f.Fuzz(func(t *testing.T, data []byte) {
		msg, err := hbp.Parse(data)
		if err != nil {
			if msg != nil {
				t.Fatalf("Parse returned both a message and an error")
			}
			return
		}
		if msg == nil {
			t.Fatal("Parse returned no message and no error")
		}

		out := msg.Marshal()
		if !bytes.Equal(out, data) {
			t.Fatalf("accepted input did not round-trip\n  in:  %x\n  out: %x", data, out)
		}

		// Re-parsing our own output must be stable.
		again, err := hbp.Parse(out)
		if err != nil {
			t.Fatalf("re-parsing marshalled output failed: %v", err)
		}
		if !bytes.Equal(again.Marshal(), out) {
			t.Fatal("marshal is not idempotent")
		}
		if again.Kind() != msg.Kind() {
			t.Fatalf("kind changed on re-parse: %s then %s", msg.Kind(), again.Kind())
		}
	})
}
