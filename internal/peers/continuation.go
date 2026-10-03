package peers

import (
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// openCall is the voice transmission a peer is sending on one timeslot, as it
// was when it began.
type openCall struct {
	stream   hbp.StreamID
	source   uint32
	target   uint32
	callType hbp.CallType
	lastSeen time.Time
	// open is true from the first frame until the terminator.
	open bool
	// adopted is a second stream ID the peer began using for this same
	// transmission, and adopting says there is one.
	adopted  hbp.StreamID
	adopting bool
}

// continuing returns f as part of the call it belongs to, and whether it had
// to be put back into one.
//
// **A hotspot that loses a radio for an instant starts again as if it were a
// new call**, with a new stream ID and no voice header, because it did not
// hear one: it is picking the transmission up from the middle. Two things
// went wrong with that. Routing saw a second transmission from the peer
// already holding the timeslot and refused it as a collision until the first
// timed out, two seconds on. And some hotspot software rebuilds the caller
// and the talkgroup from whatever Link Control it finds embedded in the
// voice, including the block that carries the radio's Talker Alias — so the
// rest of an over on TG 2 came back as radio 5002016 calling talkgroup
// 4929869, which is "LS" and "K9M", the operator's callsign read as two
// numbers (K9MLS's production log, 2026-10-03; thirteen times that week).
// Nobody listens on talkgroup 4929869, so the rest of the over went nowhere.
//
// So: a voice frame on a new stream, **with no voice header**, arriving on a
// timeslot where this peer's own call has not ended and was heard within the
// stream timeout, is the same call. It is given that call's stream, caller,
// talkgroup and call type, and so is every later frame of the new stream.
//
// The header is the line. Somebody keying up sends one, and a frame that
// follows a header is a new call however soon it comes. What this accepts is
// a second person whose header was also lost, on the same hotspot and
// timeslot, within two seconds of a call that never ended, being carried
// under the first caller's ID for that over. That is rarer than the fault,
// and audio reaching the right talkgroup under the wrong name is better than
// audio reaching nobody.
//
// **Only when that call is the one thing the peer is sending on the
// timeslot.** A peer that carries several calls at once — a linked QSP
// server, a bridge — interleaves their frames, and a second call's frame
// arriving between the first's is not the first restarting. So every call in
// progress is tracked, a frame of a stream already known is always itself,
// and a headerless newcomer is adopted only when exactly one call is open.
// A linked QSP server is never treated this way at all (join is false): what
// it sends was already sorted out by the server at the other end.
//
// Text and other data bursts are left alone: each is its own stream by
// design.
func (p *Peer) continuing(f hbp.Data, now time.Time, join bool) (hbp.Data, bool) {
	if f.IsUserData() || (f.Timeslot != hbp.Timeslot1 && f.Timeslot != hbp.Timeslot2) {
		return f, false
	}
	slot := int(f.Timeslot) - 1

	// Forget calls that ended or went quiet.
	open := p.calls[slot][:0]
	for _, c := range p.calls[slot] {
		if c.open && now.Sub(c.lastSeen) <= routing.StreamTimeout {
			open = append(open, c)
		}
	}
	p.calls[slot] = open

	header := f.FrameType == hbp.FrameTypeSync && f.DataType == hbp.DataTypeVoiceLCHeader
	var c *openCall
	began := false
	for i := range open {
		switch {
		case open[i].stream == f.StreamID:
			c = &open[i]
		case open[i].adopting && open[i].adopted == f.StreamID:
			c = &open[i]
			f = c.relabel(f)
		}
		if c != nil {
			break
		}
	}
	switch {
	case c != nil:
		// A stream already known.
	case join && !header && len(open) == 1:
		c = &open[0]
		c.adopted, c.adopting = f.StreamID, true
		f = c.relabel(f)
		began = true
	case len(open) >= maxOpenCalls:
		// More calls at once than is plausible on one timeslot. Carried as it
		// arrived and not tracked, so this stays bounded.
		return f, false
	default:
		p.calls[slot] = append(open, openCall{stream: f.StreamID, source: f.SourceID,
			target: f.TargetID, callType: f.CallType, open: true})
		c = &p.calls[slot][len(p.calls[slot])-1]
	}
	c.lastSeen = now
	if f.IsTerminator() {
		c.open = false
	}
	return f, began
}

// maxOpenCalls bounds the calls tracked per peer per timeslot.
const maxOpenCalls = 16

func (c *openCall) relabel(f hbp.Data) hbp.Data {
	f.StreamID, f.SourceID, f.TargetID, f.CallType = c.stream, c.source, c.target, c.callType
	return f
}
