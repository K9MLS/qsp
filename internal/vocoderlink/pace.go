package vocoderlink

import (
	"time"

	"github.com/k9mls/qsp/internal/dmrfec"
)

// BurstInterval is the time one DMR burst occupies on the air: three 20 ms
// vocoder frames.
const BurstInterval = 60 * time.Millisecond

// PacerHeadStart is how long the first voice burst of a transmission waits
// after its header, so that audio arriving late at the start does not leave a
// gap.
//
// **Sized from traffic, not borrowed.** A capture of production's Zello calls
// to a Motorola repeater on 2026-09-21, replayed against a 60 ms clock, needed
// 0 ms and 2 ms of head-start never to run dry: Zello delivers faster than
// real time overall, so the lead built by the fast runs covers its stalls.
// What that cannot cover is a stall in the first few bursts, before any lead
// exists, so this is two bursts' worth -- enough for that, and small beside a
// path whose own delay through Zello is far larger. MMDVMHost's network jitter
// buffer, 300 to 360 ms by default, is sized for internet jitter, which this
// is not.
const PacerHeadStart = 2 * BurstInterval

// PacerGrace is how late a burst may arrive after its slot and still go out as
// itself rather than being replaced by silence.
//
// **Measured, not chosen.** QSP's own timing jitter, seen in a capture of paced
// output on 2026-09-21, was up to about 8 ms around a correct 60 ms average. A
// burst a few milliseconds late is that jitter, not a stall, and replacing it
// with silence would push the real audio a whole slot later for nothing. A
// third of a slot covers the jitter with room to spare and is still far
// shorter than any stall the capture showed.
const PacerGrace = BurstInterval / 3

// silenceParameters is the vocoder frame DMR equipment sends for silence.
//
// **Measured on two unrelated systems.** internal/dmrfec's IPSC notes record
// that a Motorola XPR8300 speaking IP Site Connect and an MMDVM hotspot
// speaking Homebrew produce this identical frame for silence, and that it is
// the most common frame in testdata/hbp, 236 times. Filling a gap with it
// sends exactly what the operator's own repeater sends when nobody talks.
const silenceParameters dmrfec.Parameters = 0x1F003533F19C1

// pacedKind is what a queued item becomes when it is released.
type pacedKind uint8

const (
	pacedHeader pacedKind = iota
	pacedVoice
	pacedTerminator
)

// pacedItem is one thing a transmission has ready to send.
//
// **Not a finished burst.** A voice item carries its three encoded frames, and
// its superframe position, embedded signalling and sequence number are decided
// when it is released. That is what lets a silent burst be inserted in a gap:
// had positions been fixed at encode time, every burst queued after the
// silence would carry the wrong one, and the Link Control and alias cycle would
// break. Headers and terminators carry their payload, and get their sequence
// number at release too, so the numbering follows the order frames leave.
type pacedItem struct {
	kind   pacedKind
	tx     *outbound
	burst  []byte
	frames [][]byte
	at     time.Time
}

// pacer releases a transmission's bursts on the cadence a radio keeps, and
// fills a stall with silence.
//
// # Why this exists
//
// **Motorola equipment sends a burst every 60 ms and never pauses.** Across
// the IPSC captures in testdata, real repeaters and a real master keep 57 to
// 65 ms between frames, 60.0 on average, with no gap over 100 ms. QSP sent
// Zello audio as fast as it arrived, and Zello delivers in clumps: runs at 45
// to 51 ms, then stalls of 150 to 219 ms. A repeater that runs dry repeats
// what it has, and the operator heard an echo. Pacing (0426) fixed the clumps.
//
// **A stall Zello itself makes still left a gap.** A capture of paced output
// showed one of 526 ms, which no head-start short of half a second covers. A
// repeater fills a gap by repeating its last audio -- MMDVMHost's
// insertSilence, despite its name, copies the last audio block -- and a gap of
// nine bursts filled that way is a stutter. So the pacer fills it itself, with
// silence: the stall becomes a short dropout.
//
// # The rule
//
// A header opens a transmission and pushes the next slot PacerHeadStart on; a
// terminator closes it. Every other item goes no earlier than it arrived and
// no earlier than its slot, and pushes the next slot 60 ms on. While a
// transmission is open, a slot that passes with nothing arrived by PacerGrace
// after it gets a silent burst instead, and the cadence continues from the
// slot. Nothing is sent before a header or after a terminator.
//
// It takes explicit times and holds no clock, so the rule can be tested
// exactly; the channel's run loop supplies the time and builds what it
// returns.
type pacer struct {
	queue []pacedItem
	// next is the earliest the next item may go. Zero before anything has
	// been sent.
	next time.Time
	// open is the transmission between its released header and its released
	// terminator: the one a silent burst belongs to.
	open *outbound
}

// push queues an item.
func (p *pacer) push(item pacedItem) {
	p.queue = append(p.queue, item)
}

// stalled reports whether the open transmission's slot has nothing arrived in
// time for it.
func (p *pacer) stalled() bool {
	if p.open == nil {
		return false
	}
	return len(p.queue) == 0 || p.queue[0].at.After(p.next.Add(PacerGrace))
}

// due is when the pacer next has something to release, and false when it has
// nothing and no transmission is open.
func (p *pacer) due() (time.Time, bool) {
	if p.stalled() {
		// The slot is filled only once its grace has passed, so a burst that
		// is a few milliseconds late still goes as itself.
		return p.next.Add(PacerGrace), true
	}
	if len(p.queue) == 0 {
		return time.Time{}, false
	}
	if head := p.queue[0]; head.at.After(p.next) {
		return head.at, true
	}
	return p.next, true
}

// release returns every item due by now, in order, with a silent voice item
// for each stalled slot, and how many slots it filled.
func (p *pacer) release(now time.Time) (out []pacedItem, filled int) {
	for {
		when, ok := p.due()
		if !ok || when.After(now) {
			return out, filled
		}
		if p.stalled() {
			// **The cadence continues from the slot**, not from when the
			// silence went: filling late must not shift everything after it.
			out = append(out, pacedItem{kind: pacedVoice, tx: p.open, frames: silenceFrames()})
			p.next = p.next.Add(BurstInterval)
			filled++
			continue
		}
		item := p.queue[0]
		p.queue = p.queue[1:]
		// The slot advances from when the item was due, not from now: a
		// timer that fires a few milliseconds late must not make every
		// burst after it late too.
		switch item.kind {
		case pacedHeader:
			p.open = item.tx
			p.next = when.Add(PacerHeadStart)
		case pacedTerminator:
			p.open = nil
			p.next = when.Add(BurstInterval)
		default:
			p.next = when.Add(BurstInterval)
		}
		out = append(out, item)
	}
}

// flush returns everything queued, regardless of time: for shutdown, where a
// repeater left without its terminator stays keyed. Nothing is filled; a
// process that is leaving has no stall to cover.
func (p *pacer) flush() []pacedItem {
	out := append([]pacedItem(nil), p.queue...)
	p.queue = p.queue[:0]
	p.open = nil
	return out
}

// silenceFrames is one burst's worth of the silence frame, freshly allocated so
// nothing downstream can alias another burst's.
func silenceFrames() [][]byte {
	out := make([][]byte, dmrfec.FramesPerBurst)
	for i := range out {
		out[i] = dmrfec.Encode(silenceParameters)
	}
	return out
}
