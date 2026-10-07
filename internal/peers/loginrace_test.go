package peers_test

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// freshSalts gives every challenge a salt of its own, as a running server
// does. With the harness's one fixed salt a digest made for one challenge
// answers every other, and nothing about which challenge is which can be
// tested.
func freshSalts(c *peers.MasterConfig) {
	var n uint32
	c.Salt = func() ([4]byte, error) {
		n++
		var s [4]byte
		binary.BigEndian.PutUint32(s[:], 0xA0000000+n)
		return s, nil
	}
}

func anyID(c *peers.MasterConfig) {
	c.Password = func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true }
}

// challenge sends a login request and returns the salt it was answered with.
func challenge(t *testing.T, h *harness, id hbp.RepeaterID, from netip.AddrPort) [4]byte {
	t.Helper()
	out := h.send(hbp.Login{RepeaterID: id}, from)
	if len(out.Responses) != 1 {
		t.Fatalf("a login from %s got %d answers (dropped: %q)", from, len(out.Responses), out.Dropped)
	}
	ack, ok := reply(t, out.Responses[0].Payload).(hbp.Ack)
	if !ok {
		t.Fatalf("a login from %s was refused", from)
	}
	return ack.Salt()
}

// answer sends the digest for a challenge and reports whether it was accepted.
func answer(t *testing.T, h *harness, id hbp.RepeaterID, from netip.AddrPort, issued [4]byte, password string) bool {
	t.Helper()
	out := h.send(hbp.Key{RepeaterID: id, Digest: hbp.Digest(issued, []byte(password))}, from)
	if len(out.Responses) != 1 {
		return false
	}
	_, ok := reply(t, out.Responses[0].Payload).(hbp.Ack)
	return ok
}

// TestAStrangerCannotStopAHotspotLoggingIn. A hotspot's login is two
// packets, and each row is something anybody can send for that hotspot's ID
// from another address before, between or after them. The hotspot's ID is on
// the public peer list.
//
// Before 0.1.333 the second login request for an ID replaced the first, and a
// wrong password deleted the entry whole, so the first four rows each left
// the hotspot answering a challenge QSP had forgotten.
//
// To see it fail: in handleLogin, go back to
// `if known && existing.State != StateChallenged {` (rows one to three); in
// handleKey, `delete(m.peers, msg.RepeaterID)` in place of m.abandon(p) (row
// four); drop `p.relogins = existing.relogins` from handleLogin (row five).
func TestAStrangerCannotStopAHotspotLoggingIn(t *testing.T) {
	hotspot, stranger := addrA, addrB

	tests := []struct {
		name string
		// run returns the salt the hotspot was challenged with.
		run func(t *testing.T, h *harness) [4]byte
	}{
		{"a login request for its ID from elsewhere, between its two packets",
			func(t *testing.T, h *harness) [4]byte {
				mine := challenge(t, h, testID, hotspot)
				challenge(t, h, testID, stranger)
				return mine
			}},
		{"the same, sent first",
			func(t *testing.T, h *harness) [4]byte {
				challenge(t, h, testID, stranger)
				return challenge(t, h, testID, hotspot)
			}},
		{"and a wrong password from there as well",
			func(t *testing.T, h *harness) [4]byte {
				mine := challenge(t, h, testID, hotspot)
				theirs := challenge(t, h, testID, stranger)
				if answer(t, h, testID, stranger, theirs, "a guess") {
					t.Fatal("a wrong password was accepted")
				}
				return mine
			}},
		{"the stranger first, and its wrong password before the hotspot answers",
			func(t *testing.T, h *harness) [4]byte {
				theirs := challenge(t, h, testID, stranger)
				mine := challenge(t, h, testID, hotspot)
				if answer(t, h, testID, stranger, theirs, "a guess") {
					t.Fatal("a wrong password was accepted")
				}
				return mine
			}},
		{"the stranger first, and asking again before the hotspot answers",
			func(t *testing.T, h *harness) [4]byte {
				challenge(t, h, testID, stranger)
				mine := challenge(t, h, testID, hotspot)
				challenge(t, h, testID, stranger)
				return mine
			}},
		{"the hotspot's own digest, copied and sent from elsewhere",
			func(t *testing.T, h *harness) [4]byte {
				mine := challenge(t, h, testID, hotspot)
				challenge(t, h, testID, stranger)
				if answer(t, h, testID, stranger, mine, testPassword) {
					t.Fatal("a digest for the hotspot's challenge was accepted from another address")
				}
				return mine
			}},
		{"twenty login requests from twenty addresses",
			func(t *testing.T, h *harness) [4]byte {
				mine := challenge(t, h, testID, hotspot)
				for i := range 20 {
					challenge(t, h, testID, netip.AddrPortFrom(
						netip.AddrFrom4([4]byte{203, 0, 113, byte(i + 1)}), 4000))
				}
				return mine
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, freshSalts)
			mine := tc.run(t, h)
			if !answer(t, h, testID, hotspot, mine, testPassword) {
				t.Fatal("the hotspot's own login, with the right password, was not accepted")
			}
			p, ok := h.m.Lookup(testID)
			if !ok || p.Addr != hotspot {
				t.Fatalf("registered: %v, at %s; want the hotspot's own address %s", ok, p.Addr, hotspot)
			}
			// And the stranger has gained nothing by it.
			out := h.send(hbp.Config{RepeaterID: testID, Callsign: "N0CALL"}, stranger)
			if len(out.Events) != 0 {
				t.Error("the stranger's address completed the registration")
			}
		})
	}
}

// TestTwoAddressesWithThePasswordAndTheLastToAnswerHasIt. A hotspot that
// loses power and comes back on a new address while its old login is still
// in progress is the honest case of two logins at once.
func TestTwoAddressesWithThePasswordAndTheLastToAnswerHasIt(t *testing.T) {
	h := newHarness(t, freshSalts)
	first := challenge(t, h, testID, addrA)
	second := challenge(t, h, testID, addrB)
	if !answer(t, h, testID, addrB, second, testPassword) {
		t.Fatal("the second address was not accepted")
	}
	if p, _ := h.m.Lookup(testID); p.Addr != addrB {
		t.Fatalf("registered at %s, want %s", p.Addr, addrB)
	}
	// The first challenge is spent with the login it belonged to.
	if answer(t, h, testID, addrA, first, testPassword) {
		t.Error("a challenge from before the login finished was still accepted")
	}
}

// TestForgedLoginsDoNotPushOutARealOne. Logins waiting on their challenge
// shared the peer limit, two hundred by default, and the oldest went when it
// was full: a few thousand forged login requests a second pushed a real
// hotspot's out between its two packets, every time it tried.
//
// To see it fail: in handleLogin, evict when `waiting >= m.cfg.MaxPeers`.
func TestForgedLoginsDoNotPushOutARealOne(t *testing.T) {
	tests := []struct {
		name   string
		forged int
		want   bool
	}{
		{"as many as the peer limit", 8, true},
		{"a thousand", 1000, true},
		// Past what is kept, the oldest does go: the table has to end somewhere.
		{"more than are kept at all", 5000, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, freshSalts, anyID, func(c *peers.MasterConfig) { c.MaxPeers = 8 })
			mine := challenge(t, h, testID, addrA)
			h.c.advance(time.Millisecond)
			for i := range tc.forged {
				h.send(hbp.Login{RepeaterID: hbp.RepeaterID(1000 + i)}, netip.AddrPortFrom(
					netip.AddrFrom4([4]byte{203, 0, byte(i >> 8), byte(i)}), 4000))
			}
			if got := answer(t, h, testID, addrA, mine, testPassword); got != tc.want {
				t.Errorf("the real login accepted: %v, want %v", got, tc.want)
			}
			if n := h.m.Count(); n > 4096+8 {
				t.Errorf("the registry holds %d entries; forged logins are not bounded", n)
			}
		})
	}
}

// TestThePeerLimitIsStationsThatLoggedIn, at the first packet and at the
// second: logins in progress together can finish together, and one of them
// is one too many.
//
// To see it fail: remove either `stations >= m.cfg.MaxPeers` test.
func TestThePeerLimitIsStationsThatLoggedIn(t *testing.T) {
	h := newHarness(t, freshSalts, anyID, func(c *peers.MasterConfig) { c.MaxPeers = 2 })
	at := func(n byte) netip.AddrPort {
		return netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, n}), 5000)
	}

	// Three begin while there is room for all of them to begin.
	salts := map[hbp.RepeaterID][4]byte{}
	for id := hbp.RepeaterID(1); id <= 3; id++ {
		salts[id] = challenge(t, h, id, at(byte(id)))
	}
	if !answer(t, h, 1, at(1), salts[1], testPassword) || !answer(t, h, 2, at(2), salts[2], testPassword) {
		t.Fatal("the first two were not accepted")
	}
	if answer(t, h, 3, at(3), salts[3], testPassword) {
		t.Error("a third station finished logging in past a limit of two")
	}
	// A fourth is refused at its first packet.
	out := h.send(hbp.Login{RepeaterID: 4}, at(4))
	if len(out.Responses) != 1 {
		t.Fatalf("a login past the limit got %d answers", len(out.Responses))
	}
	if _, nak := reply(t, out.Responses[0].Payload).(hbp.Nak); !nak {
		t.Error("a login past a limit of logged-in stations was not refused")
	}
	// One that is already in may log in again.
	again := challenge(t, h, 2, at(9))
	if !answer(t, h, 2, at(9), again, testPassword) {
		t.Error("a station already logged in was refused a new login at the limit")
	}
}
