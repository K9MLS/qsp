package peers_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// A login request for an ID that is registered and working must not disturb
// it. It did: handleLogin replaced the registration with a challenged one, so
// one RPTL from anybody took that hotspot off the air until it timed out and
// logged in again, and one every 25 seconds kept it off.
//
// Each case is what a stranger at another address can send without the
// password; after each, the working hotspot still talks, still gets its
// keepalive answered, and is still where it was.
//
// To see it fail: in handleLogin, remove the `known && existing.State !=
// StateChallenged` branch, so the challenged record replaces the working one.
func TestALoginRequestCannotTakeAWorkingPeerOffTheAir(t *testing.T) {
	wrong := func(s [4]byte) [hbp.DigestSize]byte { return hbp.Digest(s, []byte("not-the-password")) }
	cases := []struct {
		name     string
		stranger func(h *harness)
	}{
		{"a login request", func(h *harness) {
			h.send(hbp.Login{RepeaterID: testID}, addrB)
		}},
		{"a login request from the peer's own address, as a forged source would be", func(h *harness) {
			h.send(hbp.Login{RepeaterID: testID}, addrA)
		}},
		{"a login request and a wrong password", func(h *harness) {
			h.send(hbp.Login{RepeaterID: testID}, addrB)
			h.send(hbp.Key{RepeaterID: testID, Digest: wrong(salt)}, addrB)
		}},
		{"a digest nobody asked for", func(h *harness) {
			h.send(hbp.Key{RepeaterID: testID, Digest: wrong(salt)}, addrB)
		}},
		{"a login request every 25 seconds for five minutes", func(h *harness) {
			for range 12 {
				h.send(hbp.Login{RepeaterID: testID}, addrB)
				h.c.advance(25 * time.Second)
				// The hotspot's own keepalives go on arriving meanwhile.
				h.send(hbp.Ping{RepeaterID: testID}, addrA)
				h.m.Expire()
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.login(addrA)
			tc.stranger(h)

			if out := h.send(voice(3121001, 0x7001, 0), addrA); out.Data == nil {
				t.Errorf("the working hotspot's frame was refused: %s", out.Dropped)
			}
			out := h.send(hbp.Ping{RepeaterID: testID}, addrA)
			if len(out.Responses) != 1 {
				t.Fatalf("its keepalive got %d answers, want a pong (dropped: %q)", len(out.Responses), out.Dropped)
			}
			if _, isPong := reply(t, out.Responses[0].Payload).(hbp.Pong); !isPong {
				t.Errorf("its keepalive was answered with %T, want a pong", reply(t, out.Responses[0].Payload))
			}
			p, ok := h.m.Lookup(testID)
			if !ok || p.Addr != addrA || !p.State.CanPassTraffic() {
				t.Errorf("the registration is %+v (found %v), want configured at %s", p.State, ok, addrA)
			}
		})
	}
}

// The other half: a hotspot that really did restart, or whose address really
// did change, still gets back in at once with the password, and the
// registration moves to it.
//
// To see it fail: make handleReloginKey return before `p.Addr = from`.
func TestAPeerWithThePasswordStillLogsInAgain(t *testing.T) {
	for _, from := range []netip.AddrPort{addrA, addrB} {
		h := newHarness(t)
		h.login(addrA)
		h.login(from) // fatals if any step of the handshake is not answered
		p, ok := h.m.Lookup(testID)
		if !ok || p.Addr != from || !p.State.CanPassTraffic() {
			t.Fatalf("after logging in again from %s the registration is %s at %s", from, p.State, p.Addr)
		}
		frame := voice(3121001, 0x7002, 0)
		if out := h.send(frame, from); out.Data == nil {
			t.Errorf("a frame from %s after logging in again was refused: %s", from, out.Dropped)
		}
	}
}

// A challenge is answered once, and only from the address it was sent to.
//
// To see it fail: make takeRelogin return without removing the entry, and the
// replayed digest authenticates a second time.
func TestAReloginChallengeIsSpentAndBoundToItsAddress(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)
	good := hbp.Key{RepeaterID: testID, Digest: hbp.Digest(salt, []byte(testPassword))}

	h.send(hbp.Login{RepeaterID: testID}, addrB)
	if out := h.send(good, addrA); len(out.Responses) != 0 {
		t.Fatalf("the right digest from an address that was not challenged was answered")
	}
	if out := h.send(good, addrB); len(out.Responses) != 1 {
		t.Fatalf("the right digest from the challenged address was not accepted: %s", out.Dropped)
	}
	if out := h.send(good, addrB); len(out.Responses) != 0 {
		t.Errorf("the same digest was accepted a second time")
	}

	// And one left unanswered goes stale with the login timeout.
	h2 := newHarness(t)
	h2.login(addrA)
	h2.send(hbp.Login{RepeaterID: testID}, addrB)
	h2.c.advance(peers.DefaultLoginTimeout + time.Second)
	h2.send(hbp.Ping{RepeaterID: testID}, addrA)
	if out := h2.send(good, addrB); len(out.Responses) != 0 {
		t.Errorf("a challenge older than the login timeout was still accepted")
	}
}

// Login requests that never answer their challenge cannot fill the server.
// Two hundred of them used to hold every slot for thirty seconds at a time,
// and a real hotspot was told the peer limit was reached.
//
// To see it fail: remove `&& !m.evictHalfOpen()` from handleLogin.
func TestHalfOpenLoginsDoNotFillThePeerLimit(t *testing.T) {
	h := newHarness(t, func(c *peers.MasterConfig) {
		c.MaxPeers = 4
		c.Password = func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true }
	})
	for id := hbp.RepeaterID(100); id < 110; id++ {
		h.send(hbp.Login{RepeaterID: id}, netip.MustParseAddrPort("203.0.113.9:4000"))
		h.c.advance(time.Millisecond)
	}
	h.login(addrA) // fatals if refused

	// Stations that did log in are never evicted to make room.
	full := newHarness(t, func(c *peers.MasterConfig) { c.MaxPeers = 1 })
	full.login(addrA)
	out := full.send(hbp.Login{RepeaterID: testID + 1}, addrB)
	if len(out.Responses) != 1 {
		t.Fatalf("a login beyond the limit got %d answers, want a refusal", len(out.Responses))
	}
	if _, nak := reply(t, out.Responses[0].Payload).(hbp.Nak); !nak {
		t.Errorf("a login beyond a limit of registered stations was not refused")
	}
	if _, ok := full.m.Lookup(testID); !ok {
		t.Errorf("a registered station was evicted to make room")
	}
}

// Failures that need no reply from QSP to send can be sent with a forged
// source address, so they must not lock that address out. Six of them used to
// stop a member's real hotspot logging in for five minutes.
//
// To see it fail: make provesAddress return true for every reason.
func TestForgeableFailuresDoNotLockAnAddressOut(t *testing.T) {
	h := newHarness(t, func(c *peers.MasterConfig) { c.MaxLoginFailures = 3 })
	junk := hbp.Key{RepeaterID: testID, Digest: hbp.Digest(salt, []byte("x"))}
	for range 20 {
		h.send(junk, addrA) // unsolicited: nobody at addrA was challenged
	}
	if got := h.m.BlockedSources(h.c.now()); got != 0 {
		t.Fatalf("%d sources locked out by digests nobody asked for", got)
	}
	h.login(addrA) // the member's hotspot still gets in

	// Wrong passwords in answer to a challenge still count.
	g := newHarness(t, func(c *peers.MasterConfig) { c.MaxLoginFailures = 3 })
	for range 3 {
		g.send(hbp.Login{RepeaterID: testID}, addrB)
		g.send(junk, addrB)
	}
	if got := g.m.BlockedSources(g.c.now()); got != 1 {
		t.Errorf("%d sources locked out after three wrong passwords, want 1", got)
	}
}

func reply(t *testing.T, raw []byte) hbp.Message {
	t.Helper()
	m, err := hbp.Parse(raw)
	if err != nil {
		t.Fatalf("unparseable reply: %v", err)
	}
	return m
}

// A ban on a repeater that is connected takes effect when it is saved. The
// registration list was read only at login, so a banned repeater stayed on
// for as long as it kept pinging.
//
// To see it fail: remove the eviction loop from SetAccess.
func TestBanningAConnectedPeerRemovesItNow(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)
	ban, err := access.Parse("dmr.access.registration", access.Registration, access.ModeDeny, []string{"3132910"})
	if err != nil {
		t.Fatalf("access.Parse: %v", err)
	}
	h.m.SetAccess(access.Lists{Registration: ban})

	if _, ok := h.m.Lookup(testID); ok {
		t.Fatal("the banned peer is still registered")
	}
	if out := h.send(voice(3121001, 0x7100, 0), addrA); out.Data != nil {
		t.Error("the banned peer's frame was carried")
	}
	evs := h.m.Expire()
	if len(evs) != 1 || evs[0].Kind != peers.EventDisconnected || evs[0].Peer.ID != testID {
		t.Errorf("the console was told %+v, want one disconnection of the banned peer", evs)
	}
	if evs := h.m.Expire(); len(evs) != 0 {
		t.Errorf("the disconnection was reported twice")
	}
	out := h.send(hbp.Login{RepeaterID: testID}, addrA)
	if len(out.Responses) != 1 {
		t.Fatalf("its next login got %d answers, want a refusal", len(out.Responses))
	}
	if _, nak := reply(t, out.Responses[0].Payload).(hbp.Nak); !nak {
		t.Error("its next login was not refused")
	}

	// A list that does not name a peer leaves it alone.
	g := newHarness(t)
	g.login(addrA)
	other, _ := access.Parse("dmr.access.registration", access.Registration, access.ModeDeny, []string{"999999"})
	g.m.SetAccess(access.Lists{Registration: other})
	if _, ok := g.m.Lookup(testID); !ok {
		t.Error("a ban on another ID removed this peer")
	}
}
