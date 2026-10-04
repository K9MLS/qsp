package v24link

import (
	"time"

	"github.com/k9mls/qsp/internal/protocol/p25"
)

// A repeater's calls arrive as HDLC information frames: an address, the
// control byte 03, and then one record. **From the record's first byte on,
// the voice records are the frames internal/protocol/p25 already reads** —
// the same type bytes, 62 to 73, at the same lengths — because the reflector
// protocol was made by carrying these bytes over UDP. So this file reads the
// wrapper and hands the rest to that package unchanged.
//
// Everything here is read off testdata/quantar/stun-voice-three-calls.bin:
// three transmissions from a handheld through a Quantar on 2026-10-04.

// controlUI is the control byte of an unnumbered information frame.
const controlUI = 0x03

// The records that are not voice.
const (
	// recordMarker opens and closes a transmission. Its fourth byte says
	// which: `00 02 02 0c 0b …` came before every transmission captured and
	// `00 02 02 25 0b …`, twice, after.
	recordMarker    = 0x00
	markerStart     = 0x0C
	markerEnd       = 0x25
	markerLength    = 10
	markerWhichByte = 3
	// recordHeader1 and recordHeader2 are the two halves of the transmission's
	// header. They are counted and carried, and not read: who is talking and
	// where is taken from the voice itself, which repeats it.
	recordHeader1       = 0x60
	recordHeader2       = 0x61
	recordHeader1Length = 30
	recordHeader2Length = 22
)

// RecordKind names what one information frame carries.
type RecordKind int

const (
	// RecordUnknown is a record this package cannot name.
	RecordUnknown RecordKind = iota
	// RecordStart opens a transmission.
	RecordStart
	// RecordEnd closes one.
	RecordEnd
	// RecordHeader is half of a transmission's header.
	RecordHeader
	// RecordVoice is one voice frame.
	RecordVoice
)

// Record is one information frame, read.
type Record struct {
	Kind RecordKind
	// Voice is the frame when Kind is RecordVoice, exactly as
	// internal/protocol/p25 parses it. **Its bytes are the repeater's own**,
	// IMBE included, and nothing here decodes them (ADR-0034).
	Voice p25.Frame
}

// ReadRecord reads a station's frame as an information frame. The second
// result is false when the frame is not one.
func ReadRecord(payload []byte) (Record, bool) {
	if len(payload) < 3 || payload[1] != controlUI {
		return Record{}, false
	}
	body := payload[2:]
	switch body[0] {
	case recordMarker:
		if len(body) != markerLength {
			return Record{Kind: RecordUnknown}, true
		}
		switch body[markerWhichByte] {
		case markerStart:
			return Record{Kind: RecordStart}, true
		case markerEnd:
			return Record{Kind: RecordEnd}, true
		}
	case recordHeader1:
		if len(body) == recordHeader1Length {
			return Record{Kind: RecordHeader}, true
		}
	case recordHeader2:
		if len(body) == recordHeader2Length {
			return Record{Kind: RecordHeader}, true
		}
	default:
		if f, err := p25.Parse(body); err == nil && f.Voice() {
			return Record{Kind: RecordVoice, Voice: f}, true
		}
	}
	return Record{Kind: RecordUnknown}, true
}

// CallTimeout ends a transmission whose end marker never came. Voice frames
// arrive every twenty milliseconds, so a second of nothing is a transmission
// that is over.
const CallTimeout = time.Second

// Call is one transmission heard from a repeater.
type Call struct {
	Started time.Time
	// Ended is zero while the transmission is in progress.
	Ended time.Time
	// Talkgroup and SourceID are zero until the voice has said them, which
	// takes up to two voice units: see linkControl.
	Talkgroup uint16
	SourceID  uint32
	// Frames is voice frames heard.
	Frames uint64
	// Marked reports that the repeater closed the transmission itself. False
	// is a transmission that went quiet and was closed by CallTimeout.
	Marked bool

	last time.Time
	lc   linkControl
}

// Duration is how long the transmission ran, or has run so far.
func (c Call) Duration(now time.Time) time.Duration {
	if !c.Ended.IsZero() {
		return c.Ended.Sub(c.Started)
	}
	return now.Sub(c.Started)
}

// linkControl follows one voice unit's link control across the three frames
// that carry it.
//
// **Only every other unit names the talker.** The link control word is nine
// bytes over frames 64, 65 and 66, and the repeater captured alternates two of
// them: the standard one — format 00, manufacturer 00, then the talkgroup and
// the radio — and one of Motorola's own, manufacturer 90, whose bytes in the
// same places are not a talkgroup and not a radio. Read without looking at
// frame 64 first, frame 66 of those units names radio 12251096, which is
// nobody, and a call that ends on one is recorded against it.
type linkControl struct {
	standard bool
}

const (
	lcFormatGroupVoice = 0x00
	lcManufacturerStd  = 0x00
)

// read takes one voice frame and returns the talkgroup or radio it names, if
// it is one that can be trusted to.
func (lc *linkControl) read(f p25.Frame) (talkgroup uint16, source uint32) {
	switch f.Kind {
	case p25.KindVoice1:
		lc.standard = false
	case p25.KindVoice3:
		lc.standard = len(f.Payload) >= 2 &&
			f.Payload[0] == lcFormatGroupVoice && f.Payload[1] == lcManufacturerStd
	case p25.KindVoice4:
		if tg, high, err := f.Talkgroup(); lc.standard && err == nil && high == 0 {
			return tg, 0
		}
	case p25.KindVoice5:
		if src, err := f.SourceID(); lc.standard && err == nil {
			return 0, src
		}
	}
	return 0, 0
}

// heard advances the transmission in progress by one record.
//
// It returns the transmission now in progress, or nil; the one this record
// finished, or nil; and whether the record began one.
func heard(call *Call, rec Record, now time.Time) (current, finished *Call, began bool) {
	switch rec.Kind {
	case RecordStart:
		if call != nil {
			// A start with one in progress: the last one's end was lost.
			call.Ended = call.last
			finished = call
		}
		return &Call{Started: now, last: now}, finished, true
	case RecordEnd:
		if call == nil {
			return nil, nil, false // the marker is sent twice
		}
		call.Ended, call.Marked = now, true
		return nil, call, false
	case RecordHeader, RecordVoice:
		if call == nil {
			// The link came up, or QSP started, in the middle of one.
			call, began = &Call{Started: now}, true
		}
		call.last = now
		if rec.Kind == RecordVoice {
			call.Frames++
			tg, src := call.lc.read(rec.Voice)
			if tg != 0 {
				call.Talkgroup = tg
			}
			if src != 0 {
				call.SourceID = src
			}
		}
		return call, nil, began
	}
	return call, nil, false
}

// stale reports a call whose end never came.
func (c *Call) stale(now time.Time) bool {
	return c != nil && now.Sub(c.last) > CallTimeout
}
