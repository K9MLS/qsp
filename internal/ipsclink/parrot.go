package ipsclink

import (
	"context"
	"time"

	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// # Parrot for Motorola repeaters
//
// Until 2026-09-03 this did not exist, and the reason recorded in
// peers.DeliverFromIPSC was that "there is no path back to an IPSC peer".
// **That reason stopped being true when patch 0195 fixed the outbound frame
// shape** and a repeater keyed on QSP's audio for the first time. The comment
// outlived the fact, which is the failure §7 names: a comment that has stopped
// being true is a defect, and this one was switching off a feature.
//
// # What the operator has to do that QSP cannot
//
// **The parrot talkgroup must be in the repeater's codeplug.** ADR-0043 states
// the limit: QSP has authority over delivery and none over transmission. A
// repeater receives everything QSP sends and decides for itself what to put on
// the air, and QSP cannot read its codeplug or learn its talkgroups, because an
// IPSC peer announces no subscriptions.
//
// So IPSC parrot can be entirely correct and produce silence, and the operator
// will reasonably read that as a QSP fault. Nothing in code can fix that; the
// documentation and the console say so instead.
//
// The Motorola convention for an echo test is a **group call**, unlike
// BrandMeister where it is a private call to 9990 or an MCC-based ID.
// BrandMeister's reason is its own architecture — every talkgroup is
// distributed worldwide, so a parrot talkgroup would carry test audio across
// the whole network and only one operator could use it at a time. A club
// network has no such fan-out, and QSP already replays to one peer and no
// other, because a frame parrot handles never reaches the routing core.

// parrotHandles offers a frame to parrot, and reports whether parrot took it.
//
// A frame parrot handles is consumed: it does not reach Deliver and never
// enters routing. A member's echo test is not something the rest of the network
// should hear.
func (l *Listener) parrotHandles(from hbp.RepeaterID, frame hbp.Data) bool {
	if l.cfg.Parrot == nil {
		return false
	}
	if !l.cfg.Parrot.Handles(frame) {
		// A member who keys anything else has stopped talking to parrot, and a
		// half-finished recording of theirs should not be played back at them
		// later.
		l.cfg.Parrot.Cancel(from)
		return false
	}
	if rec := l.cfg.Parrot.Observe(from, frame); rec != nil {
		l.replay(*rec)
	}
	return true
}

// expireParrot completes recordings whose transmissions have stopped.
//
// Called from the sweep, because a DMR transmission ends in silence rather than
// in a frame that can be recognised.
func (l *Listener) expireParrot() {
	if l.cfg.Parrot == nil {
		return
	}
	for _, rec := range l.cfg.Parrot.Expire(time.Now()) {
		l.replay(rec)
	}
}

// replay hands a finished recording to the player.
func (l *Listener) replay(rec parrot.Recording) {
	if l.player == nil {
		return
	}
	l.player.Start(l.parrotCtx(), rec)
}

// parrotCtx returns the context replays run under, so that shutting the
// listener down stops them rather than leaving goroutines writing to a closed
// socket.
func (l *Listener) parrotCtx() context.Context {
	if l.ctx != nil {
		return l.ctx
	}
	return context.Background()
}

// ipscSink delivers one replayed frame to one repeater.
//
// It is the counterpart of the Homebrew sink in internal/peers: the same
// parrot.Player drives both, so there is exactly one copy of the sixty
// millisecond timing, and only the delivery differs.
type ipscSink struct{ l *Listener }

// Deliver sends one frame to the repeater that recorded it, and to no other.
func (s ipscSink) Deliver(peer hbp.RepeaterID, frame hbp.Data) error {
	return s.l.SendVoiceTo(uint32(peer), frame)
}
