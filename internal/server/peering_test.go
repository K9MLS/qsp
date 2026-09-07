package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
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

// TestALinkCanBeRemoved is the gap an operator found by using the page.
//
// **Accepting a peering wrote an upstream, a bridge and a passphrase file, and
// nothing could undo any of it.** An operator whose first attempt went wrong —
// and the first attempt went wrong, because the exchange could not terminate —
// was left with a broken link on the page for good, unless they edited JSON on
// the server. Which is the thing this page exists to avoid.
func TestALinkCanBeRemoved(t *testing.T) {
	dir := t.TempDir()
	pass := filepath.Join(dir, "test.pass")
	if err := os.WriteFile(pass, []byte("a-secret"), 0o600); err != nil {
		t.Fatalf("%v", err)
	}

	cfg := config.Config{}
	cfg.DMR.Upstreams = []config.Upstream{
		{Name: "test", PassphraseFile: pass},
		{Name: "keep"},
	}
	cfg.DMR.Bridges = []config.Bridge{
		{Name: "test-link", Endpoints: []config.Endpoint{{Upstream: "test"}}},
		{Name: "mine", Endpoints: []config.Endpoint{{Upstream: "test"}}},
		{Name: "unrelated"},
	}

	// The three things accept created, and only those.
	after, orphaned := removeLinkFrom(cfg, "test")

	if len(after.DMR.Upstreams) != 1 || after.DMR.Upstreams[0].Name != "keep" {
		t.Errorf("upstreams after removal: %+v", after.DMR.Upstreams)
	}
	for _, b := range after.DMR.Bridges {
		if b.Name == "test-link" {
			t.Error("the bridge accept created was left behind")
		}
	}
	// **A bridge an operator wrote is left alone**, even though it routes to
	// the upstream being removed. Deleting somebody's hand-written
	// configuration because it referred to something else is a surprise nobody
	// asked for; it is named instead.
	var kept []string
	for _, b := range after.DMR.Bridges {
		kept = append(kept, b.Name)
	}
	if len(kept) != 2 {
		t.Errorf("bridges after removal: %v", kept)
	}
	if len(orphaned) != 1 || orphaned[0] != "mine" {
		t.Errorf("orphaned bridges reported as %v, want [mine]", orphaned)
	}
}

// removeLinkFrom is the decision handleRemoveLink makes, without the HTTP.
func removeLinkFrom(cfg config.Config, name string) (config.Config, []string) {
	kept := make([]config.Upstream, 0, len(cfg.DMR.Upstreams))
	var removed string
	for _, u := range cfg.DMR.Upstreams {
		if strings.EqualFold(u.Name, name) {
			removed = u.Name
			continue
		}
		kept = append(kept, u)
	}
	cfg.DMR.Upstreams = kept

	var orphaned []string
	bridges := make([]config.Bridge, 0, len(cfg.DMR.Bridges))
	for _, b := range cfg.DMR.Bridges {
		if strings.EqualFold(b.Name, removed+"-link") {
			continue
		}
		if bridgeMentions(b, removed) {
			orphaned = append(orphaned, b.Name)
		}
		bridges = append(bridges, b)
	}
	cfg.DMR.Bridges = bridges
	return cfg, orphaned
}
