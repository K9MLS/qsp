package hbp

import "fmt"

// This file implements messages defined in the protocol specification but never
// observed in testdata/hbp/.
//
// # Provenance, and why it matters
//
// Everything else in this package was derived from captured traffic. These were
// derived from the published Homebrew repeater protocol specification (DL5DI,
// G4KLX, DG1HT, 2015-07-26), which the BrandMeister wiki reproduces.
//
// That distinction is recorded in the type documentation because the research
// pass that produced these also found **three places where the specification
// and real traffic disagree** — see docs/architecture/hbp-protocol.md. A
// documented format is a much better starting point than a guess, but it is not
// the same as evidence, and if one of these misbehaves against real hardware
// this file is where to look first.
//
// Each is a fixed-size message: a tag followed by a four-byte repeater ID.

// Wire sizes, from the specification.
const (
	nakSize           = 10 // "MSTNAK"  + id(4)
	masterAckSize     = 10 // "MSTACK"  + id(4)
	masterCloseSize   = 9  // "MSTCL"   + id(4)
	repeaterCloseSize = 9  // "RPTCL"   + id(4)
	masterPingSize    = 11 // "MSTPING" + id(4)
	repeaterPongSize  = 11 // "RPTPONG" + id(4)
)

// Additional message kinds, specified but not observed.
const (
	// KindNak is a master's rejection. Tag "MSTNAK".
	//
	// Specified as the reply to a login the master will not accept, and as the
	// reply to any packet from a peer the master considers dead — which tells
	// the peer to log in again.
	KindNak Kind = "MSTNAK"
	// KindMasterAck is the master's acknowledgement as the specification names
	// it. Tag "MSTACK".
	//
	// Real traffic uses RPTACK instead; see Ack. This exists so that a peer
	// following the specification is understood rather than rejected.
	KindMasterAck Kind = "MSTACK"
	// KindMasterClose is a master shutting down. Tag "MSTCL".
	KindMasterClose Kind = "MSTCL"
	// KindRepeaterClose is a peer disconnecting cleanly. Tag "RPTCL".
	//
	// Without this a departing peer is indistinguishable from one that lost
	// power until its timeout expires.
	KindRepeaterClose Kind = "RPTCL"
	// KindMasterPing is a master-initiated keepalive. Tag "MSTPING".
	//
	// The specification has the master polling and the peer answering. Observed
	// traffic runs the other way: the peer sends RPTPING and the master answers
	// MSTPONG. Both directions are supported.
	KindMasterPing Kind = "MSTPING"
	// KindRepeaterPong is a peer's reply to MSTPING. Tag "RPTPONG".
	KindRepeaterPong Kind = "RPTPONG"
)

// idMessage is the shape shared by every message in this file: a tag and a
// repeater ID.
type idMessage struct {
	tag  string
	kind Kind
	id   RepeaterID
}

func (m idMessage) marshal() []byte {
	dst := make([]byte, 0, len(m.tag)+4)
	dst = append(dst, m.tag...)
	var id [4]byte
	putID(id[:], m.id)
	return append(dst, id[:]...)
}

func (m idMessage) appendTo(dst []byte) []byte {
	dst = append(dst, m.tag...)
	var id [4]byte
	putID(id[:], m.id)
	return append(dst, id[:]...)
}

// parseIDMessage decodes a tag-plus-ID message of a known size.
func parseIDMessage(b []byte, tag string, size int, kind Kind) (RepeaterID, error) {
	if err := exactLen(b, size, kind); err != nil {
		return 0, err
	}
	return getID(b[len(tag):]), nil
}

// Nak is a master's rejection. Tag "MSTNAK".
//
// Specified but not observed; see the file comment.
type Nak struct{ RepeaterID RepeaterID }

// Kind implements Message.
func (Nak) Kind() Kind { return KindNak }

// Marshal implements Message.
func (m Nak) Marshal() []byte { return idMessage{"MSTNAK", KindNak, m.RepeaterID}.marshal() }

// AppendTo implements Message.
func (m Nak) AppendTo(dst []byte) []byte {
	return idMessage{"MSTNAK", KindNak, m.RepeaterID}.appendTo(dst)
}

func parseNak(b []byte) (Message, error) {
	id, err := parseIDMessage(b, "MSTNAK", nakSize, KindNak)
	if err != nil {
		return nil, err
	}
	return Nak{RepeaterID: id}, nil
}

// MasterAck is the acknowledgement as the specification names it. Tag "MSTACK".
//
// Observed masters send RPTACK instead. Accepting both means a peer written to
// the specification is understood.
type MasterAck struct{ RepeaterID RepeaterID }

// Kind implements Message.
func (MasterAck) Kind() Kind { return KindMasterAck }

// Marshal implements Message.
func (m MasterAck) Marshal() []byte {
	return idMessage{"MSTACK", KindMasterAck, m.RepeaterID}.marshal()
}

// AppendTo implements Message.
func (m MasterAck) AppendTo(dst []byte) []byte {
	return idMessage{"MSTACK", KindMasterAck, m.RepeaterID}.appendTo(dst)
}

func parseMasterAck(b []byte) (Message, error) {
	id, err := parseIDMessage(b, "MSTACK", masterAckSize, KindMasterAck)
	if err != nil {
		return nil, err
	}
	return MasterAck{RepeaterID: id}, nil
}

// MasterClose is a master shutting down. Tag "MSTCL".
type MasterClose struct{ RepeaterID RepeaterID }

// Kind implements Message.
func (MasterClose) Kind() Kind { return KindMasterClose }

// Marshal implements Message.
func (m MasterClose) Marshal() []byte {
	return idMessage{"MSTCL", KindMasterClose, m.RepeaterID}.marshal()
}

// AppendTo implements Message.
func (m MasterClose) AppendTo(dst []byte) []byte {
	return idMessage{"MSTCL", KindMasterClose, m.RepeaterID}.appendTo(dst)
}

func parseMasterClose(b []byte) (Message, error) {
	id, err := parseIDMessage(b, "MSTCL", masterCloseSize, KindMasterClose)
	if err != nil {
		return nil, err
	}
	return MasterClose{RepeaterID: id}, nil
}

// RepeaterClose is a peer disconnecting cleanly. Tag "RPTCL".
//
// This is the message whose absence meant a departing hotspot looked identical
// to one that lost power until its timeout expired.
type RepeaterClose struct{ RepeaterID RepeaterID }

// Kind implements Message.
func (RepeaterClose) Kind() Kind { return KindRepeaterClose }

// Marshal implements Message.
func (m RepeaterClose) Marshal() []byte {
	return idMessage{"RPTCL", KindRepeaterClose, m.RepeaterID}.marshal()
}

// AppendTo implements Message.
func (m RepeaterClose) AppendTo(dst []byte) []byte {
	return idMessage{"RPTCL", KindRepeaterClose, m.RepeaterID}.appendTo(dst)
}

func parseRepeaterClose(b []byte) (Message, error) {
	id, err := parseIDMessage(b, "RPTCL", repeaterCloseSize, KindRepeaterClose)
	if err != nil {
		return nil, err
	}
	return RepeaterClose{RepeaterID: id}, nil
}

// MasterPing is a master-initiated keepalive. Tag "MSTPING".
type MasterPing struct{ RepeaterID RepeaterID }

// Kind implements Message.
func (MasterPing) Kind() Kind { return KindMasterPing }

// Marshal implements Message.
func (m MasterPing) Marshal() []byte {
	return idMessage{"MSTPING", KindMasterPing, m.RepeaterID}.marshal()
}

// AppendTo implements Message.
func (m MasterPing) AppendTo(dst []byte) []byte {
	return idMessage{"MSTPING", KindMasterPing, m.RepeaterID}.appendTo(dst)
}

func parseMasterPing(b []byte) (Message, error) {
	id, err := parseIDMessage(b, "MSTPING", masterPingSize, KindMasterPing)
	if err != nil {
		return nil, err
	}
	return MasterPing{RepeaterID: id}, nil
}

// RepeaterPong is a peer's reply to MSTPING. Tag "RPTPONG".
type RepeaterPong struct{ RepeaterID RepeaterID }

// Kind implements Message.
func (RepeaterPong) Kind() Kind { return KindRepeaterPong }

// Marshal implements Message.
func (m RepeaterPong) Marshal() []byte {
	return idMessage{"RPTPONG", KindRepeaterPong, m.RepeaterID}.marshal()
}

// AppendTo implements Message.
func (m RepeaterPong) AppendTo(dst []byte) []byte {
	return idMessage{"RPTPONG", KindRepeaterPong, m.RepeaterID}.appendTo(dst)
}

func parseRepeaterPong(b []byte) (Message, error) {
	id, err := parseIDMessage(b, "RPTPONG", repeaterPongSize, KindRepeaterPong)
	if err != nil {
		return nil, err
	}
	return RepeaterPong{RepeaterID: id}, nil
}

// assert the sizes agree with their tags, so a typo fails at construction
// rather than on the wire.
func init() {
	for _, c := range []struct {
		tag  string
		size int
	}{
		{"MSTNAK", nakSize}, {"MSTACK", masterAckSize},
		{"MSTCL", masterCloseSize}, {"RPTCL", repeaterCloseSize},
		{"MSTPING", masterPingSize}, {"RPTPONG", repeaterPongSize},
	} {
		if len(c.tag)+4 != c.size {
			panic(fmt.Sprintf("hbp: %s should be %d bytes, not %d", c.tag, len(c.tag)+4, c.size))
		}
	}
}
