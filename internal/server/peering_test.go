package server

import (
	"fmt"
	"testing"

	"github.com/k9mls/qsp/internal/peering"
)

// TestAReciprocalNeedsNoPassphraseTyped is the defect that stopped the first
// peering anybody attempted from a browser.
//
// A peering is agreed in two halves and **only one passphrase exists** —
// OpenBridge authenticates every datagram against one shared secret, so the
// accepting side is not choosing a new one. Which means the administrator
// pasting a reciprocal back has nothing to type, and the form demanded it
// anyway: "a passphrase must be at least 24 characters", printed under an empty
// box that could not be filled, on the second of two steps after everything
// else had worked.
func TestAReciprocalNeedsNoPassphraseTyped(t *testing.T) {
	var held offeredPassphrases

	const passphrase = "a-passphrase-long-enough-to-be-accepted"
	fingerprint := peering.FingerprintOf(passphrase)

	held.put(fingerprint, passphrase)
	got, ok := held.take(fingerprint)
	if !ok || got != passphrase {
		t.Fatalf("the offered passphrase was not held: got %q ok=%v", got, ok)
	}

	// **Forgotten on use.** The peering is written at that point and the secret
	// lives in its passphrase file; a second copy in memory afterwards is one
	// nobody asked for.
	if _, ok := held.take(fingerprint); ok {
		t.Error("the passphrase was still held after being used")
	}

	// An invitation this instance did not offer has nothing held, and the
	// operator is asked for the passphrase as before.
	if _, ok := held.take(peering.FingerprintOf("somebody else's secret")); ok {
		t.Error("a passphrase was produced for an offer this instance never made")
	}
}

// TestHeldOffersAreBounded keeps an authenticated administrator generating
// offers in a loop from growing the process.
func TestHeldOffersAreBounded(t *testing.T) {
	var held offeredPassphrases
	for i := 0; i < offerMemoryMax*3; i++ {
		held.put(peering.FingerprintOf(fmt.Sprintf("offer-%d", i)), "secret")
	}
	held.mu.Lock()
	n := len(held.held)
	held.mu.Unlock()
	if n > offerMemoryMax {
		t.Errorf("%d offers held, want at most %d", n, offerMemoryMax)
	}
	// The most recent must survive: a fresh offer always works, and an
	// abandoned one is what is lost.
	last := peering.FingerprintOf(fmt.Sprintf("offer-%d", offerMemoryMax*3-1))
	if _, ok := held.take(last); !ok {
		t.Error("the most recent offer was evicted")
	}
}

// TestTheExchangeEnds is the test that was missing, and its absence cost an
// operator half an hour of being told where to paste things.
//
// A peering has two halves. One side offers; the other accepts and replies; the
// first side accepts the reply and **it is over**. `handleAcceptPeering` built a
// reciprocal unconditionally, so accepting a reply produced another reply,
// which the page presented as one more thing to send back. There was no end to
// it, and no instruction from anybody could have got the operator out, because
// the page kept handing them a fresh token.
//
// The evidence that distinguishes the two halves was already there: the
// offering instance holds its own passphrase, so a held one means this is the
// reply to our own offer.
func TestTheExchangeEnds(t *testing.T) {
	var held offeredPassphrases
	const passphrase = "a-passphrase-long-enough-to-be-accepted"
	fingerprint := peering.FingerprintOf(passphrase)

	// Alice offers.
	held.put(fingerprint, passphrase)

	// Bob accepts: he is not holding the passphrase, so he replies.
	if _, closing := held.take(peering.FingerprintOf("bob has never seen this")); closing {
		t.Fatal("an instance produced a passphrase for an offer it never made")
	}

	// Alice accepts Bob's reply: she is holding it, so the exchange ends.
	got, closing := held.take(fingerprint)
	if !closing {
		t.Fatal("the offering side did not recognise the reply to its own offer")
	}
	if got != passphrase {
		t.Errorf("the held passphrase was %q", got)
	}

	// **And it cannot end twice.** A second accept of the same reply finds
	// nothing held and would reply again — which is the loop, and is why the
	// passphrase is forgotten on use rather than kept.
	if _, again := held.take(fingerprint); again {
		t.Error("the exchange could be closed twice from one offer")
	}
}
