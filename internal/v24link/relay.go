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

// relay carries one record of st's transmission onward, and reports whether
// the transmission is being carried.
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
// call it missed the start of.
func (l *Listener) relay(st *station, payload []byte, rec Record, began, ended bool, now time.Time) bool {
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
		// The floor is asked for once, when the transmission begins.
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
			return false
		}
	case !relaying:
		return false
	case !ended:
		// Still talking: keep the floor from going stale.
		l.floor.Take(st.holder, now)
	}

	if ended {
		l.endRelay(st)
		return true
	}

	for _, send := range l.others(st) {
		// A transmission QSP joined part-way has no start of its own to pass
		// on, so the repeater's own form of one goes first.
		if began && rec.Kind != RecordStart {
			_ = send(startMarker)
		}
		_ = send(payload) // a repeater that cannot be written to is closed by its own tunnel
	}
	if rec.Kind == RecordVoice {
		if l.cfg.Gateways != nil {
			l.cfg.Gateways.FromRepeater(payload[2:])
		}
		l.relayed.Add(1)
		st.mu.Lock()
		st.view.Relayed++
		st.mu.Unlock()
	}
	return true
}

// endRelay closes a carried transmission everywhere it was carried to, and
// gives the floor back. It does nothing for a transmission that was not being
// carried, so it is safe to call whenever one ends for any reason.
func (l *Listener) endRelay(st *station) {
	st.mu.Lock()
	relaying := st.relaying
	st.relaying = false
	st.mu.Unlock()
	if !relaying {
		return
	}
	for _, send := range l.others(st) {
		// Twice, as a repeater sends it.
		_ = send(endMarker)
		_ = send(endMarker)
	}
	if l.cfg.Gateways != nil {
		l.cfg.Gateways.EndFromRepeater()
	}
	l.floor.Release(st.holder)
}

// others is how to write to every other repeater whose link is open.
func (l *Listener) others(st *station) []func([]byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []func([]byte) error
	for _, o := range l.stations {
		if o == st {
			continue
		}
		o.mu.Lock()
		if o.view.Up && o.send != nil {
			out = append(out, o.send)
		}
		o.mu.Unlock()
	}
	return out
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
