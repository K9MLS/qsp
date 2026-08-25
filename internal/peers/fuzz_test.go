package peers_test

import (
	"net/netip"
	"testing"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// FuzzHandle asserts the properties that matter for code reading hostile UDP.
//
// Three invariants, in order of importance:
//
//  1. Handle never panics, whatever arrives.
//  2. An unauthenticated datagram never produces an accepted frame. This is the
//     one that keeps a stranger off the network.
//  3. Every discarded datagram carries an explanation, so an operator asking
//     "why won't my hotspot connect" is never met with silence.
func FuzzHandle(f *testing.F) {
	// Seed with real frames from the fixtures.
	for _, fixture := range []string{
		"../../testdata/hbp/hbp-login-session.pcap",
		"../../testdata/hbp/hbp-voice-session.pcap",
	} {
		for _, p := range readFixture(f, fixture) {
			f.Add(p)
		}
	}
	// Plus messages shaped correctly for this master's configured peer.
	f.Add(hbp.Login{RepeaterID: testID}.Marshal())
	f.Add(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(salt, []byte(testPassword))}.Marshal())
	f.Add(hbp.Config{RepeaterID: testID, Callsign: "K9MLS"}.Marshal())
	f.Add(hbp.Ping{RepeaterID: testID}.Marshal())
	f.Add([]byte{})
	f.Add([]byte("RPTCL\x00\x2f\xcd\xee"))

	from := netip.MustParseAddrPort("192.0.2.10:54663")

	f.Fuzz(func(t *testing.T, datagram []byte) {
		m, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
			Password: func(id hbp.RepeaterID) ([]byte, bool) {
				if id == testID {
					return []byte(testPassword), true
				}
				return nil, false
			},
			Salt: func() ([4]byte, error) { return salt, nil },
		})
		if err != nil {
			t.Fatalf("NewMaster: %v", err)
		}

		out := m.Handle(datagram, from)

		// A single datagram can never register a peer from nothing, so no
		// frame may be accepted.
		if out.Data != nil {
			t.Fatalf("a frame was accepted from an unregistered station: %x", datagram)
		}
		if m.ConfiguredCount() != 0 {
			t.Fatalf("a single datagram produced a fully registered peer: %x", datagram)
		}

		// Anything discarded must say why.
		acted := len(out.Responses) > 0 || len(out.Events) > 0 || out.Data != nil
		if !acted && out.Dropped == "" {
			t.Fatalf("datagram was discarded with no explanation: %x", datagram)
		}

		// Any response must itself be a valid HBP message; emitting something
		// unparseable would confuse the peer rather than help it.
		for _, r := range out.Responses {
			if _, err := hbp.Parse(r.Payload); err != nil {
				t.Fatalf("master emitted an unparseable response %x: %v", r.Payload, err)
			}
			if r.To != from {
				t.Fatalf("response addressed to %s, want the sender %s", r.To, from)
			}
		}

		// Expire must be safe in any state.
		_ = m.Expire()
		_ = m.Peers()
	})
}

// readFixture pulls UDP payloads out of a capture for the seed corpus.
func readFixture(f *testing.F, path string) [][]byte {
	f.Helper()
	packets, err := readCaptureRaw(path)
	if err != nil {
		f.Fatalf("%v", err)
	}
	out := make([][]byte, 0, len(packets))
	out = append(out, packets...)
	return out
}
