package ipsc

import "encoding/binary"

// capturedBody holds the body of each message a master was observed sending,
// taken verbatim from testdata/ipsc/ipsc-phase2-registration.pcap and
// ipsc-phase2-established.pcap.
//
// # Read this before trusting anything built from it
//
// These bytes came from **one** Motorola XPR8300 on firmware R02.30.20. Nine of
// the eleven body bytes of KindRegisterReply and thirty-nine of the forty-four
// of KindF1 have no known meaning, so a master built from them is not an
// implementation of IPSC: it is a recording of one repeater's answers with the
// sender ID substituted.
//
// **The first body byte is known to be wrong.** It is 0x6a for the XPR8300 and
// 0x66 for the other repeater captured, so it is a property of the device
// rather than of the protocol, and QSP has no way to know what its own value
// should be. It emits the XPR8300's.
//
// That is why this exists in a probe rather than in the server. The repeater is
// the only oracle: if it registers and stays registered, these bytes are good
// enough to be a master, and if it does not, the failure says which of them
// matters. Neither answer is available by reasoning about it.
var capturedBody = map[Kind][]byte{
	// 91 002fcdee | 6a 00 00 80 4d 00 00 04 06 04 00
	KindRegisterReply: {0x6a, 0x00, 0x00, 0x80, 0x4d, 0x00, 0x00, 0x04, 0x06, 0x04, 0x00},
	// 97 002fcdee | 6a 00 00 80 4d 04 06 04 00
	KindKeepaliveReply: {0x6a, 0x00, 0x00, 0x80, 0x4d, 0x04, 0x06, 0x04, 0x00},
	// 85 002fcdee | 00 00 00 01 01 02
	Kind85: {0x00, 0x00, 0x00, 0x01, 0x01, 0x02},
	// f1 002fcdee | thirty-nine bytes, of which sixteen look like entropy
	KindF1: {
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x1f,
		0x5d, 0x00, 0x00, 0x01, 0x00, 0x10, 0x00, 0x00,
		0x00, 0x00, 0x7f, 0x2d, 0x5b, 0xa4, 0xbd, 0x9e,
		0xe0, 0x3e, 0x4c, 0x1a, 0x82, 0xd4, 0x3c, 0x22,
		0x6a, 0xf0, 0xf5, 0x7d, 0x2c, 0x00, 0x00,
	},
}

// CapturedBody returns the body a master was observed sending for a kind.
func CapturedBody(k Kind) ([]byte, bool) {
	b, ok := capturedBody[k]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), b...), true
}

// Responder answers a peer the way the captured master answered one.
//
// It is an experiment, not an implementation. See capturedBody.
type Responder struct {
	// MasterID is the radio ID this master announces as its own. It is the
	// one field in every reply whose meaning is established.
	MasterID uint32
}

// Reply returns the messages the captured master sent in response to one from a
// peer, or nil when it sent nothing.
//
// The mapping is taken from the order observed in the registration capture:
// 0x90 drew 0x91, 0x96 drew 0x97, and 0xf0 drew 0xf1. Nothing was observed
// answering 0x85 within the fifteen minutes either side of the one the master
// sent, so nothing is sent for it.
func (r Responder) Reply(in Message) []Message {
	var kinds []Kind
	switch in.Kind {
	case KindRegisterRequest:
		kinds = []Kind{KindRegisterReply}
	case KindKeepaliveRequest:
		kinds = []Kind{KindKeepaliveReply}
	case KindF0:
		kinds = []Kind{KindF1}
	default:
		return nil
	}
	out := make([]Message, 0, len(kinds))
	for _, k := range kinds {
		body, ok := CapturedBody(k)
		if !ok {
			continue
		}
		out = append(out, Message{Kind: k, SenderID: r.MasterID, Body: body})
	}
	return out
}

// MessageFor builds a message of a kind the master was observed sending.
func MessageFor(k Kind, senderID uint32) (Message, bool) {
	body, ok := CapturedBody(k)
	if !ok {
		return Message{}, false
	}
	return Message{Kind: k, SenderID: senderID, Body: body}, true
}

// SenderIDOf reads the sender ID out of a raw datagram without parsing it,
// for logging a message this package refuses to accept.
func SenderIDOf(b []byte) (uint32, bool) {
	if len(b) < HeaderLen {
		return 0, false
	}
	return binary.BigEndian.Uint32(b[1:5]), true
}

// peerBody holds the body of each message a **peer** was observed sending,
// taken verbatim from testdata/ipsc/ipsc-phase2-registration.pcap.
//
// # The mirror of capturedBody, and it carries the same warning
//
// These came from one Motorola SLR5700 registering to an XPR8300 in master
// role. As with the master's bodies, most of these bytes have no known meaning,
// so a peer built from them is a recording of one repeater's requests with the
// sender ID substituted rather than an implementation of IPSC.
//
// **The first body byte is device-specific and known to differ.** It is 0x66
// here and 0x6a for the XPR8300, so it is a property of the radio rather than
// of the protocol. A tool replaying these emits the SLR5700's value, which is
// correct only in the sense that a real repeater once sent it.
//
// This exists so that a bench instrument can register to a real master and
// write down what the master sends — the one thing every capture in this
// repository cannot show, because all of them are the peer's half of the
// conversation. Under ADR-0043 QSP is never a peer in production; nothing here
// is wired into cmd/qsp, and it exists for the probe tools alone.
var peerBody = map[Kind][]byte{
	// 90 0004d098 | 66 00 00 80 4c 04 08 04 00
	KindRegisterRequest: {0x66, 0x00, 0x00, 0x80, 0x4c, 0x04, 0x08, 0x04, 0x00},
	// 96 0004d098 | 66 00 00 80 4c 04 06 04 00
	KindKeepaliveRequest: {0x66, 0x00, 0x00, 0x80, 0x4c, 0x04, 0x06, 0x04, 0x00},
	// f0 0004d098 | 00 00 00 00 — sent once, immediately after the reply
	KindF0: {0x00, 0x00, 0x00, 0x00},
	// 85 0004d098 | 00 00 00 01 01 02 — identical to the master's Kind85
	Kind85: {0x00, 0x00, 0x00, 0x01, 0x01, 0x02},
}

// PeerBody returns the body a peer was observed sending for a kind.
func PeerBody(k Kind) ([]byte, bool) {
	b, ok := peerBody[k]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), b...), true
}

// PeerMessageFor builds the message a peer sends for a kind, as that peer.
func PeerMessageFor(k Kind, senderID uint32) (Message, bool) {
	body, ok := PeerBody(k)
	if !ok {
		return Message{}, false
	}
	return Message{Kind: k, SenderID: senderID, Body: body}, true
}
