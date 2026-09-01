package ipsc_test

import (
	"bytes"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// TestTheResponderReproducesTheCapturedMasterExactly is what makes the probe an
// experiment rather than an invention.
//
// Every byte it sends must be a byte the captured master sent, apart from the
// sender ID. If this test fails, something has been made up.
func TestTheResponderReproducesTheCapturedMasterExactly(t *testing.T) {
	cap := readCapture(t, registration)
	const master = "192.168.1.233"

	byKind := map[ipsc.Kind][]byte{}
	for _, pkt := range cap.UDP {
		if pkt.SrcIP != master {
			continue
		}
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			t.Fatalf("packet %d: %v", pkt.Index, err)
		}
		byKind[msg.Kind] = msg.Body
	}
	if len(byKind) == 0 {
		t.Fatal("no master messages in the registration capture")
	}

	for kind, want := range byKind {
		got, ok := ipsc.CapturedBody(kind)
		if !ok {
			t.Errorf("kind %#02x was sent by the captured master but the responder has no body for it", byte(kind))
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("kind %#02x: responder body % x, captured % x", byte(kind), got, want)
		}
	}
}

// TestTheResponderAnswersWhatTheMasterAnswered pins the request-to-reply
// mapping to the order seen on the wire.
func TestTheResponderAnswersWhatTheMasterAnswered(t *testing.T) {
	r := ipsc.Responder{MasterID: masterID}
	for _, tc := range []struct {
		in   ipsc.Kind
		want ipsc.Kind
	}{
		{ipsc.KindRegisterRequest, ipsc.KindRegisterReply},
		{ipsc.KindKeepaliveRequest, ipsc.KindKeepaliveReply},
		{ipsc.KindF0, ipsc.KindF1},
	} {
		out := r.Reply(ipsc.Message{Kind: tc.in, SenderID: remotePeerID})
		if len(out) != 1 {
			t.Errorf("kind %#02x drew %d replies, want 1", byte(tc.in), len(out))
			continue
		}
		if out[0].Kind != tc.want {
			t.Errorf("kind %#02x drew %#02x, want %#02x", byte(tc.in), byte(out[0].Kind), byte(tc.want))
		}
		if out[0].SenderID != masterID {
			t.Errorf("reply carries sender ID %d, want the master's %d", out[0].SenderID, masterID)
		}
	}
}

// TestNothingIsSentForKind85 records that the captured master sent exactly one
// 0x85 in twenty-four minutes, and never in response to the peer's.
//
// Answering every one would be inventing behaviour; the probe stays silent
// because silence is what was observed.
func TestNothingIsSentForKind85(t *testing.T) {
	r := ipsc.Responder{MasterID: masterID}
	if out := r.Reply(ipsc.Message{Kind: ipsc.Kind85, SenderID: remotePeerID}); len(out) != 0 {
		t.Errorf("0x85 drew %d replies, want none", len(out))
	}
}

// TestTheRegisterReplyCarriesAForeignDeviceByte is a test that asserts a known
// defect, so that it cannot be forgotten.
//
// The first body byte is 0x6a on the XPR8300 and 0x66 on the other repeater
// captured, so it belongs to the device rather than the protocol. QSP has no
// idea what its own should be and emits an XPR8300's. Whether a repeater cares
// is exactly what the probe exists to find out.
func TestTheRegisterReplyCarriesAForeignDeviceByte(t *testing.T) {
	body, ok := ipsc.CapturedBody(ipsc.KindRegisterReply)
	if !ok {
		t.Fatal("no captured register reply body")
	}
	if body[0] != 0x6a {
		t.Errorf("first body byte is %#02x; the capture it was taken from has 0x6a, "+
			"and if this changed deliberately the reason belongs in a fixture", body[0])
	}
}

// TestF1CarriesNoPeerIdentity is the finding that killed the peer-list guess.
//
// KindF1 is the largest message in any capture and a peer list was the obvious
// reading. It contains neither the peer's radio ID in either byte order, nor
// either endpoint's IP address, nor either port. Whatever it is, it does not
// enumerate peers by identity — so replaying one master's is less reckless than
// it would be for a list, and the probe can test that cheaply.
func TestF1CarriesNoPeerIdentity(t *testing.T) {
	body, ok := ipsc.CapturedBody(ipsc.KindF1)
	if !ok {
		t.Fatal("no captured F1 body")
	}
	for _, tc := range []struct {
		name   string
		needle []byte
	}{
		{"peer radio ID, big endian", []byte{0x00, 0x04, 0xd0, 0x98}},
		{"peer radio ID, little endian", []byte{0x98, 0xd0, 0x04, 0x00}},
		{"peer IP 198.51.100.2", []byte{66, 188, 183, 60}},
		{"master IP 192.168.1.233", []byte{192, 168, 1, 233}},
	} {
		if bytes.Contains(body, tc.needle) {
			t.Errorf("F1 contains the %s; it may be a peer list after all, and replaying one is then wrong", tc.name)
		}
	}
}
