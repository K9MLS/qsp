package peers

import (
	"math/rand/v2"
	"net/netip"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// recount is how many logins are waiting on their challenge, by going
// through the registry: what Master.waiting is kept to avoid doing.
func (m *Master) recount() int {
	n := 0
	for _, p := range m.peers {
		if p.State == StateChallenged {
			n++
		}
	}
	return n
}

// TestTheCountOfHalfOpenLoginsIsKeptRight. The count is changed in six
// places and read on every login request. Wrong low, and the peer limit
// lets too many in; wrong high, and it refuses a server that has room.
//
// Thirty thousand packets of every kind a stranger or a hotspot can send, in
// an order nobody chose, with the count compared against the registry after
// each one.
//
// To see it fail: remove `m.waiting--` from handleKey or handleReloginKey,
// or use delete(m.peers, ...) in place of m.forget anywhere.
func TestTheCountOfHalfOpenLoginsIsKeptRight(t *testing.T) {
	for _, limit := range []int{3, 50} {
		now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
		var salts uint32
		last := map[[2]int][4]byte{} // the salt last issued to (id, address)
		m, err := NewMaster(logging.Discard(), MasterConfig{
			MaxPeers:     limit,
			LoginTimeout: 5 * time.Second,
			PeerTimeout:  20 * time.Second,
			Password: func(id hbp.RepeaterID) ([]byte, bool) {
				return []byte("secret"), id%7 != 0 // some IDs have no password
			},
			Now: func() time.Time { return now },
			Salt: func() ([4]byte, error) {
				salts++
				return [4]byte{byte(salts >> 24), byte(salts >> 16), byte(salts >> 8), byte(salts)}, nil
			},
		})
		if err != nil {
			t.Fatalf("NewMaster: %v", err)
		}
		rng := rand.New(rand.NewPCG(20261007, uint64(limit)))
		addr := func(n int) netip.AddrPort {
			return netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, byte(n + 1)}), 5000)
		}

		for step := range 30000 {
			id, a := 1+rng.IntN(40), rng.IntN(5)
			rid, from := hbp.RepeaterID(id), addr(a)
			var msg hbp.Message
			switch rng.IntN(10) {
			case 0, 1, 2:
				msg = hbp.Login{RepeaterID: rid}
			case 3, 4, 5: // the right answer to the last challenge sent there
				msg = hbp.Key{RepeaterID: rid, Digest: hbp.Digest(last[[2]int{id, a}], []byte("secret"))}
			case 6:
				msg = hbp.Key{RepeaterID: rid, Digest: hbp.Digest([4]byte{1}, []byte("a guess"))}
			case 7:
				msg = hbp.Config{RepeaterID: rid, Callsign: "N0CALL", ColorCode: "1"}
			case 8:
				msg = hbp.RepeaterClose{RepeaterID: rid}
			case 9:
				now = now.Add(time.Duration(rng.IntN(3000)) * time.Millisecond)
				m.Expire()
			}
			if msg != nil {
				out := m.Handle(msg.Marshal(), from)
				if _, isLogin := msg.(hbp.Login); isLogin && len(out.Responses) == 1 {
					if reply, err := hbp.Parse(out.Responses[0].Payload); err == nil {
						if ack, ok := reply.(hbp.Ack); ok {
							last[[2]int{id, a}] = ack.Salt()
						}
					}
				}
			}

			m.mu.Lock()
			kept, counted, stations := m.waiting, m.recount(), m.stations()
			listed := len(m.halfOpen)
			m.mu.Unlock()
			if kept != counted {
				t.Fatalf("limit %d, step %d (%T): the count says %d logins are waiting and the registry holds %d",
					limit, step, msg, kept, counted)
			}
			if stations > limit {
				t.Fatalf("limit %d, step %d: %d stations are logged in", limit, step, stations)
			}
			if listed > 4*maxHalfOpen+1 {
				t.Fatalf("limit %d, step %d: the list of half-open logins has grown to %d", limit, step, listed)
			}
		}
	}
}

// TestEvictionTakesTheOldestLoginStillWaiting, and passes over the ones the
// list still names that have since finished or gone.
//
// To see it fail: in evictHalfOpen, forget the first one listed without
// asking whether it is still waiting.
func TestEvictionTakesTheOldestLoginStillWaiting(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
	var issued [4]byte
	m, err := NewMaster(logging.Discard(), MasterConfig{
		MaxPeers: 10,
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte("secret"), true },
		Now:      func() time.Time { return now },
		Salt:     func() ([4]byte, error) { issued[3]++; return issued, nil },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}
	at := netip.MustParseAddrPort("192.0.2.1:5000")
	for id := hbp.RepeaterID(1); id <= 4; id++ {
		m.Handle(hbp.Login{RepeaterID: id}.Marshal(), at)
		now = now.Add(time.Second)
	}
	// The oldest finishes logging in; the second is given up on.
	m.Handle(hbp.Key{RepeaterID: 1, Digest: hbp.Digest([4]byte{0, 0, 0, 1}, []byte("secret"))}.Marshal(), at)
	m.Handle(hbp.Key{RepeaterID: 2, Digest: hbp.Digest([4]byte{9}, []byte("wrong"))}.Marshal(), at)

	m.mu.Lock()
	evicted := m.evictHalfOpen()
	_, one := m.peers[1]
	_, three := m.peers[3]
	_, four := m.peers[4]
	m.mu.Unlock()
	if !evicted || !one || three || !four {
		t.Errorf("evicted: %v; logged-in station kept: %v, oldest waiting login kept: %v, newest kept: %v; "+
			"want the oldest still waiting, and only it, to go", evicted, one, three, four)
	}
}
