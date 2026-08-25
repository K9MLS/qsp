package hbp_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

const (
	loginFixture = "../../../testdata/hbp/hbp-login-session.pcap"
	voiceFixture = "../../../testdata/hbp/hbp-voice-session.pcap"
)

// TestGoldenEveryFrameParses is the central claim of this package: every packet
// in the captured fixtures is understood.
//
// A parser that handles the frames it was written against but chokes on the
// rest of a real session is not finished, so this asserts on the whole file
// rather than on selected examples.
func TestGoldenEveryFrameParses(t *testing.T) {
	for _, fixture := range []string{loginFixture, voiceFixture} {
		t.Run(fixture, func(t *testing.T) {
			kinds := map[hbp.Kind]int{}
			for _, p := range readCapture(t, fixture) {
				msg, err := hbp.Parse(p.Payload)
				if err != nil {
					t.Fatalf("packet %d (%s, %d bytes): %v\n  bytes: %s",
						p.Index, p.Flow(), len(p.Payload), err, hex.EncodeToString(p.Payload))
				}
				kinds[msg.Kind()]++
			}
			t.Logf("parsed message kinds: %v", kinds)
			if len(kinds) == 0 {
				t.Fatal("no messages were parsed")
			}
		})
	}
}

// TestGoldenRoundTripsByteForByte proves the serialiser is the exact inverse of
// the parser.
//
// This is the property that makes QSP safe as a relay: a frame it forwards must
// be indistinguishable from the frame it received, including fields it does not
// interpret.
func TestGoldenRoundTripsByteForByte(t *testing.T) {
	for _, fixture := range []string{loginFixture, voiceFixture} {
		t.Run(fixture, func(t *testing.T) {
			for _, p := range readCapture(t, fixture) {
				msg, err := hbp.Parse(p.Payload)
				if err != nil {
					t.Fatalf("packet %d: %v", p.Index, err)
				}
				got := msg.Marshal()
				if !bytes.Equal(got, p.Payload) {
					t.Fatalf("packet %d (%s) did not round-trip:\n  original: %s\n  marshal:  %s",
						p.Index, msg.Kind(), hex.EncodeToString(p.Payload), hex.EncodeToString(got))
				}
				// AppendTo must agree with Marshal, including into a non-empty
				// buffer.
				prefix := []byte{0xde, 0xad}
				appended := msg.AppendTo(append([]byte(nil), prefix...))
				if !bytes.Equal(appended[len(prefix):], p.Payload) {
					t.Fatalf("packet %d: AppendTo disagrees with Marshal", p.Index)
				}
			}
		})
	}
}

// TestGoldenLoginSequence asserts the handshake observed on the master link,
// in order.
func TestGoldenLoginSequence(t *testing.T) {
	var seq []hbp.Kind
	var salt [4]byte
	var digest [hbp.DigestSize]byte

	for _, p := range readCapture(t, loginFixture) {
		msg, err := hbp.Parse(p.Payload)
		if err != nil {
			t.Fatalf("packet %d: %v", p.Index, err)
		}
		switch m := msg.(type) {
		case hbp.Login, hbp.Key, hbp.Config, hbp.Ack:
			seq = append(seq, msg.Kind())
			if a, ok := m.(hbp.Ack); ok && len(seq) == 2 {
				salt = a.Salt()
			}
			if k, ok := m.(hbp.Key); ok {
				digest = k.Digest
			}
		}
	}

	want := []hbp.Kind{
		hbp.KindLogin, hbp.KindAck,
		hbp.KindKey, hbp.KindAck,
		hbp.KindConfig, hbp.KindAck,
	}
	if len(seq) != len(want) {
		t.Fatalf("handshake has %d messages, want %d: %v", len(seq), len(want), seq)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Errorf("handshake[%d] = %s, want %s", i, seq[i], want[i])
		}
	}

	// The salt is documented in the fixture notes; assert it so that a
	// re-sanitised fixture cannot silently change the known-answer test below.
	if gotSalt := hex.EncodeToString(salt[:]); gotSalt != "9947b430" {
		t.Fatalf("salt = %s, want 9947b430 (see testdata/hbp/hbp-login-session.md)", gotSalt)
	}

	// The known-answer test for the authentication construction. If this
	// breaks, no peer will ever be able to connect to QSP.
	if !hbp.VerifyDigest(salt, []byte("qsp-test-password"), digest) {
		t.Fatalf("digest does not match SHA-256(salt || password)\n  got: %s",
			hex.EncodeToString(digest[:]))
	}
}

// TestGoldenAckIsAmbiguousByDesign records the fact that drives the Ack API:
// the same wire shape carries two different meanings.
func TestGoldenAckIsAmbiguousByDesign(t *testing.T) {
	var acks []hbp.Ack
	for _, p := range readCapture(t, loginFixture) {
		if msg, err := hbp.Parse(p.Payload); err == nil {
			if a, ok := msg.(hbp.Ack); ok {
				acks = append(acks, a)
			}
		}
	}
	if len(acks) != 3 {
		t.Fatalf("got %d RPTACK messages, want 3", len(acks))
	}

	// First: a salt. Second and third: the peer's own repeater ID echoed back.
	if hex.EncodeToString(acks[0].Payload[:]) != "9947b430" {
		t.Errorf("first ack payload = %x, want the salt", acks[0].Payload)
	}
	for i, a := range acks[1:] {
		if a.RepeaterID() != 3132910 {
			t.Errorf("ack %d repeater ID = %d, want 3132910", i+2, a.RepeaterID())
		}
	}
	// All three are byte-identical in shape; only state distinguishes them.
	for _, a := range acks {
		if len(a.Marshal()) != 10 {
			t.Errorf("RPTACK marshalled to %d bytes, want 10", len(a.Marshal()))
		}
	}
}

// TestGoldenBothDialectsPresent asserts that the fixture exercises the master
// link and the local gateway link, which encode configuration and keepalives
// differently.
func TestGoldenBothDialectsPresent(t *testing.T) {
	counts := map[hbp.Kind]int{}
	for _, p := range readCapture(t, loginFixture) {
		if msg, err := hbp.Parse(p.Payload); err == nil {
			counts[msg.Kind()]++
		}
	}
	for _, k := range []hbp.Kind{
		hbp.KindConfig, hbp.KindGatewayConfig,
		hbp.KindPing, hbp.KindPong, hbp.KindGatewayPong,
	} {
		if counts[k] == 0 {
			t.Errorf("fixture contains no %s messages", k)
		}
	}
}

// TestGoldenConfigFields checks decoded values against the documented capture.
func TestGoldenConfigFields(t *testing.T) {
	var cfg *hbp.Config
	var gw *hbp.GatewayConfig
	for _, p := range readCapture(t, loginFixture) {
		msg, err := hbp.Parse(p.Payload)
		if err != nil {
			continue
		}
		switch m := msg.(type) {
		case hbp.Config:
			if cfg == nil {
				c := m
				cfg = &c
			}
		case hbp.GatewayConfig:
			if gw == nil {
				g := m
				gw = &g
			}
		}
	}
	if cfg == nil || gw == nil {
		t.Fatal("fixture is missing RPTC or DMRC")
	}

	if cfg.Callsign != "K9MLS" {
		t.Errorf("RPTC callsign = %q, want K9MLS", cfg.Callsign)
	}
	if cfg.RXFreq != "449625000" || cfg.TXFreq != "444625000" {
		t.Errorf("RPTC frequencies = %q/%q", cfg.RXFreq, cfg.TXFreq)
	}
	if cfg.ColorCode != "11" {
		t.Errorf("RPTC color code = %q, want 11", cfg.ColorCode)
	}
	if cfg.RepeaterID != 3132910 {
		t.Errorf("RPTC repeater ID = %d, want 3132910", cfg.RepeaterID)
	}

	// The two dialects must agree on the fields they share.
	if gw.Callsign != cfg.Callsign || gw.RXFreq != cfg.RXFreq ||
		gw.TXFreq != cfg.TXFreq || gw.ColorCode != cfg.ColorCode {
		t.Errorf("DMRC and RPTC disagree on their shared fields:\n  DMRC: %+v\n  RPTC: %+v", gw, cfg)
	}
}
