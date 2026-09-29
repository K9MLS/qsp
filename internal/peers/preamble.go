package peers

import (
	"sync"
	"time"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// One preamble per text to a hotspot.
//
// # Why
//
// **MMDVMHost expects one preamble from the network and makes its own
// wake-up train from it.** For every preamble CSBK it receives, it transmits
// fifteen, counting down from the preamble's blocks-to-follow plus fourteen
// (DMRSlot.cpp, NO_PREAMBLE_CSBK = 15). A text reaches QSP with sixteen
// preambles — a radio's own, a Motorola repeater's, and QSP's composed ones
// alike — and QSP passed all sixteen on. The hotspot then put 240 preambles
// on the air, about fourteen seconds of them, with the countdown jumping back
// up sixteen times, and the radio listening gave up on a message that
// arrived long after the count said it would. Every text to a hotspot radio
// was lost that way on 2026-09-28, while the same text displayed through a
// Motorola repeater, which transmits preambles as it receives them.
//
// The September capture that recorded hotspot delivery working,
// testdata/hbp/hbp-text-rate34.pcap, shows QSP sending the hotspot headers
// and blocks and no preambles at all.
//
// # What
//
// **Only a preamble that announces data is held**, which is exactly the set
// MMDVMHost multiplies: `(csbko == PRECCSBK) && csbk.getDataContent()`, the
// data-content bit being octet 2 bit 7. A preamble with that bit clear wakes
// radios for control signalling — a private call's request and answer, a
// radio check, a call alert — is followed by a CSBK rather than a data header,
// and MMDVMHost passes it through as it is. **0443 held those too**, released
// nothing because no data header followed, and so stripped the wake-up from
// every private call to a hotspot radio; 0445 put them back.
//
// For each hotspot and timeslot, the latest data preamble is held rather than
// sent. When a data header follows, the held preamble goes first and the
// header after it, so the hotspot receives exactly one — the last, whose
// count is the header and blocks still to come, which is the count
// MMDVMHost's own train then ends on. A preamble no header follows within
// preambleHold is dropped. Everything else passes untouched, including any
// CSBK that is not a preamble: a radio check or a call alert still goes out
// the moment it arrives.
//
// This sits where frames leave for a hotspot, so it applies whatever the
// frame's origin: another hotspot, a Motorola repeater, a link, or QSP
// itself. The Motorola side is not touched and keeps every preamble.

// preambleHold is how long a held preamble waits for its header. A text's
// preambles and header arrive within about two seconds of each other.
const preambleHold = 3 * time.Second

type preambleKey struct {
	peer hbp.RepeaterID
	slot hbp.Timeslot
}

type heldPreamble struct {
	frame hbp.Data
	at    time.Time
}

// preambleGate decides what a hotspot is sent. It is safe for concurrent use:
// frames reach hotspots from the serve loop, the IPSC listener, links and the
// text sender.
type preambleGate struct {
	mu   sync.Mutex
	held map[preambleKey]heldPreamble
}

func newPreambleGate() *preambleGate {
	return &preambleGate{held: map[preambleKey]heldPreamble{}}
}

// pass returns what to send to peer now, in order, in place of frame.
func (g *preambleGate) pass(peer hbp.RepeaterID, frame hbp.Data, now time.Time) []hbp.Data {
	if g == nil {
		return []hbp.Data{frame}
	}
	key := preambleKey{peer: peer, slot: frame.Timeslot}

	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case isDataPreamble(frame):
		g.held[key] = heldPreamble{frame: frame, at: now}
		return nil
	case frame.DataType == dmrfec.DataTypeDataHeader && frame.FrameType == hbp.FrameTypeSync:
		h, ok := g.held[key]
		delete(g.held, key)
		if ok && now.Sub(h.at) <= preambleHold {
			return []hbp.Data{h.frame, frame}
		}
		return []hbp.Data{frame}
	default:
		return []hbp.Data{frame}
	}
}

// isDataPreamble reports whether a frame is a preamble announcing data: the
// one kind MMDVMHost multiplies, and so the one kind this gate holds.
func isDataPreamble(frame hbp.Data) bool {
	if frame.DataType != dataTypeCSBK {
		return false
	}
	c, ok := dmrfec.CSBKOf(frame.Payload[:])
	return ok && c.IsPreamble() && c.DataFollows
}
