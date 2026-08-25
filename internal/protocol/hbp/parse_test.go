package hbp_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestParseRejectsUncapturedKinds asserts that message types known to exist but
// never observed are refused precisely, rather than guessed at.
//
// RPTCL is the reason this matters: it shares its first four bytes with RPTC,
// so without an explicit guard a close message would be parsed as a truncated
// configuration and acted on as if a peer had reconfigured itself.
func TestParseRejectsUncapturedKinds(t *testing.T) {
	cases := map[string][]byte{
		"RPTO options": append([]byte("RPTO"), 0x00, 0x2f, 0xcd, 0xee),
		"RPTSBKN":      append([]byte("RPTSBKN"), 0x00, 0x2f, 0xcd, 0xee),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := hbp.Parse(in)
			if !errors.Is(err, hbp.ErrNotCaptured) {
				t.Fatalf("got %v, want ErrNotCaptured", err)
			}
			// The refusal must explain itself; what it needs differs per tag
			// (a capture, or a specification that defines the message).
			if len(err.Error()) < len("hbp: ")+20 {
				t.Errorf("error gives no explanation: %v", err)
			}
		})
	}
}

// TestRPTCLIsNotMistakenForRPTC guards a real misparse.
//
// RPTCL shares its first four bytes with RPTC. Without ordering the dispatch so
// that the longer tag is tested first, a peer's clean disconnect would be
// parsed as a configuration announcement and acted on as though the peer had
// reconfigured itself.
func TestRPTCLIsNotMistakenForRPTC(t *testing.T) {
	// A well-formed close.
	msg, err := hbp.Parse(hbp.RepeaterClose{RepeaterID: 3132910}.Marshal())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if msg.Kind() != hbp.KindRepeaterClose {
		t.Fatalf("RPTCL parsed as %s", msg.Kind())
	}

	// And a close padded to the length of a configuration must not satisfy
	// RPTC's length check.
	in := append([]byte("RPTCL"), bytes.Repeat([]byte{' '}, 297)...)
	if got, err := hbp.Parse(in); err == nil {
		t.Fatalf("a padded RPTCL was parsed as %s", got.Kind())
	}
}

// TestSpecifiedMessagesRoundTrip covers the messages derived from the protocol
// specification rather than from captured traffic.
//
// They have no fixture behind them, so a round-trip test is the strongest
// guarantee available until one exists.
func TestSpecifiedMessagesRoundTrip(t *testing.T) {
	const id = hbp.RepeaterID(3132910)
	cases := map[hbp.Kind]hbp.Message{
		hbp.KindNak:           hbp.Nak{RepeaterID: id},
		hbp.KindMasterAck:     hbp.MasterAck{RepeaterID: id},
		hbp.KindMasterClose:   hbp.MasterClose{RepeaterID: id},
		hbp.KindRepeaterClose: hbp.RepeaterClose{RepeaterID: id},
		hbp.KindMasterPing:    hbp.MasterPing{RepeaterID: id},
		hbp.KindRepeaterPong:  hbp.RepeaterPong{RepeaterID: id},
	}
	for kind, msg := range cases {
		t.Run(string(kind), func(t *testing.T) {
			wire := msg.Marshal()
			// Tag length plus a four-byte repeater ID.
			if want := len(kind) + 4; len(wire) != want {
				t.Fatalf("marshalled to %d bytes, want %d", len(wire), want)
			}
			if !bytes.HasPrefix(wire, []byte(kind)) {
				t.Fatalf("wire form does not begin with its tag: %q", wire)
			}

			got, err := hbp.Parse(wire)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got.Kind() != kind {
				t.Fatalf("round-tripped to %s", got.Kind())
			}
			if !bytes.Equal(got.Marshal(), wire) {
				t.Fatalf("re-marshal differs:\n got %x\nwant %x", got.Marshal(), wire)
			}
		})
	}
}

// TestPingDialectsAreDistinguished.
//
// The specification has the master polling (MSTPING) and the peer answering
// (RPTPONG); observed traffic runs the other way (RPTPING / MSTPONG). All four
// tags share prefixes, so dispatch order matters.
func TestPingDialectsAreDistinguished(t *testing.T) {
	const id = hbp.RepeaterID(3132910)
	for want, msg := range map[hbp.Kind]hbp.Message{
		hbp.KindPing:         hbp.Ping{RepeaterID: id},
		hbp.KindPong:         hbp.Pong{RepeaterID: id},
		hbp.KindMasterPing:   hbp.MasterPing{RepeaterID: id},
		hbp.KindRepeaterPong: hbp.RepeaterPong{RepeaterID: id},
	} {
		got, err := hbp.Parse(msg.Marshal())
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		if got.Kind() != want {
			t.Errorf("%s parsed as %s", want, got.Kind())
		}
	}
}

// TestParseRejectsTruncation walks every fixed-size message type from zero
// length up to one byte short of valid.
//
// Truncation is the most common malformed input on a UDP socket, whether from a
// buggy peer, a fragmented datagram, or an attacker.
//
// DMRD is excluded because it is genuinely variable-length: 53 bytes of protocol
// followed by an optional trailer. It gets its own test below, since "shorter
// than the full observed frame" is not the same thing as "truncated" for it.
func TestParseRejectsTruncation(t *testing.T) {
	fixedSize := map[string][]byte{
		"RPTL":    hbp.Login{RepeaterID: 3132910}.Marshal(),
		"RPTACK":  hbp.Ack{Payload: [4]byte{1, 2, 3, 4}}.Marshal(),
		"RPTK":    hbp.Key{RepeaterID: 3132910}.Marshal(),
		"RPTC":    hbp.Config{RepeaterID: 3132910, Callsign: "K9MLS"}.Marshal(),
		"RPTPING": hbp.Ping{RepeaterID: 3132910}.Marshal(),
		"MSTPONG": hbp.Pong{RepeaterID: 3132910}.Marshal(),
		"DMRC":    hbp.GatewayConfig{RepeaterID: 3132910, Callsign: "K9MLS"}.Marshal(),
		"DMRP":    hbp.GatewayPong{}.Marshal(),
		"MSTNAK":  hbp.Nak{RepeaterID: 3132910}.Marshal(),
		"MSTACK":  hbp.MasterAck{RepeaterID: 3132910}.Marshal(),
		"MSTCL":   hbp.MasterClose{RepeaterID: 3132910}.Marshal(),
		"RPTCL":   hbp.RepeaterClose{RepeaterID: 3132910}.Marshal(),
		"MSTPING": hbp.MasterPing{RepeaterID: 3132910}.Marshal(),
		"RPTPONG": hbp.RepeaterPong{RepeaterID: 3132910}.Marshal(),
	}
	for name, full := range fixedSize {
		t.Run(name, func(t *testing.T) {
			for n := 0; n < len(full); n++ {
				if _, err := hbp.Parse(full[:n]); err == nil {
					t.Errorf("a %d-byte truncation of %s (full length %d) was accepted", n, name, len(full))
				}
			}
		})
	}
}

// TestDMRDLengthBoundary pins the variable-length rule exactly.
//
// 53 bytes is the protocol minimum: header plus the 33-byte burst. Anything
// shorter loses part of the burst and must be rejected. Frames observed from
// MMDVMHost are 55 bytes, carrying two trailing bytes of link quality that this
// package preserves without interpreting.
func TestDMRDLengthBoundary(t *testing.T) {
	full := hbp.Data{SourceID: 3132910, Trailing: []byte{0x11, 0x22}}.Marshal()
	if len(full) != 55 {
		t.Fatalf("a frame with 2 trailing bytes marshalled to %d bytes, want 55", len(full))
	}

	for n := 0; n < 53; n++ {
		if _, err := hbp.Parse(full[:n]); err == nil {
			t.Errorf("a %d-byte DMRD was accepted; the burst would be incomplete", n)
		}
	}
	for n := 53; n <= 55; n++ {
		msg, err := hbp.Parse(full[:n])
		if err != nil {
			t.Errorf("a %d-byte DMRD was rejected: %v", n, err)
			continue
		}
		d := msg.(hbp.Data)
		if want := n - 53; len(d.Trailing) != want {
			t.Errorf("%d-byte DMRD has %d trailing bytes, want %d", n, len(d.Trailing), want)
		}
	}
}

// TestParseRejectsOverlongFixedSizeMessages ensures a padded message is not
// silently accepted, which would let an attacker smuggle bytes past a length
// check.
func TestParseRejectsOverlongFixedSizeMessages(t *testing.T) {
	for name, full := range map[string][]byte{
		"RPTL":    hbp.Login{RepeaterID: 1}.Marshal(),
		"RPTACK":  hbp.Ack{}.Marshal(),
		"RPTK":    hbp.Key{RepeaterID: 1}.Marshal(),
		"RPTPING": hbp.Ping{RepeaterID: 1}.Marshal(),
		"MSTPONG": hbp.Pong{RepeaterID: 1}.Marshal(),
		"RPTC":    hbp.Config{RepeaterID: 1}.Marshal(),
		"DMRC":    hbp.GatewayConfig{RepeaterID: 1}.Marshal(),
		"DMRP":    hbp.GatewayPong{}.Marshal(),
		"MSTNAK":  hbp.Nak{RepeaterID: 1}.Marshal(),
		"RPTCL":   hbp.RepeaterClose{RepeaterID: 1}.Marshal(),
		"MSTPING": hbp.MasterPing{RepeaterID: 1}.Marshal(),
	} {
		t.Run(name, func(t *testing.T) {
			padded := append(append([]byte(nil), full...), 0x00)
			if _, err := hbp.Parse(padded); !errors.Is(err, hbp.ErrTrailingBytes) {
				t.Errorf("got %v, want ErrTrailingBytes", err)
			}
		})
	}
}

// TestParseRejectsExcessiveDMRDTrailing bounds the variable-length part.
func TestParseRejectsExcessiveDMRDTrailing(t *testing.T) {
	d := hbp.Data{SourceID: 1}
	base := d.Marshal() // 53 bytes, no trailing

	if _, err := hbp.Parse(append(append([]byte(nil), base...), make([]byte, 8)...)); err != nil {
		t.Errorf("8 trailing bytes should be accepted, got: %v", err)
	}
	if _, err := hbp.Parse(append(append([]byte(nil), base...), make([]byte, 9)...)); !errors.Is(err, hbp.ErrTrailingBytes) {
		t.Errorf("9 trailing bytes: got %v, want ErrTrailingBytes", err)
	}
}

// TestParseRejectsUnknownTags checks the fallback path and that the error does
// not emit control characters into a log.
func TestParseRejectsUnknownTags(t *testing.T) {
	for _, in := range [][]byte{
		[]byte("XXXX"),
		{0x00, 0x01, 0x02, 0x03},
		{0xFF, 0xFF, 0xFF, 0xFF},
		[]byte("HTTP/1.1 200 OK"),
	} {
		_, err := hbp.Parse(in)
		if !errors.Is(err, hbp.ErrUnknownKind) {
			t.Errorf("input %q: got %v, want ErrUnknownKind", in, err)
		}
		for _, c := range err.Error() {
			if c < 0x20 && c != '\n' && c != '\t' {
				t.Errorf("error text contains a control character: %q", err.Error())
				break
			}
		}
	}
}

// TestParseEmptyAndTiny covers the degenerate inputs a socket can deliver.
func TestParseEmptyAndTiny(t *testing.T) {
	for n := 0; n < 4; n++ {
		if _, err := hbp.Parse(make([]byte, n)); !errors.Is(err, hbp.ErrShort) {
			t.Errorf("%d-byte input: got %v, want ErrShort", n, err)
		}
	}
	if _, err := hbp.Parse(nil); !errors.Is(err, hbp.ErrShort) {
		t.Errorf("nil input: got %v, want ErrShort", err)
	}
}

// TestDataFlagsRoundTripAcrossEveryCombination exercises the bit-packed byte
// exhaustively, since a mistake there silently misroutes calls.
func TestDataFlagsRoundTripAcrossEveryCombination(t *testing.T) {
	for _, ts := range []hbp.Timeslot{hbp.Timeslot1, hbp.Timeslot2} {
		for _, ct := range []hbp.CallType{hbp.CallGroup, hbp.CallPrivate} {
			for ft := 0; ft < 4; ft++ {
				for dt := 0; dt < 16; dt++ {
					in := hbp.Data{
						SourceID: 3132910, TargetID: 9990, RepeaterID: 3132910,
						Timeslot: ts, CallType: ct,
						FrameType: hbp.FrameType(ft), DataType: uint8(dt),
						StreamID: 0xdeadbeef, Trailing: []byte{0x11, 0x22},
					}
					wire := in.Marshal()
					msg, err := hbp.Parse(wire)
					if err != nil {
						t.Fatalf("%s/%s/ft=%d/dt=%d: %v", ts, ct, ft, dt, err)
					}
					got := msg.(hbp.Data)
					if got.Timeslot != ts || got.CallType != ct ||
						got.FrameType != hbp.FrameType(ft) || got.DataType != uint8(dt) {
						t.Fatalf("flags round-trip failed for %s/%s/ft=%d/dt=%d: got %+v", ts, ct, ft, dt, got)
					}
					if !bytes.Equal(got.Marshal(), wire) {
						t.Fatalf("re-marshal differs for %s/%s/ft=%d/dt=%d", ts, ct, ft, dt)
					}
				}
			}
		}
	}
}

// TestConfigFieldsAreTruncatedNotOverflowed ensures an over-long value cannot
// push subsequent fields out of position.
func TestConfigFieldsAreTruncatedNotOverflowed(t *testing.T) {
	c := hbp.Config{
		RepeaterID: 3132910,
		Callsign:   "THIS-CALLSIGN-IS-FAR-TOO-LONG",
		URL:        strings.Repeat("x", 500),
	}
	wire := c.Marshal()
	if len(wire) != 302 {
		t.Fatalf("over-long fields produced a %d-byte RPTC, want 302", len(wire))
	}
	msg, err := hbp.Parse(wire)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := msg.(hbp.Config)
	if got.Callsign != "THIS-CAL" {
		t.Errorf("callsign = %q, want it truncated to 8 bytes", got.Callsign)
	}
	if len(got.URL) != 124 {
		t.Errorf("URL length = %d, want 124", len(got.URL))
	}
	if got.RepeaterID != 3132910 {
		t.Errorf("repeater ID was corrupted by field overflow: %d", got.RepeaterID)
	}
}

// TestDigestKnownAnswer pins the authentication construction independently of
// the fixture.
func TestDigestKnownAnswer(t *testing.T) {
	salt := [4]byte{0x99, 0x47, 0xb4, 0x30}
	got := hbp.Digest(salt, []byte("qsp-test-password"))
	const want = "470eb0cf86ee3a0854f7ccbc20cb7373a16cea6cbd75d58f5a4518a7d596b4f6"

	if hexOf(got[:]) != want {
		t.Fatalf("digest = %s, want %s", hexOf(got[:]), want)
	}
	if !hbp.VerifyDigest(salt, []byte("qsp-test-password"), got) {
		t.Error("VerifyDigest rejected a digest it just produced")
	}
	if hbp.VerifyDigest(salt, []byte("wrong-password"), got) {
		t.Error("VerifyDigest accepted the wrong password")
	}
	if hbp.VerifyDigest([4]byte{0, 0, 0, 0}, []byte("qsp-test-password"), got) {
		t.Error("VerifyDigest accepted the wrong salt")
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}
