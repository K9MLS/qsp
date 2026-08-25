package peers_test

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestDigestCannotBeReplayedFromAnotherAddress is the central spoofing
// protection.
//
// An observer of the login exchange sees the salt and the digest in the clear
// on UDP. Without an address check they could authenticate as that station.
func TestDigestCannotBeReplayedFromAnotherAddress(t *testing.T) {
	h := newHarness(t)
	h.send(hbp.Login{RepeaterID: testID}, addrA)

	// The correct digest, from the wrong address.
	out := h.send(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(salt, []byte(testPassword))}, addrB)
	if len(out.Responses) != 0 {
		t.Fatal("a valid digest was accepted from an address the challenge was not issued to")
	}
	if !strings.Contains(out.Dropped, "challenge was issued to") {
		t.Errorf("drop reason = %q", out.Dropped)
	}

	p, _ := h.m.Lookup(testID)
	if p.State != peers.StateChallenged {
		t.Errorf("the replay changed peer state to %s", p.State)
	}
}

// TestSpentSaltCannotBeReused ensures a captured digest is not indefinitely
// valid.
func TestSpentSaltCannotBeReused(t *testing.T) {
	h := newHarness(t)
	digest := hbp.Digest(salt, []byte(testPassword))

	h.send(hbp.Login{RepeaterID: testID}, addrA)
	if out := h.send(hbp.Key{RepeaterID: testID, Digest: digest}, addrA); out.Dropped != "" {
		t.Fatalf("first authentication failed: %s", out.Dropped)
	}

	// The same digest again, without a new challenge.
	out := h.send(hbp.Key{RepeaterID: testID, Digest: digest}, addrA)
	if len(out.Responses) != 0 {
		t.Fatal("a spent digest was accepted a second time")
	}
	if !strings.Contains(out.Dropped, "not challenged") {
		t.Errorf("drop reason = %q", out.Dropped)
	}
}

// TestTrafficIsRejectedFromAnUnregisteredAddress stops an attacker who knows a
// repeater ID from injecting frames.
//
// Radio IDs are public. Without this check, anyone could transmit as any
// station by sending a single UDP datagram.
func TestTrafficIsRejectedFromAnUnregisteredAddress(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	frame := hbp.Data{RepeaterID: testID, SourceID: uint32(testID), TargetID: 3100, Trailing: []byte{0, 0}}
	out := h.send(frame, addrB)
	if out.Data != nil {
		t.Fatal("a frame was accepted from an address the peer did not register from")
	}
	if !strings.Contains(out.Dropped, "registered from") {
		t.Errorf("drop reason = %q", out.Dropped)
	}
}

// TestKeepaliveFromAnotherAddressDoesNotHijackTheSession proves a spoofed
// keepalive cannot move a peer's address or keep a dead session alive.
func TestKeepaliveFromAnotherAddressDoesNotHijackTheSession(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	before, _ := h.m.Lookup(testID)
	h.c.advance(30 * time.Second)

	out := h.send(hbp.Ping{RepeaterID: testID}, addrB)
	if len(out.Responses) != 0 {
		t.Fatal("a keepalive from an unknown address was answered")
	}

	after, _ := h.m.Lookup(testID)
	if after.Addr != addrA {
		t.Errorf("the peer's address moved to %s", after.Addr)
	}
	if !after.LastHeard.Equal(before.LastHeard) {
		t.Error("a spoofed keepalive refreshed the peer's timeout")
	}
}

// TestUnauthenticatedLoginCannotDisplaceAWorkingPeer covers the denial-of-
// service shape: a stranger who knows a repeater ID sending RPTL repeatedly.
//
// The registration is re-challenged, but the peer's announced identity and its
// ability to be looked up survive until someone actually authenticates.
func TestUnauthenticatedLoginCannotDisplaceAWorkingPeer(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	// A stranger, from a different address, who cannot answer the challenge.
	out := h.send(hbp.Login{RepeaterID: testID}, addrB)
	if len(out.Responses) != 1 {
		t.Fatalf("re-login produced %d responses, want 1", len(out.Responses))
	}

	p, ok := h.m.Lookup(testID)
	if !ok {
		t.Fatal("the peer disappeared when a stranger sent a login request")
	}
	if p.Callsign() != "K9MLS" {
		t.Errorf("the peer's announced identity was lost: callsign = %q", p.Callsign())
	}
	if p.ConfiguredAt.IsZero() {
		t.Error("the peer's original registration time was lost")
	}
}

// TestNATRebindRequiresReauthentication documents and pins the policy in
// ADR-0011.
//
// A peer whose source address changes must complete the handshake again from
// the new address. That is a deliberate trade: a legitimate mobile peer suffers
// a brief outage, and an attacker cannot take over a session by spoofing a
// repeater ID.
func TestNATRebindRequiresReauthentication(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	// Traffic from the new address is refused until the peer logs in again.
	frame := hbp.Data{RepeaterID: testID, SourceID: uint32(testID), TargetID: 3100, Trailing: []byte{0, 0}}
	if out := h.send(frame, addrB); out.Data != nil {
		t.Fatal("traffic was accepted from a rebound address without re-authentication")
	}

	// A full handshake from the new address succeeds and moves the peer.
	h.login(addrB)

	p, ok := h.m.Lookup(testID)
	if !ok {
		t.Fatal("the peer is not registered after re-authenticating")
	}
	if p.Addr != addrB {
		t.Errorf("peer address = %s, want %s", p.Addr, addrB)
	}
	if out := h.send(frame, addrB); out.Data == nil {
		t.Errorf("traffic still refused after a successful re-login: %s", out.Dropped)
	}
	if h.m.Count() != 1 {
		t.Errorf("re-authentication left %d registrations, want 1", h.m.Count())
	}
}

// TestRepeaterIDZeroIsRefused rejects an obviously invalid station.
func TestRepeaterIDZeroIsRefused(t *testing.T) {
	h := newHarness(t, func(c *peers.MasterConfig) {
		c.Password = func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true }
	})
	out := h.send(hbp.Login{RepeaterID: 0}, addrA)
	if len(out.Responses) != 0 {
		t.Error("repeater ID 0 was issued a challenge")
	}
	if !strings.Contains(out.Dropped, "not a valid station") {
		t.Errorf("drop reason = %q", out.Dropped)
	}
}

// TestPeerLimitIsEnforced bounds memory against a flood of distinct IDs.
func TestPeerLimitIsEnforced(t *testing.T) {
	h := newHarness(t, func(c *peers.MasterConfig) {
		c.MaxPeers = 3
		c.Password = func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true }
	})
	for i := 1; i <= 3; i++ {
		if out := h.send(hbp.Login{RepeaterID: hbp.RepeaterID(i)}, addrA); out.Dropped != "" {
			t.Fatalf("peer %d refused below the limit: %s", i, out.Dropped)
		}
	}
	out := h.send(hbp.Login{RepeaterID: 4}, addrA)
	// Refused with MSTNAK rather than a challenge, so the peer stops retrying
	// blindly.
	if len(out.Responses) == 1 {
		if msg, err := hbp.Parse(out.Responses[0].Payload); err == nil {
			if _, isNak := msg.(hbp.Nak); !isNak {
				t.Errorf("a peer beyond the limit got %s, want MSTNAK", msg.Kind())
			}
		}
	}
	if !strings.Contains(out.Dropped, "peer limit") {
		t.Errorf("drop reason = %q", out.Dropped)
	}
	if h.m.Count() != 3 {
		t.Errorf("registry holds %d peers, want 3", h.m.Count())
	}

	// An already-registered peer may still re-login at the limit, or a reboot
	// would lock it out until its old registration expired.
	if out := h.send(hbp.Login{RepeaterID: 2}, addrA); out.Dropped != "" {
		t.Errorf("an existing peer was refused at the limit: %s", out.Dropped)
	}
}

// TestSilentPeerTimesOut covers the only way a peer currently leaves.
func TestSilentPeerTimesOut(t *testing.T) {
	h := newHarness(t, func(c *peers.MasterConfig) { c.PeerTimeout = 60 * time.Second })
	h.login(addrA)

	h.c.advance(59 * time.Second)
	if events := h.m.Expire(); len(events) != 0 {
		t.Fatalf("peer expired early: %+v", events)
	}

	h.c.advance(2 * time.Second)
	events := h.m.Expire()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Kind != peers.EventDisconnected {
		t.Errorf("event kind = %s, want disconnected", events[0].Kind)
	}
	if !strings.Contains(events[0].Reason, "no traffic") {
		t.Errorf("reason = %q", events[0].Reason)
	}
	if h.m.Count() != 0 {
		t.Error("the expired peer is still registered")
	}
}

// TestKeepaliveDefersTimeout proves the timeout is measured from last contact.
func TestKeepaliveDefersTimeout(t *testing.T) {
	h := newHarness(t, func(c *peers.MasterConfig) { c.PeerTimeout = 60 * time.Second })
	h.login(addrA)

	for i := 0; i < 10; i++ {
		h.c.advance(30 * time.Second)
		if out := h.send(hbp.Ping{RepeaterID: testID}, addrA); out.Dropped != "" {
			t.Fatalf("keepalive %d dropped: %s", i, out.Dropped)
		}
		if events := h.m.Expire(); len(events) != 0 {
			t.Fatalf("a peer sending keepalives expired: %+v", events)
		}
	}
	if h.m.ConfiguredCount() != 1 {
		t.Error("the peer is no longer registered after five minutes of keepalives")
	}
}

// TestIncompleteHandshakeExpiresSooner stops half-open registrations holding
// slots.
func TestIncompleteHandshakeExpiresSooner(t *testing.T) {
	h := newHarness(t, func(c *peers.MasterConfig) {
		c.LoginTimeout = 30 * time.Second
		c.PeerTimeout = 300 * time.Second
	})
	h.send(hbp.Login{RepeaterID: testID}, addrA)

	h.c.advance(31 * time.Second)
	events := h.m.Expire()
	if h.m.Count() != 0 {
		t.Error("an abandoned handshake was not cleaned up")
	}
	// No disconnected event: the peer was never connected, so announcing a
	// disconnection would be a lie.
	if len(events) != 0 {
		t.Errorf("an incomplete handshake emitted %d events, want 0: %+v", len(events), events)
	}
}

// TestExpireIsDeterministic keeps logs and tests reproducible.
func TestExpireIsDeterministic(t *testing.T) {
	h := newHarness(t, func(c *peers.MasterConfig) {
		c.Password = func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true }
		c.PeerTimeout = 10 * time.Second
	})
	for _, id := range []hbp.RepeaterID{300, 100, 200} {
		h.send(hbp.Login{RepeaterID: id}, addrA)
		h.send(hbp.Key{RepeaterID: id, Digest: hbp.Digest(salt, []byte(testPassword))}, addrA)
		h.send(hbp.Config{RepeaterID: id, Callsign: "TEST"}, addrA)
	}
	h.c.advance(time.Minute)

	events := h.m.Expire()
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	for i, want := range []hbp.RepeaterID{100, 200, 300} {
		if events[i].Peer.ID != want {
			t.Errorf("event %d is peer %d, want %d (events must be ordered)", i, events[i].Peer.ID, want)
		}
	}
}

// TestSnapshotsDoNotAliasRegistryState proves an observer cannot mutate the
// registry or watch it change underneath them.
func TestSnapshotsDoNotAliasRegistryState(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	snap, _ := h.m.Lookup(testID)
	snap.Config.Callsign = "TAMPERED"
	snap.State = peers.StateChallenged

	fresh, _ := h.m.Lookup(testID)
	if fresh.Callsign() != "K9MLS" {
		t.Errorf("registry callsign was changed through a snapshot: %q", fresh.Callsign())
	}
	if fresh.State != peers.StateConfigured {
		t.Errorf("registry state was changed through a snapshot: %s", fresh.State)
	}
}

// TestPeersSnapshotIsOrdered keeps console output stable.
func TestPeersSnapshotIsOrdered(t *testing.T) {
	h := newHarness(t, func(c *peers.MasterConfig) {
		c.Password = func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true }
	})
	for _, id := range []hbp.RepeaterID{500, 100, 300} {
		h.send(hbp.Login{RepeaterID: id}, addrA)
	}
	got := h.m.Peers()
	for i, want := range []hbp.RepeaterID{100, 300, 500} {
		if got[i].ID != want {
			t.Fatalf("Peers()[%d] = %d, want %d", i, got[i].ID, want)
		}
	}
}

// TestNewMasterRequiresAPasswordSource refuses a configuration that would
// authenticate nobody.
func TestNewMasterRequiresAPasswordSource(t *testing.T) {
	if _, err := peers.NewMaster(nil, peers.MasterConfig{}); err == nil {
		t.Error("NewMaster accepted a configuration with no password source")
	}
}
