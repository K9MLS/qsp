package peers_test

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// Login throttling, through the master. The unit tests are in the internal
// test file; these are the properties an operator would notice.

// TestTheMasterStopsAnsweringAGuesser is the property that matters: forty
// attempts in six minutes is indistinguishable from somebody guessing the peer
// password, and QSP answered every one of them.
func TestTheMasterStopsAnsweringAGuesser(t *testing.T) {
	h := newHarness(t, func(c *peers.MasterConfig) {
		c.MaxLoginFailures = 3
		c.LoginLockout = time.Minute
	})

	// Three wrong passwords.
	for i := 0; i < 3; i++ {
		out := h.send(hbp.Login{RepeaterID: testID}, addrA)
		ack, err := hbp.Parse(out.Responses[0].Payload)
		if err != nil {
			t.Fatalf("challenge: %v", err)
		}
		wrong := hbp.Digest(ack.(hbp.Ack).Salt(), []byte("not-the-password"))
		h.send(hbp.Key{RepeaterID: testID, Digest: wrong}, addrA)
	}

	// The fourth login gets nothing at all — not even a challenge, which is
	// where a guesser would otherwise get a fresh salt.
	out := h.send(hbp.Login{RepeaterID: testID}, addrA)
	if len(out.Responses) != 0 {
		t.Errorf("a locked-out source was answered with %d responses", len(out.Responses))
	}
}

// TestAnHonestHotspotIsNotPunishedForever. The lockout must lift, or a member
// who mistypes their password is locked out until somebody restarts QSP.
func TestAnHonestHotspotIsNotPunishedForever(t *testing.T) {
	h := newHarness(t, func(c *peers.MasterConfig) {
		c.MaxLoginFailures = 2
		c.LoginLockout = time.Minute
	})

	for i := 0; i < 2; i++ {
		out := h.send(hbp.Login{RepeaterID: testID}, addrA)
		ack, _ := hbp.Parse(out.Responses[0].Payload)
		wrong := hbp.Digest(ack.(hbp.Ack).Salt(), []byte("wrong"))
		h.send(hbp.Key{RepeaterID: testID, Digest: wrong}, addrA)
	}
	if len(h.send(hbp.Login{RepeaterID: testID}, addrA).Responses) != 0 {
		t.Fatal("not locked out")
	}

	h.c.advance(2 * time.Minute)
	if len(h.send(hbp.Login{RepeaterID: testID}, addrA).Responses) == 0 {
		t.Error("the lockout did not lift; a mistyped password locks a member out for good")
	}
}

// TestARefusedLoginIsVisible. The operator found out about forty failures
// because the member messaged them.
func TestARefusedLoginIsVisible(t *testing.T) {
	h := newHarness(t)

	out := h.send(hbp.Login{RepeaterID: testID}, addrA)
	ack, _ := hbp.Parse(out.Responses[0].Payload)
	wrong := hbp.Digest(ack.(hbp.Ack).Salt(), []byte("wrong"))
	h.send(hbp.Key{RepeaterID: testID, Digest: wrong}, addrA)

	failures := h.m.LoginFailures(h.c.now())
	if len(failures) != 1 {
		t.Fatalf("%d refusals reported, want 1", len(failures))
	}
	if failures[0].RepeaterID != uint32(testID) {
		t.Errorf("the refusal names repeater %d", failures[0].RepeaterID)
	}
	if !strings.Contains(failures[0].Reason, "wrong password") {
		t.Errorf("the reason is %q", failures[0].Reason)
	}
}
