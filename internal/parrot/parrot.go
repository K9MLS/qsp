// Package parrot records a transmission and hands it back for replay.
//
// **QSP replays bytes it never understood.** A transmission is a sequence of
// frames carrying 33-byte DMR bursts, and nothing here decodes one — the radio
// at the far end decodes its own audio exactly as it would anybody else's.
// That is what makes parrot cheap when audio features are not, and why it
// arrives long before the vocoder. See docs/adr/ADR-0028-parrot.md.
//
// This package is pure: it holds frames and a clock, performs no I/O, and owns
// no goroutines. The transport that plays a recording back is elsewhere, which
// is what makes every rule here testable without a socket.
package parrot

import (
	"errors"
	"fmt"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// Defaults for Config.
const (
	// DefaultMaxDuration bounds a recording.
	//
	// Without a bound a stuck PTT is unbounded memory. Thirty seconds is also
	// as long as anybody wants to wait to hear themselves: a member who
	// transmits for five minutes and waits five more has not been served.
	DefaultMaxDuration = 30 * time.Second

	// DefaultGap is the pause between the end of a transmission and the start
	// of its replay.
	//
	// Long enough that a radio has finished its own transmission and returned
	// to receive — a replay that begins instantly is played at a radio that is
	// not listening yet, and the first second is lost.
	DefaultGap = time.Second

	// FrameInterval is how often DMR frames arrive, and therefore how often
	// they must be replayed. A radio decodes on this schedule and faster
	// produces nothing it can use.
	FrameInterval = 60 * time.Millisecond
)

// Config configures a Recorder.
type Config struct {
	// Talkgroup is the number that records and replays. Required.
	Talkgroup uint32
	// Timeslot the talkgroup lives on.
	Timeslot hbp.Timeslot
	// MaxDuration bounds a recording. Zero selects DefaultMaxDuration.
	MaxDuration time.Duration
	// Gap is the pause before replay. Zero selects DefaultGap.
	Gap time.Duration
	// Silence is how long without a frame before a transmission is presumed
	// over. Zero selects calls.StreamTimeout.
	//
	// **A transmission's end is a silence, not a frame.** The header and the
	// terminator share a frame type and only position tells them apart, so
	// ending on the frame type would end every recording on its first frame.
	// internal/calls reached the same conclusion for the same reason.
	Silence time.Duration
	// Now supplies the clock. Nil means time.Now.
	Now func() time.Time
}

// Recording is a captured transmission, ready to be played back.
type Recording struct {
	// Peer is where it came from and where it goes back to. Parrot is a test
	// of one member's path, so a recording is never sent anywhere else.
	Peer hbp.RepeaterID
	// Frames are the captured frames, in order, with a fresh stream ID.
	Frames []hbp.Data
	// Truncated reports that the transmission hit MaxDuration.
	Truncated bool
	// Duration is how long the transmission ran.
	Duration time.Duration
	// PlayAt is when the replay should begin.
	PlayAt time.Time
}

// recording is one in progress.
type recording struct {
	peer      hbp.RepeaterID
	stream    hbp.StreamID
	frames    []hbp.Data
	startedAt time.Time
	// lastFrame is when a frame last arrived, which is how the end of a
	// transmission is detected.
	lastFrame time.Time
	truncated bool
}

// Recorder captures transmissions on the parrot talkgroup.
//
// It is not safe for concurrent use and is owned by the goroutine that reads
// the socket, in the single-writer style of ADR-0002.
type Recorder struct {
	cfg Config
	now func() time.Time
	// active is one in-progress recording per peer. Two hotspots may test at
	// once; the same hotspot testing twice replaces its own.
	active map[hbp.RepeaterID]*recording
	// nextStream produces stream IDs for replays.
	nextStream func() hbp.StreamID
	// maxFrames is the count a recording is allowed to reach, derived from
	// MaxDuration rather than configured separately.
	//
	// **Derived, so it cannot disagree with the duration it exists to
	// enforce.** A second setting would be a second thing to keep in step, and
	// an operator who lengthened one and not the other would get a bound they
	// did not choose. The headroom is generous because a frame arriving a
	// little early is ordinary and being truncated for it is not.
	maxFrames int
}

// frameHeadroom is how far above the expected frame count a recording may go
// before the count rather than the clock ends it.
//
// Three times. A transmission at the DMR rate reaches MaxDuration long before
// this, so a well-behaved peer never meets it; a peer sending three times too
// fast is not one whose audio is worth keeping.
const frameHeadroom = 3

// New constructs a Recorder.
func New(cfg Config) (*Recorder, error) {
	if cfg.Talkgroup == 0 {
		return nil, errors.New("parrot: a talkgroup is required")
	}
	if cfg.MaxDuration <= 0 {
		cfg.MaxDuration = DefaultMaxDuration
	}
	if cfg.Gap <= 0 {
		cfg.Gap = DefaultGap
	}
	if cfg.Silence <= 0 {
		cfg.Silence = calls.StreamTimeout
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	r := &Recorder{
		cfg:    cfg,
		now:    now,
		active: make(map[hbp.RepeaterID]*recording),
		// One frame every FrameInterval for MaxDuration, times the headroom.
		maxFrames: int(cfg.MaxDuration/FrameInterval) * frameHeadroom,
	}
	// Derived from the clock so a test can predict it, and monotonic so a
	// replay never reuses the stream ID of the transmission it copies — a
	// repeated stream ID is a duplicate to a radio and is discarded as one.
	var counter uint32
	r.nextStream = func() hbp.StreamID {
		counter++
		return hbp.StreamID(uint32(now().UnixNano())&0x00FFFFFF | counter<<24)
	}
	return r, nil
}

// Talkgroup returns the number parrot answers on.
func (r *Recorder) Talkgroup() uint32 { return r.cfg.Talkgroup }

// Handles reports whether a frame belongs to parrot.
//
// **A frame parrot handles is one the routing core never sees.** A recording
// being routed would face access control, contention and subscription on the
// way out, and none of those questions applies to a member hearing their own
// voice — nor should a test leak onto a bridged network.
func (r *Recorder) Handles(frame hbp.Data) bool {
	if frame.TargetID != r.cfg.Talkgroup {
		return false
	}
	// **Parrot answers audio, and a text message is not audio.**
	//
	// This tested only the talkgroup, the call type and the timeslot, which
	// was complete while IP Site Connect carried nothing but voice. Since
	// ADR-0045 a text arrives as a data burst, and one addressed to the parrot
	// number matched every condition here.
	//
	// The group case was merely odd: a text to the parrot talkgroup recorded
	// and played back. **The private case lost messages.** A private call to
	// the parrot number matches on either timeslot, so any private text to
	// that radio ID was consumed, never routed, and never delivered — and the
	// sender's radio still reported success, because the repeater
	// acknowledges on RF one hop away and a master is not part of that.
	//
	// Silent, plausible, and invisible to the operator, which is the failure
	// class this project keeps finding.
	if frame.IsUserData() {
		return false
	}
	// **A private call to the parrot number counts, on either timeslot.**
	// That is how most networks do parrot and how most operators program it,
	// because it lets somebody test without the whole club hearing them. A
	// private call is addressed to a number rather than carried on a
	// talkgroup, so requiring a particular timeslot for one would refuse the
	// commonest way it is used.
	if frame.CallType == hbp.CallPrivate {
		return true
	}
	return frame.CallType == hbp.CallGroup && frame.Timeslot == r.cfg.Timeslot
}

// Observe records a frame and reports a recording when a transmission ends.
//
// It returns nil while a transmission is in progress, and a Recording when the
// terminator arrives or the limit is reached.
func (r *Recorder) Observe(peer hbp.RepeaterID, frame hbp.Data) *Recording {
	now := r.now()
	current := r.active[peer]

	// A new stream replaces whatever this peer had. A member who keys up while
	// their own recording is being made has started over, which is what
	// pressing PTT again nearly always means.
	if current == nil || current.stream != frame.StreamID {
		current = &recording{peer: peer, stream: frame.StreamID, startedAt: now}
		r.active[peer] = current
	}
	current.lastFrame = now

	if !current.truncated {
		// **Bounded by count as well as by the clock.** The duration check
		// alone is a bound that does not hold: frames arrive over UDP and
		// nothing obliges a peer to send them at sixty a second, so a looping
		// hotspot — or a deliberate one — can send tens of thousands inside
		// thirty wall-clock seconds, and this map is per peer.
		//
		// Reaching it needs a registered peer that knows the password, so it is
		// not a way in from outside. It is a member's equipment misbehaving,
		// which is the ordinary case rather than the adversarial one, and a
		// bound that only holds for well-behaved senders is not a bound.
		switch {
		case now.Sub(current.startedAt) > r.cfg.MaxDuration,
			len(current.frames) >= r.maxFrames:
			// Kept rather than discarded: hearing thirty seconds of your own
			// audio answers the question a member was asking.
			current.truncated = true
		default:
			current.frames = append(current.frames, frame)
		}
	}

	if !current.truncated {
		// Otherwise the recording ends when the frames stop. See Expire.
		return nil
	}
	return r.finish(peer, current, now)
}

// Expire completes recordings whose transmissions have stopped.
//
// Called from the sweep, on the goroutine that owns this Recorder. A
// transmission ends in silence rather than in a distinguishable frame, so this
// is where most recordings finish.
func (r *Recorder) Expire(now time.Time) []Recording {
	var done []Recording
	for peer, rec := range r.active {
		if now.Sub(rec.lastFrame) < r.cfg.Silence {
			continue
		}
		if finished := r.finish(peer, rec, now); finished != nil {
			done = append(done, *finished)
		}
	}
	return done
}

// finish completes a recording.
func (r *Recorder) finish(peer hbp.RepeaterID, rec *recording, now time.Time) *Recording {
	delete(r.active, peer)

	if len(rec.frames) == 0 {
		return nil
	}

	stream := r.nextStream()
	frames := make([]hbp.Data, 0, len(rec.frames))
	for i, f := range rec.frames {
		f.StreamID = stream
		f.Sequence = uint8(i)

		// **Nothing else is rewritten, and a private call cannot be.**
		//
		// An earlier version swapped the source and target here so a private
		// replay would be addressed back to the calling radio. It did not work
		// on air, and could not: a DMR voice header carries the call's
		// addressing inside the 33-byte burst, in the Link Control, under its
		// own error correction. A radio believes the Link Control rather than
		// the wrapper around it, so the swap only made the two disagree —
		// frames the radio received and muted.
		//
		// Rewriting the Link Control means decoding and re-encoding a DMR
		// burst, which is precisely what QSP does not do and what makes parrot
		// cheap enough to exist. Until it does, **parrot answers a group call**:
		// replayed unchanged, the Link Control still says "group call to this
		// talkgroup", and a radio with that talkgroup in its receive list
		// un-mutes it with nothing rewritten at all.

		frames = append(frames, f)
	}

	return &Recording{
		Peer:      peer,
		Frames:    frames,
		Truncated: rec.truncated,
		// Measured to the last frame rather than to now, which for a recording
		// ended by silence includes the silence.
		Duration: rec.lastFrame.Sub(rec.startedAt),
		PlayAt:   now.Add(r.cfg.Gap),
	}
}

// Cancel abandons a peer's in-progress recording.
//
// Called when a member keys up on something else: a recording that was never
// finished should not be waiting to surprise them later.
func (r *Recorder) Cancel(peer hbp.RepeaterID) {
	delete(r.active, peer)
}

// Active reports how many recordings are in progress, for the health report.
func (r *Recorder) Active() int { return len(r.active) }

// String describes the configuration, for logs.
func (r *Recorder) String() string {
	return fmt.Sprintf("parrot on talkgroup %d timeslot %s", r.cfg.Talkgroup, r.cfg.Timeslot)
}
