package vocoderlink

import (
	"time"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
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

// pacer releases a transmission's bursts on the cadence a radio keeps.
//
// # Why this exists
//
// **Motorola equipment sends a burst every 60 ms and never pauses.** Across
// the IPSC captures in testdata, real repeaters and a real master keep 57 to
// 65 ms between frames, 60.0 on average, with no gap over 100 ms. QSP sent
// Zello audio as fast as it arrived, and Zello delivers in clumps: runs at 45
// to 51 ms, then stalls of 150 to 219 ms. A repeater playing out at 60 ms runs
// dry in the stall and repeats what it has to cover the gap -- MMDVMHost logs
// exactly that, "returning the last received frame" -- and the operator
// heard it as an echo. The hotspots coped only because MMDVMHost buffers.
//
// # The rule
//
// Every frame is released no earlier than it arrived and no earlier than the
// channel's next slot. A voice burst or terminator then pushes the next slot
// 60 ms on; a header pushes it PacerHeadStart on, so the first voice burst has
// a head-start. Nothing is ever released faster than one burst per 60 ms.
//
// **A late burst goes out when it arrives and the cadence resumes from
// there.** Catching up by sending faster would follow every gap with a clump,
// which is the fault being removed.
//
// It takes explicit times and holds no clock, so the rule can be tested
// exactly; the channel's run loop supplies the time and delivers what it
// returns.
type pacer struct {
	queue []pacedFrame
	// next is the earliest a frame may be released. Zero before anything has
	// been sent.
	next time.Time
}

type pacedFrame struct {
	frame hbp.Data
	at    time.Time
}

// push queues a frame that was ready at the given time.
func (p *pacer) push(f hbp.Data, at time.Time) {
	p.queue = append(p.queue, pacedFrame{frame: f, at: at})
}

// due is when the frame at the front may go, and false when nothing waits.
func (p *pacer) due() (time.Time, bool) {
	if len(p.queue) == 0 {
		return time.Time{}, false
	}
	head := p.queue[0]
	if head.at.After(p.next) {
		return head.at, true
	}
	return p.next, true
}

// late reports whether the frame at the front arrived after its slot: an
// underrun, which the listener hears as a gap however it is handled.
func (p *pacer) late() bool {
	return len(p.queue) > 0 && !p.next.IsZero() && p.queue[0].at.After(p.next) &&
		p.queue[0].frame.FrameType != hbp.FrameTypeSync
}

// release returns every frame due by now, in order, advancing the slot as it
// goes.
func (p *pacer) release(now time.Time) (out []hbp.Data, late int) {
	for {
		when, ok := p.due()
		if !ok || when.After(now) {
			return out, late
		}
		if p.late() {
			late++
		}
		f := p.queue[0].frame
		p.queue = p.queue[1:]
		// The slot advances from when the frame was due, not from now: a
		// timer that fires a few milliseconds late must not make every
		// burst after it late too.
		if isHeader(f) {
			p.next = when.Add(PacerHeadStart)
		} else {
			p.next = when.Add(BurstInterval)
		}
		out = append(out, f)
	}
}

// flush returns everything queued, regardless of time: for shutdown, where a
// repeater left without its terminator stays keyed.
func (p *pacer) flush() []hbp.Data {
	out := make([]hbp.Data, 0, len(p.queue))
	for _, q := range p.queue {
		out = append(out, q.frame)
	}
	p.queue = p.queue[:0]
	return out
}

func isHeader(f hbp.Data) bool {
	return f.FrameType == hbp.FrameTypeSync && f.DataType == dmrfec.DataTypeVoiceLCHeader
}
