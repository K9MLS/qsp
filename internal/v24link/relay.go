package v24link

import (
	"log/slog"
	"time"
)

// startMarker and endMarker open and close a transmission, as the repeater
// sent them. QSP sends one on a repeater's behalf only when that repeater did
// not: a transmission joined part-way has no start, and one that went quiet,
// or whose tunnel closed, has no end.
var (
	startMarker = []byte{0x07, controlUI, recordMarker, 0x02, 0x02, markerStart, 0x0B, 0, 0, 0, 0, 0}
	endMarker   = []byte{0x07, controlUI, recordMarker, 0x02, 0x02, markerEnd, 0x0B, 0, 0, 0, 0, 0}
)

// Who is being carried, and who is owed an end.
//
// **Two facts, each kept in one place** (since 0.1.328). Which call is
// carried is the floor's: a transmission is carried while its repeater holds
// the floor and stops being carried the moment it does not. Which call a
// repeater has been sent the start of, and so is owed the end of, is that
// repeater's own: station.receiving names the talker. The gateways together
// are one more listener, and Listener.toGateways names theirs.
//
// Until then each talker kept a flag saying it was carried and each listener
// a flag saying it had been sent a start, and nothing tied a flag to the call
// it was about. A repeater that faded for a little over a second came back
// still flagged as carried, alongside whoever had taken the floor meanwhile,
// and a listener got two calls a frame at a time, which a radio plays as
// neither. When the faded call was finally closed, its end went to everybody,
// in the middle of the call they were now listening to (found 2026-10-07, D1
// to D4).
//
// **An end goes only to a listener still on that call.** A listener moved to
// another call was sent the old one's end when it moved, so closing a call
// that has already been replaced sends nothing anywhere.
//
// Everything here runs with Listener.callMu held, so a record, a timer and a
// gateway's frame each see the whole of the last one's work.

// relay carries one record of st's transmission onward, and reports whether
// the transmission is being carried. The caller holds callMu.
//
// **To the gateways, voice and nothing else**: from its type byte on, a
// repeater's voice record is the gateway protocol's voice frame, so it goes
// as it came, and the call's end becomes the terminator a gateway expects.
// **To other repeaters, every record as it was received**: markers, header
// and voice, which is what one repeater says to another.
//
// Nothing is decoded and nothing is rewritten. The IMBE in a voice record
// leaves as the bytes it arrived as (ADR-0034).
//
// **One call at a time.** A transmission that begins while another station
// has the floor is heard, counted, and not carried — not from its first
// record and not from any later one, so a listener never gets the tail of a
// call it missed the start of. One that loses the floor part-way, by going
// quiet for longer than the floor is held, is not carried from there on.
func (l *Listener) relay(st *station, payload []byte, rec Record, began, ended bool, now time.Time) (carried, lost bool) {
	if began && ended {
		// A start that interrupted a transmission whose end was lost: close
		// the old one where it was carried, then treat this as a beginning.
		l.endRelay(st)
		ended = false
	}
	st.mu.Lock()
	relaying := st.relaying
	st.mu.Unlock()

	switch {
	case began:
		// The floor is asked for when the transmission begins.
		relaying = l.floor.Take(st.holder, now)
		st.mu.Lock()
		st.relaying = relaying
		if st.call != nil {
			st.call.Carried = relaying
		}
		if !relaying {
			st.view.Held++
		}
		st.mu.Unlock()
		if !relaying {
			l.held.Add(1)
			return false, false
		}
		// It has the floor, so whatever call it was being sent is over or
		// has gone quiet. That call's end now, while it means something,
		// and not whenever its talker is given up on.
		st.mu.Lock()
		was, send, linked := st.receiving, st.send, st.view.Up && st.send != nil
		st.receiving = ""
		st.mu.Unlock()
		if was != "" && linked {
			send(endMarker, nil)
			send(endMarker, nil)
		}
	case !relaying:
		return false, false
	case !ended:
		// Still talking, and it keeps the floor only if it still has it. The
		// answer was thrown away until 0.1.328, which is how a call that had
		// lost the floor went on being carried.
		if !l.floor.Take(st.holder, now) {
			l.endRelay(st)
			return false, true
		}
	}

	if ended {
		l.endRelay(st)
		return true, false
	}

	l.toRepeaters(st.holder, st, payload, rec.Kind == RecordStart, false, nil)
	if rec.Kind == RecordVoice {
		if l.cfg.Gateways != nil {
			if l.toGateways != "" && l.toGateways != st.holder {
				// The gateways were on a call that never ended. Its end
				// first, or this one's voice runs on from that one's.
				l.cfg.Gateways.EndFromRepeater()
			}
			l.toGateways = st.holder
			l.cfg.Gateways.FromRepeater(payload[2:])
		}
		l.relayed.Add(1)
		st.mu.Lock()
		st.view.Relayed++
		st.mu.Unlock()
	}
	return true, false
}

// endRelay stops carrying st's transmission: its end goes to whoever is
// still listening to it, and the floor is given back if st still has it. It
// does nothing for a transmission that was not being carried, so it is safe
// to call whenever one ends for any reason. The caller holds callMu.
func (l *Listener) endRelay(st *station) {
	st.mu.Lock()
	relaying := st.relaying
	st.relaying = false
	st.mu.Unlock()
	if !relaying {
		return
	}
	l.endAtRepeaters(st.holder)
	if l.toGateways == st.holder {
		l.toGateways = ""
		if l.cfg.Gateways != nil {
			l.cfg.Gateways.EndFromRepeater()
		}
	}
	l.floor.Release(st.holder)
}

// toRepeaters sends one record of talker's call to every linked repeater but
// except, and reports how many that was. The caller holds callMu.
//
// A repeater hearing this call for the first time is sent a start before the
// record, whenever in the call that is: a call QSP joined part-way has none
// of its own, and a repeater whose link opened part-way missed it. If it was
// on another call, that call's end goes first. isStart says the record is
// itself a start, so none is added. header sends the captured call header
// after an added start.
//
// wrote, when not nil, is called for each repeater once the record has been
// written to its tunnel. What is returned is repeaters the record was queued
// for, which is known now; whether it left is known later.
func (l *Listener) toRepeaters(talker string, except *station, payload []byte, isStart, header bool, wrote func(*station)) int {
	reached := 0
	for _, o := range l.linked() {
		if o == except {
			continue
		}
		o.mu.Lock()
		was, send := o.receiving, o.send
		o.receiving = talker
		o.mu.Unlock()

		if was != talker {
			if was != "" {
				// Twice, as a repeater sends it.
				send(endMarker, nil)
				send(endMarker, nil)
			}
			if !isStart {
				send(startMarker, nil)
				if header {
					send(capturedHeader1, nil)
					send(capturedHeader2, nil)
				}
			}
		}
		var after func()
		if wrote != nil {
			after = func() { wrote(o) }
		}
		send(payload, after)
		reached++
	}
	return reached
}

// endAtRepeaters closes talker's call at every repeater still listening to
// it, with the end marker twice as a repeater sends it, and reports how many
// that was. The caller holds callMu.
//
// A repeater whose link dropped during the call is not among them: it cannot
// be sent anything, and it stopped being owed an end when its link went (see
// countUp in serve).
func (l *Listener) endAtRepeaters(talker string) int {
	ended := 0
	for _, o := range l.linked() {
		o.mu.Lock()
		listening, send := o.receiving == talker, o.send
		if listening {
			o.receiving = ""
		}
		o.mu.Unlock()
		if !listening {
			continue
		}
		send(endMarker, nil)
		send(endMarker, nil)
		ended++
	}
	return ended
}

// supersede closes any older tunnel that is the same repeater: the same
// router, the same group, the same site.
//
// **A router that restarts dials again and never closes what it had.** The
// old tunnel would time out by itself in half a minute; until then the
// console would show the repeater twice and count a link that is not there.
// A second serial port on the same router is a different group or a
// different site, and is left alone.
func (l *Listener) supersede(st *station, group, site byte, log *slog.Logger) {
	l.mu.Lock()
	var old []*station
	for _, o := range l.stations {
		if o == st || o.router != st.router || o.id > st.id {
			continue
		}
		o.mu.Lock()
		same := o.view.Introduced && o.view.Site == site && o.group == group
		o.mu.Unlock()
		if same {
			old = append(old, o)
		}
	}
	l.mu.Unlock()
	for _, o := range old {
		log.Info("an older tunnel to the same repeater was closed", slog.Int("site", int(site)))
		_ = o.conn.Close()
	}
}
