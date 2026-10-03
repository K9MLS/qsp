package hbp

import "fmt"

// KindTalkerAlias is a peer reporting the callsign a radio is sending inside
// its voice. Tag "DMRA".
const KindTalkerAlias Kind = "DMRA"

// Bounds on a talker alias datagram. The tag and the repeater ID are the
// least it can be; the most is generous, since no sender seen comes near it.
const (
	talkerAliasMin = 8
	talkerAliasMax = 64
)

// TalkerAlias is a peer passing on a block of Talker Alias it decoded from a
// radio. Tag "DMRA".
//
// **Read only as far as it has been seen.** A Pi-Star was observed sending
// these on 2026-10-03, six beside two hundred voice frames, whenever a radio
// with Talker Alias switched on keyed up. Nothing in QSP handled the tag, so
// each was counted as ignored traffic from the operator's own hotspot. The
// senders disagree about the rest: MMDVMHost writes the radio ID, a block
// number and seven octets of text; DMRGateway puts the repeater ID in front
// and, as written, sends only the first fifteen octets of the nineteen. So
// the repeater ID is read, because it says who sent the datagram, and the
// rest is kept exactly as it arrived.
//
// QSP does not relay it. Neither MMDVMHost nor DMRGateway reads a "DMRA" from
// a master, and the alias already travels to other radios inside the voice
// frames, which QSP passes on untouched.
type TalkerAlias struct {
	RepeaterID RepeaterID
	// Rest is everything after the repeater ID, as sent.
	Rest []byte
}

// Kind implements Message.
func (TalkerAlias) Kind() Kind { return KindTalkerAlias }

// Marshal implements Message.
func (m TalkerAlias) Marshal() []byte {
	return m.AppendTo(make([]byte, 0, talkerAliasMin+len(m.Rest)))
}

// AppendTo implements Message.
func (m TalkerAlias) AppendTo(dst []byte) []byte {
	dst = append(dst, "DMRA"...)
	var id [4]byte
	putID(id[:], m.RepeaterID)
	dst = append(dst, id[:]...)
	return append(dst, m.Rest...)
}

func parseTalkerAlias(b []byte) (Message, error) {
	if len(b) < talkerAliasMin {
		return nil, fmt.Errorf("%w: a %s of %d bytes, need at least %d", ErrShort, KindTalkerAlias, len(b), talkerAliasMin)
	}
	if len(b) > talkerAliasMax {
		return nil, fmt.Errorf("%w: a %s of %d bytes, at most %d", ErrTrailingBytes, KindTalkerAlias, len(b), talkerAliasMax)
	}
	// Copied: Parse never keeps the caller's slice.
	return TalkerAlias{RepeaterID: getID(b[4:8]), Rest: append([]byte(nil), b[8:]...)}, nil
}
