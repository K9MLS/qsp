package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// acceptHarness builds a server whose configuration can be written and whose
// passphrase files land somewhere disposable.
func acceptHarness(t *testing.T) (*Server, *stubAuth, *stubConfig, string) {
	t.Helper()
	dir := t.TempDir()
	cm := newStubConfig()
	cfg := cm.current
	cfg.DMR.PasswordFile = filepath.Join(dir, "peers.pass")
	cfg.DMR.Join.NetworkName = "Test Network"
	cfg.DMR.Identity.Callsign = "K9MLS"
	cm.current = cfg
	srv, a := newConfigServer(t, cm, &recordingAudit{})
	return srv, a, cm, dir
}

// anInvitation is what the other administrator sends.
func anInvitation(t *testing.T, passphrase string) string {
	t.Helper()
	token, err := peering.Encode(peering.Invitation{
		Network:     "Their Network",
		Callsign:    "KD9EJA",
		Address:     "their.example.com:62045",
		NetworkID:   3155373,
		Export:      []peering.Talkgroup{{Talkgroup: 2, Timeslot: 2}},
		Import:      []peering.Talkgroup{{Talkgroup: 2, Timeslot: 2}},
		Fingerprint: peering.FingerprintOf(passphrase),
		Issued:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("could not build an invitation: %v", err)
	}
	return token
}

// TestAPublicNameInTheListenFieldIsRefused is the defect that took production
// down, at the place it was written.
//
// The accept form took qsp.hopto.me:62045 in "We listen on" and wrote it into
// an upstream. That name resolves to the router, which this host is not, so QSP
// refused to start — correctly — and systemd crash-looped to its start limit.
// Recovery took two rounds of hand-edited JSON on a live server.
//
// 192.0.2.1 is TEST-NET-1: reserved, never assigned to an interface, and needs
// no DNS.
func TestAPublicNameInTheListenFieldIsRefused(t *testing.T) {
	const passphrase = "a-passphrase-long-enough-to-be-accepted"
	srv, a, cm, dir := acceptHarness(t)

	body := fmt.Sprintf(`{"token":%q,"passphrase":%q,"name":"test","talkgroup":2,
		"timeslot":2,"listen":"192.0.2.1:62045","address":"qsp.example.com:62045",
		"network_id":3132910,"confirm":true}`, anInvitation(t, passphrase), passphrase)

	rec := authed(t, srv, a, http.MethodPost, "/api/links/accept", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an unbindable listen address was accepted with %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cannot listen on") {
		t.Errorf("the refusal does not say what is wrong: %s", rec.Body.String())
	}

	// **A refusal leaves nothing behind.** The passphrase file was written
	// before any address was looked at, so a refusal after it left a .pass for
	// a link that was never created.
	if len(cm.saved) != 0 {
		t.Errorf("a configuration was written for a peering that was refused")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".pass") {
			t.Errorf("a refused peering left %s behind", e.Name())
		}
	}
}

// TestTheListenFieldIsNotTheReplyAddress is the conflation underneath the
// crash, rather than the crash.
//
// One box fed two opposite fields: the local bind address and the address the
// far end is told to send to. **Every value was wrong in one of three ways** —
// 0.0.0.0 binds and is refused by Invitation.Validate, a public name validates
// and cannot be bound, a LAN address does both and reaches nothing from
// outside. The two roles are two boxes.
func TestTheListenFieldIsNotTheReplyAddress(t *testing.T) {
	const passphrase = "a-passphrase-long-enough-to-be-accepted"
	srv, a, _, _ := acceptHarness(t)

	body := fmt.Sprintf(`{"token":%q,"passphrase":%q,"name":"test","talkgroup":2,
		"timeslot":2,"listen":"0.0.0.0:0","address":"qsp.example.com:62045",
		"network_id":3132910,"confirm":true}`, anInvitation(t, passphrase), passphrase)

	rec := authed(t, srv, a, http.MethodPost, "/api/links/accept", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("a correct peering was refused with %d: %s", rec.Code, rec.Body.String())
	}

	var got acceptResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v", err)
	}
	if got.Reciprocal == "" {
		t.Fatal("no reciprocal to send back; the console shows an empty box and says nothing")
	}
	back, err := peering.Decode(got.Reciprocal)
	if err != nil {
		t.Fatalf("the reciprocal does not decode: %v", err)
	}
	if back.Address != "qsp.example.com:62045" {
		t.Errorf("the reciprocal tells the far end to send to %q, want the public address", back.Address)
	}
}

// TestAReciprocalThatCannotBeBuiltIsReported.
//
// peering.Reciprocal validates, and it refused 0.0.0.0 — the value the page
// suggested for the one box that fed both roles. The caller tested only for
// err == nil, so the reply came back empty and the page rendered an empty box
// under "send this back" with no message. **A validation that exists and fires
// and is never seen is not a validation.**
func TestAReciprocalThatCannotBeBuiltIsReported(t *testing.T) {
	const passphrase = "a-passphrase-long-enough-to-be-accepted"
	srv, a, cm, _ := acceptHarness(t)

	body := fmt.Sprintf(`{"token":%q,"passphrase":%q,"name":"test","talkgroup":2,
		"timeslot":2,"listen":"0.0.0.0:0","address":"0.0.0.0:62045",
		"network_id":3132910,"confirm":true}`, anInvitation(t, passphrase), passphrase)

	rec := authed(t, srv, a, http.MethodPost, "/api/links/accept", body)
	if rec.Code == http.StatusOK {
		t.Fatalf("a bind address was accepted as somewhere the far end can reach: %s", rec.Body.String())
	}
	if len(cm.saved) != 0 {
		t.Error("a link was written for a peering whose reciprocal could not be built")
	}
}

// TestAMissingReplyAddressIsRefused. Left empty it produced an invitation with
// no address, which is a link the far end never reaches.
func TestAMissingReplyAddressIsRefused(t *testing.T) {
	const passphrase = "a-passphrase-long-enough-to-be-accepted"
	srv, a, _, _ := acceptHarness(t)

	body := fmt.Sprintf(`{"token":%q,"passphrase":%q,"name":"test","talkgroup":2,
		"timeslot":2,"listen":"0.0.0.0:0","address":"",
		"network_id":3132910,"confirm":true}`, anInvitation(t, passphrase), passphrase)

	rec := authed(t, srv, a, http.MethodPost, "/api/links/accept", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a peering with nowhere to reply to was accepted with %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "They send to us at") {
		t.Errorf("the refusal does not name the box to fill: %s", rec.Body.String())
	}
}

// TestAnEmptyListenAddressIsRefused. An upstream with no listen address binds
// every interface on a port the kernel chooses, which no far end was told
// about.
func TestAnEmptyListenAddressIsRefused(t *testing.T) {
	const passphrase = "a-passphrase-long-enough-to-be-accepted"
	srv, a, _, _ := acceptHarness(t)

	body := fmt.Sprintf(`{"token":%q,"passphrase":%q,"name":"test","talkgroup":2,
		"timeslot":2,"listen":"","address":"qsp.example.com:62045",
		"network_id":3132910,"confirm":true}`, anInvitation(t, passphrase), passphrase)

	rec := authed(t, srv, a, http.MethodPost, "/api/links/accept", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an empty listen address was accepted with %d", rec.Code)
	}
}
