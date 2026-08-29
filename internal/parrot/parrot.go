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
}

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

	r := &Recorder{cfg: cfg, now: now, active: make(map[hbp.RepeaterID]*recording)}
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
		if now.Sub(current.startedAt) > r.cfg.MaxDuration {
			// Kept rather than discarded: hearing thirty seconds of your own
			// audio answers the question a member was asking.
			current.truncated = true
		} else {
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

		// **A private call must be addressed back to the radio that made it.**
		// A radio un-mutes a private call only when the target is its own ID,
		// so replaying one with the original addressing produces frames the
		// radio receives and refuses to play — parrot appearing to work and
		// sounding like nothing.
		//
		// A group call is left alone: the member's display should show what it
		// showed when they transmitted, and the talkgroup is what their radio
		// is listening to.
		if f.CallType == hbp.CallPrivate {
			f.TargetID = f.SourceID
			f.SourceID = r.cfg.Talkgroup
		}

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
