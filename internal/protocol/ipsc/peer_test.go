package ipsc_test

import (
	"testing"

	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// TestThePeerRegistrationIsTheCapturedBytes requires that what cmd/ipsc-peer
// puts on the wire is what an SLR5700 put on the wire, with only the sender ID
// changed.
//
// **The bytes below were read out of ipsc-phase2-registration.pcap**, from
// 203.0.113.60 (sender 0x0004d098) to an XPR8300 in master role at
// 192.168.1.233. They are the whole of the peer's vocabulary.
//
// This is the only assertion available about the tool. Most of these bytes have
// no known meaning, so nothing here can check that they are *correct* — only
// that they are unchanged. The master is the oracle for correctness: if it
// accepts the registration the bytes were good enough, and if it does not, the
// failure says which of them matters.
func TestThePeerRegistrationIsTheCapturedBytes(t *testing.T) {
	const senderID = 0x0004d098 // the SLR5700 that was captured

	for _, c := range []struct {
		kind ipsc.Kind
		what string
		raw  string
	}{
		{ipsc.KindRegisterRequest, "register request",
			"90" + "0004d098" + "66000080" + "4c040804" + "00"},
		{ipsc.KindKeepaliveRequest, "keepalive",
			"96" + "0004d098" + "66000080" + "4c040604" + "00"},
		{ipsc.KindF0, "the one-shot after the reply",
			"f0" + "0004d098" + "00000000"},
		{ipsc.Kind85, "the periodic 0x85",
			"85" + "0004d098" + "00000001" + "0102"},
	} {
		msg, ok := ipsc.PeerMessageFor(c.kind, senderID)
		if !ok {
			t.Errorf("%s: no captured peer body for kind %#02x", c.what, byte(c.kind))
			continue
		}
		got := hexOf(msg.Marshal())
		if got != c.raw {
			t.Errorf("%s (%#02x)\n got %s\nwant %s", c.what, byte(c.kind), got, c.raw)
		}
	}
}

// TestAPeerAndAMasterDoNotSendTheSameBytes guards a substitution that would
// look harmless and produce a program that cannot register.
//
// The first body byte is a property of the radio rather than of the protocol:
// 0x66 for the captured SLR5700 and 0x6a for the XPR8300. A tool that reached
// for CapturedBody instead of PeerBody would announce itself with the master's
// value, and IPSC's only refusal vocabulary is silence — so the failure would
// present as a master that never answers, with nothing to say why.
func TestAPeerAndAMasterDoNotSendTheSameBytes(t *testing.T) {
	peer, ok := ipsc.PeerBody(ipsc.KindKeepaliveRequest)
	if !ok {
		t.Fatal("no peer keepalive body")
	}
	master, ok := ipsc.CapturedBody(ipsc.KindKeepaliveReply)
	if !ok {
		t.Fatal("no master keepalive body")
	}
	if peer[0] == master[0] {
		t.Errorf("the peer and the master both open with %#02x; the captures "+
			"show 0x66 and 0x6a, and a peer sending the master's value is the "+
			"failure that presents as silence", peer[0])
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, v := range b {
		out = append(out, digits[v>>4], digits[v&0x0f])
	}
	return string(out)
}
