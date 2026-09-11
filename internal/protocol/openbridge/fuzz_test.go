package openbridge_test

import (
	"bytes"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/openbridge"
)

// FuzzVerify and FuzzParse assert what matters for a signature-checking parser
// reading hostile UDP.
//
// **This boundary was unfuzzed**, which pass two of the code review found: hbp,
// ipsc, peers, routing and access all had a fuzz target and the two remaining
// protocol parsers did not. Openbridge is the one that carries a passphrase, so
// it is the one where a parse bug is a way in rather than a nuisance.
func FuzzVerify(f *testing.F) {
	pass := []byte("a-passphrase")

	// A correctly signed packet, so the corpus contains something that passes.
	frame := make([]byte, openbridge.FrameSize)
	copy(frame, "DMRD")
	for i := 4; i < len(frame); i++ {
		frame[i] = byte(i)
	}
	if signed, err := openbridge.Sign(frame, pass); err == nil {
		f.Add(signed)
	}

	f.Add([]byte{})
	f.Add([]byte("DMRD"))
	f.Add(make([]byte, openbridge.FrameSize))
	f.Add(make([]byte, openbridge.PacketSize))
	f.Add(make([]byte, openbridge.PacketSize+1))
	f.Add(bytes.Repeat([]byte{0xFF}, 1500))

	f.Fuzz(func(t *testing.T, data []byte) {
		frame, err := openbridge.Verify(data, pass)
		if err != nil {
			// A refusal must not also hand back a frame, or a caller checking
			// the value rather than the error relays something unverified.
			if frame != nil {
				t.Fatalf("Verify refused %d bytes and still returned a frame", len(data))
			}
			return
		}

		// **Accepted means the signature held**, so the frame must be exactly
		// the signed portion — no more and no less. A verifier that returned a
		// different length would be handing on bytes the signature did not
		// cover.
		if len(frame) != openbridge.FrameSize {
			t.Fatalf("verified a packet and returned %d bytes, want %d",
				len(frame), openbridge.FrameSize)
		}
		if !bytes.Equal(frame, data[:openbridge.FrameSize]) {
			t.Fatal("the verified frame is not the bytes the signature covered")
		}

		// Re-signing must reproduce the packet, or signing and verifying
		// disagree about what is covered.
		again, err := openbridge.Sign(frame, pass)
		if err != nil {
			t.Fatalf("a frame that verified cannot be signed: %v", err)
		}
		if !bytes.Equal(again, data) {
			t.Fatalf("sign and verify disagree:\n in  %x\n out %x", data, again)
		}
	})
}

// A wrong passphrase must never verify, whatever the input. **This is the
// property an attacker attacks**, so it is worth asserting separately from the
// round trip.
func FuzzVerifyRejectsAWrongPassphrase(f *testing.F) {
	right := []byte("the-right-passphrase")
	wrong := []byte("the-wrong-passphrase")

	frame := make([]byte, openbridge.FrameSize)
	copy(frame, "DMRD")
	if signed, err := openbridge.Sign(frame, right); err == nil {
		f.Add(signed)
	}
	f.Add(make([]byte, openbridge.PacketSize))

	f.Fuzz(func(t *testing.T, data []byte) {
		if _, err := openbridge.Verify(data, wrong); err == nil {
			// Only a genuine forgery could reach here, and finding one would be
			// a finding about HMAC-SHA1 rather than about this code — but a
			// test that cannot report it is a test that would hide it.
			if _, ok := openbridge.Verify(data, right); ok == nil {
				t.Fatalf("a packet verified under both passphrases: %x", data)
			}
			t.Fatalf("a packet verified under the wrong passphrase: %x", data)
		}
	})
}
