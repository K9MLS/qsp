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

// stationQuantar is the type a Quantar gives for itself in its introduction.
const stationQuantar = 0xC2

// DefaultSite is the site number QSP introduces itself with when none is
// set. The station captured is site 1, the codeplug's own default, so this is
// the first number that is not that.
const DefaultSite = 2

// MaxSite is the largest site number an introduction can carry: the byte
// holds twice the number, plus one.
const MaxSite = 127

// Kind names what a station's frame is.
type Kind string

const (
	// KindLinkRequest is the station asking to open the link.
	KindLinkRequest Kind = "link request"
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
	case len(payload) == 10 && control&^pollFinal == controlXID && payload[2] == 0x01:
		return KindIntroduction
	case len(payload) == 2 && control&controlRRMask == controlRR:
		return KindReceiveReady
	}
	return KindUnknown
}

// Answer returns the reply a station's frame is owed, or nil, and what the
// frame was.
//
// **Three frames are answered and no other.**
//
// A link request is accepted, with the station's own address and its poll bit
// carried back.
//
// An introduction is answered with QSP's own, in the shape the station used:
// message type 1, twice the site number plus one, the Quantar type, and the
// station's last five bytes returned as they came. The station is set for a
// repeater on the far end (`RT/RT Configuration`), so that is what QSP says it
// is. **Unanswered, the station repeats its introduction three times and
// starts again from the link request, every 1.55 seconds** — which is what
// 0.1.303 saw 97 times in a row.
//
// A keepalive that demands an answer gets one. A keepalive that does not is
// only noted: answering every keepalive with a keepalive would have two ends
// answering each other for ever.
//
// Anything else gets nothing. Voice has not been captured, and a reply made up
// for it would be a guess the station might half accept.
func Answer(payload []byte, site uint8) ([]byte, Kind) {
	kind := Classify(payload)
	switch kind {
	case KindLinkRequest:
		return []byte{payload[0], controlUA | payload[1]&pollFinal}, kind
	case KindIntroduction:
		out := make([]byte, 10)
		copy(out, payload)
		out[3] = site*2 + 1
		out[4] = stationQuantar
		return out, kind
	case KindReceiveReady:
		if payload[1]&pollFinal != 0 {
			return []byte{payload[0], controlRR | pollFinal}, kind
		}
	}
	return nil, kind
}

// Keepalive is the Receive Ready frame QSP sends unasked.
func Keepalive(address byte) []byte { return []byte{address, controlRR} }
