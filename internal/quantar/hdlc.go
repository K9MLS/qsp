package quantar

// The station's frames are HDLC: an address byte, then a control byte. The
// control values are the published standard's (ISO/IEC 13239), which is why
// they can be named here without a capture of each.
const (
	// pollFinal is the bit a request sets to demand an answer, and the answer
	// carries back.
	pollFinal = 0x10
	// controlSABM asks to open a link. The station sent it, with the poll bit,
	// twice a second for as long as nothing answered.
	controlSABM = 0x2F
	// controlUA accepts.
	controlUA = 0x63
)

// Kind names what a station's frame is, for the log.
type Kind string

const (
	// KindLinkRequest is the station asking to open the link.
	KindLinkRequest Kind = "link request"
	// KindUnknown is everything this package cannot yet name. It is recorded
	// and never answered.
	KindUnknown Kind = "unknown"
)

// Answer returns the reply a station's frame is owed, or nil.
//
// **One frame is answered and no other.** A link request is accepted, with
// the station's own address and its poll bit carried back. Anything else gets
// nothing: what the station sends once the link is open has not been captured,
// and a reply made up for it would be a guess the station might half accept.
func Answer(payload []byte) ([]byte, Kind) {
	if len(payload) != 2 {
		return nil, KindUnknown
	}
	address, control := payload[0], payload[1]
	if control&^pollFinal != controlSABM {
		return nil, KindUnknown
	}
	return []byte{address, controlUA | control&pollFinal}, KindLinkRequest
}
