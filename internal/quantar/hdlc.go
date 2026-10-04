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
	// controlXID is the introduction. The station sent it, with the poll bit,
	// 23 milliseconds after it was accepted.
	controlXID = 0xAF
	// controlRR is Receive Ready, the keepalive. Its top three bits are a
	// sequence number, which is zero on a link that numbers nothing.
	controlRR     = 0x01
	controlRRMask = 0x0F
)

// Identity is what QSP presents itself to the station as.
//
// **Two forms, because one sentence of evidence supports each.** The station
// is set for a repeater on the far end, and a repeater speaks as this one
// does: address FD, type C2. The only reply on public record that a Quantar
// accepted came from a Motorola console interface: address 0B, type 00, site
// 13. Which this station takes is for the station to say, so both are here and
// an operator can change between them without a new build.
type Identity struct {
	// Name is how the setting is written.
	Name string
	// Address opens every frame QSP originates.
	Address byte
	// StationType is the type byte in QSP's introduction.
	StationType byte
	// DefaultSite is the site number used when none is set.
	DefaultSite uint8
}

var (
	// Repeater is QSP presenting as a second Quantar.
	Repeater = Identity{Name: "repeater", Address: 0xFD, StationType: 0xC2, DefaultSite: 2}
	// Console is QSP presenting as a console interface.
	Console = Identity{Name: "console", Address: 0x0B, StationType: 0x00, DefaultSite: 13}
)

// IdentityNamed returns the identity a setting names. Empty is Repeater.
func IdentityNamed(name string) (Identity, bool) {
	switch name {
	case "", Repeater.Name:
		return Repeater, true
	case Console.Name:
		return Console, true
	}
	return Identity{}, false
}

// MaxSite is the largest site number an introduction can carry: the byte
// holds twice the number, plus one.
const MaxSite = 127

// Kind names what a station's frame is.
type Kind string

const (
	// KindLinkRequest is the station asking to open the link.
	KindLinkRequest Kind = "link request"
	// KindAcceptance is the station accepting a link request of QSP's.
	KindAcceptance Kind = "acceptance"
	// KindIntroduction is the station saying what it is and which site.
	KindIntroduction Kind = "introduction"
	// KindReceiveReady is the station's keepalive.
	KindReceiveReady Kind = "receive ready"
	// KindUnknown is everything this package cannot yet name. It is recorded
	// and never answered.
	KindUnknown Kind = "unknown"
)

// Classify names a station's frame.
func Classify(payload []byte) Kind {
	if len(payload) < 2 {
		return KindUnknown
	}
	control := payload[1]
	switch {
	case len(payload) == 2 && control&^pollFinal == controlSABM:
		return KindLinkRequest
	case len(payload) == 2 && control&^pollFinal == controlUA:
		return KindAcceptance
	case len(payload) == 10 && control&^pollFinal == controlXID && payload[2] == 0x01:
		return KindIntroduction
	case len(payload) == 2 && control&controlRRMask == controlRR:
		return KindReceiveReady
	}
	return KindUnknown
}

// Answer returns the reply a station's frame is owed whatever the link's
// state, or nil, and what the frame was.
//
// A link request is accepted, with the station's own address and its poll bit
// carried back. A keepalive that demands an answer gets one; a keepalive that
// does not is only noted, because answering every keepalive with a keepalive
// would have two ends answering each other for ever.
//
// **An introduction is not answered here.** Whether it is owed one depends on
// how far the link has come, which the listener knows and one frame does not.
//
// Anything else gets nothing. Voice has not been captured, and a reply made up
// for it would be a guess the station might half accept.
func Answer(payload []byte) ([]byte, Kind) {
	kind := Classify(payload)
	switch kind {
	case KindLinkRequest:
		return []byte{payload[0], controlUA | payload[1]&pollFinal}, kind
	case KindReceiveReady:
		if payload[1]&pollFinal != 0 {
			return []byte{payload[0], controlRR | pollFinal}, kind
		}
	}
	return nil, kind
}

// LinkRequest is QSP asking the station to open the link from QSP's side.
//
// **The link is opened from both ends.** 0.1.304 accepted the station's
// request, answered its introduction, and was ignored: the station repeated
// itself as though nothing had arrived, 65 times. The published account says
// a station whose request is accepted "sends a single UA frame back", and a
// UA answers only a request — so the far end in that account was asking too.
// This is that request, in the station's own form.
func (id Identity) LinkRequest() []byte {
	return []byte{id.Address, controlSABM | pollFinal}
}

// Introduction is QSP saying what it is: message type 1, twice the site
// number plus one, the station type, and the five bytes every introduction
// seen or published ends with.
func (id Identity) Introduction(site uint8) []byte {
	return []byte{id.Address, controlXID | pollFinal, 0x01, site*2 + 1, id.StationType,
		0x00, 0x00, 0x00, 0x00, 0xFF}
}

// Keepalive is the Receive Ready frame QSP sends unasked.
func (id Identity) Keepalive() []byte { return []byte{id.Address, controlRR} }
