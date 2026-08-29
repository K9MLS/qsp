// Package calls observes voice transmissions without routing them.
//
// A DMR transmission arrives as a burst of frames sharing a stream ID. This
// package reassembles those frames into calls so an operator can answer "who is
// talking, and who just talked" — Constitution §10 — before any routing engine
// exists.
//
// It makes no routing decision and forwards nothing. It watches.
//
// # Ending a call
//
// A well-behaved transmission ends with a terminator frame. Real ones sometimes
// do not: the peer loses power, the network drops the last datagram, or the
// stream is cut mid-burst. A tracker that waited for a terminator would leave
// such a call open forever, which in a bridging context is how a talkgroup gets
// welded open.
//
// Calls therefore end one of two ways, and the distinction is recorded rather
// than hidden:
//
//   - EndTerminated: a terminator arrived. Normal.
//   - EndTimedOut: frames stopped without one. The call is closed after
//     StreamTimeout and marked, because an operator seeing many of these has a
//     real problem — a flaky peer or a lossy link — that a silent cleanup would
//     conceal.
//
// # Stream identity
//
// A stream is identified by peer, stream ID and timeslot together. Stream IDs
// are chosen by the transmitting peer and are not globally unique: the captured
// fixtures show the same ID on two links as a gateway relays one transmission,
// and two peers could pick the same value independently. Timeslot is included
// because DMR carries two simultaneous conversations per peer.
package calls

import (
	"fmt"
	"sort"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// StreamTimeout is how long a stream may be silent before it is presumed lost.
//
// Voice frames arrive every 60 ms in a DMR superframe. Two seconds tolerates a
// substantial burst of loss while still closing a dead call promptly enough
// that an operator watching the console does not see a phantom transmission.
const StreamTimeout = 2 * time.Second

// DefaultHistory is how many completed calls are retained for the last-heard
// list.
const DefaultHistory = 50

// EndReason records how a call finished.
type EndReason string

const (
	// EndTerminated means a terminator frame arrived. This is the normal case.
	EndTerminated EndReason = "terminated"
	// EndTimedOut means frames stopped without a terminator.
	EndTimedOut EndReason = "timed_out"
)

// Key identifies one in-progress transmission.
type Key struct {
	Peer     hbp.RepeaterID
	Stream   hbp.StreamID
	Timeslot hbp.Timeslot
}

// String implements fmt.Stringer.
func (k Key) String() string {
	return fmt.Sprintf("peer=%d stream=%s %s", k.Peer, k.Stream, k.Timeslot)
}

// Call is one voice transmission.
type Call struct {
	Key Key
	// Source is the radio ID that keyed up. Unlike the peer ID, this survives
	// relaying, so it is the identity an operator recognises.
	Source uint32
	// Target is the talkgroup or radio being called.
	Target uint32
	// Group reports whether this is a group call rather than a private one.
	Group bool
	// Started is when the first frame arrived, in UTC.
	Started time.Time
	// Ended is when the call finished, in UTC. Zero while in progress.
	Ended time.Time
	// EndReason records how it finished. Empty while in progress.
	EndReason EndReason
	// Frames counts the frames received.
	Frames int
	// Voice reports whether any voice frame arrived.
	//
	// **A text message is a handful of one-frame data bursts**, each with its
	// own stream ID, so each becomes a call. Fifty of them bury the voice
	// traffic this list exists to show — and marking them "no terminator" is a
	// false alarm, because a single data burst has no terminator and is not
	// meant to. The distinction is recorded here so the console can tell them
	// apart rather than guessing from the frame count.
	Voice bool
}

// Duration returns how long the call ran, or how long it has been running.
func (c Call) Duration(now time.Time) time.Duration {
	if c.Ended.IsZero() {
		return now.Sub(c.Started)
	}
	return c.Ended.Sub(c.Started)
}

// InProgress reports whether the call is still running.
func (c Call) InProgress() bool { return c.Ended.IsZero() }

// Options configures a Tracker.
type Options struct {
	// Timeout is how long a stream may be silent before being presumed lost.
	// Zero selects StreamTimeout.
	Timeout time.Duration
	// History is how many completed calls to retain. Zero selects
	// DefaultHistory.
	History int
	// MaxActive bounds concurrent in-progress calls. Zero selects a limit
	// derived from History. It exists so a peer cannot exhaust memory by
	// sending frames with an endless supply of stream IDs.
	MaxActive int
}

// Tracker reassembles frames into calls.
//
// It is not safe for concurrent use and is designed to be owned by the same
// goroutine that reads the socket, consistent with ADR-0002.
type Tracker struct {
	timeout   time.Duration
	maxActive int

	active   map[Key]*Call
	lastSeen map[Key]time.Time
	history  []Call
	capacity int
}

// NewTracker constructs a Tracker.
func NewTracker(opts Options) *Tracker {
	if opts.Timeout <= 0 {
		opts.Timeout = StreamTimeout
	}
	if opts.History <= 0 {
		opts.History = DefaultHistory
	}
	if opts.MaxActive <= 0 {
		// Two timeslots per peer, so a generous allowance still bounds memory.
		opts.MaxActive = 128
	}
	return &Tracker{
		timeout:   opts.Timeout,
		maxActive: opts.MaxActive,
		active:    make(map[Key]*Call),
		lastSeen:  make(map[Key]time.Time),
		capacity:  opts.History,
	}
}

// Update classifies one accepted frame.
//
// It returns the call that started and the call that ended, either of which may
// be nil. A single frame can do both: a transmission consisting of one frame
// that is also a terminator starts and immediately ends.
func (t *Tracker) Update(peer hbp.RepeaterID, frame hbp.Data, now time.Time) (started, ended *Call) {
	now = now.UTC()
	key := Key{Peer: peer, Stream: frame.StreamID, Timeslot: frame.Timeslot}

	call, exists := t.active[key]
	if !exists {
		if len(t.active) >= t.maxActive {
			// Refusing to track is better than growing without bound. The frame
			// itself is unaffected; only observation is dropped.
			return nil, nil
		}
		call = &Call{
			Key:     key,
			Source:  frame.SourceID,
			Target:  frame.TargetID,
			Group:   frame.CallType == hbp.CallGroup,
			Started: now,
		}
		t.active[key] = call
		started = call
	}

	call.Frames++
	t.lastSeen[key] = now
	if frame.FrameType == hbp.FrameTypeVoice || frame.FrameType == hbp.FrameTypeVoiceSync {
		call.Voice = true
	}

	// A sync frame both opens and closes a transmission. The opening one is the
	// first frame of the stream, so only a later one ends it.
	if frame.FrameType == hbp.FrameTypeSync && call.Frames > 1 {
		ended = t.finish(key, call, now, EndTerminated)
	}

	if started != nil {
		snapshot := *started
		started = &snapshot
	}
	return started, ended
}

// Expire closes streams that stopped without a terminator.
//
// The caller invokes it periodically. Each returned call is marked
// EndTimedOut so that a lossy link is visible rather than looking like a short
// transmission.
func (t *Tracker) Expire(now time.Time) []Call {
	now = now.UTC()

	var lost []Key
	for key, call := range t.active {
		if now.Sub(t.lastFrameTime(call, now)) > t.timeout {
			lost = append(lost, key)
		}
	}
	// Deterministic order so logs and tests are reproducible.
	sort.Slice(lost, func(i, j int) bool {
		if lost[i].Peer != lost[j].Peer {
			return lost[i].Peer < lost[j].Peer
		}
		return lost[i].Stream < lost[j].Stream
	})

	out := make([]Call, 0, len(lost))
	for _, key := range lost {
		call := t.active[key]
		// The call ended when its last frame arrived, not now; recording the
		// sweep time would inflate every timed-out call by up to the timeout.
		endedAt := t.lastFrameTime(call, now)
		out = append(out, *t.finish(key, call, endedAt, EndTimedOut))
	}
	return out
}

// lastFrameTime reports when a call's most recent frame arrived.
//
// The arrival time is kept in a side map rather than on Call because it is the
// tracker's bookkeeping, not information an operator needs.
func (t *Tracker) lastFrameTime(call *Call, now time.Time) time.Time {
	if v, ok := t.lastSeen[call.Key]; ok {
		return v
	}
	return call.Started
}

func (t *Tracker) finish(key Key, call *Call, at time.Time, reason EndReason) *Call {
	call.Ended = at
	call.EndReason = reason
	delete(t.active, key)
	delete(t.lastSeen, key)

	finished := *call
	t.history = append(t.history, finished)
	if len(t.history) > t.capacity {
		t.history = t.history[len(t.history)-t.capacity:]
	}
	return &finished
}

// Active returns in-progress calls, most recently started first.
func (t *Tracker) Active() []Call {
	out := make([]Call, 0, len(t.active))
	for _, c := range t.active {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// History returns completed calls, most recent first.
func (t *Tracker) History() []Call {
	out := make([]Call, len(t.history))
	for i, c := range t.history {
		out[len(t.history)-1-i] = c
	}
	return out
}

// ActiveCount returns the number of in-progress calls.
func (t *Tracker) ActiveCount() int { return len(t.active) }
