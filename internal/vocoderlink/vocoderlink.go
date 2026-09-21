// Package vocoderlink carries a routed DMR call through a vocoder chip and out
// as USRP audio.
//
// # Where it sits
//
// The routing core decides a call reaches a transcoder (ADR-0063), the peers
// listener hands each frame here, and this package does the rest: acquire the
// chip, pull the three vocoder frames out of every voice burst, decode each to
// 20 ms of 8 kHz PCM, and send it to the one program on the far side of the
// USRP socket — `qsp-zello`, per ADR-0009.
//
// **Both directions share one chip, one at a time.** Audio arriving on the
// socket is encoded into a complete DMR transmission under the transcoder's
// own radio ID and handed to routing, where the per-repeater opt-in decides
// who hears it (outbound.go).
//
// # Why a queue and a goroutine per channel
//
// Send is called on the goroutine that reads every peer's socket. A chip
// answers one packet at a time over UDP, so decoding a burst is three round
// trips; doing that inline would stall every call on the server behind one
// transcoded call. **A full queue drops the frame and counts it.** Blocking
// instead would move the stall rather than remove it.
package vocoderlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k9mls/qsp/internal/ambe"
	"github.com/k9mls/qsp/internal/audio"
	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// Chip is what a channel needs from a vocoder. *ambe.Client satisfies it.
type Chip interface {
	Acquire(h ambe.Holder) error
	Release()
	Decode(frame ambe.ChannelFrame) (ambe.SpeechReply, error)
	Encode(samples []int16) (ambe.ChannelFrame, error)
	Rate() int
}

// Radio is the USRP side. *audio.Conn satisfies it.
type Radio interface {
	Send(f audio.Frame) error
	Receive(ctx context.Context) (audio.Frame, error)
}

// ErrQueueFull is returned by Send when the channel is not keeping up.
//
// One fixed message, so the listener's once-per-reason log line collapses a
// second of refusals into one line rather than fifty.
var ErrQueueFull = errors.New("the transcoder's queue is full; the vocoder is not keeping up")

// QueueDepth is how many frames a channel holds. A burst arrives every 60 ms,
// so this is about four seconds of a stalled chip before anything is dropped.
const QueueDepth = 64

// Options configures a Channel.
type Options struct {
	// Name is the transcoder's configured name.
	Name string
	// Chip returns the open vocoder, or nil when there is none. Called at the
	// start of every call, because the supervisor reopens a channel that
	// dropped and the client from the last call may be gone.
	//
	// **It must return a nil interface, not a nil *ambe.Client.** Use
	// SupervisedChip, which exists because the second compiles and is
	// non-nil.
	Chip func() Chip
	// Radio is the USRP socket. Required.
	Radio Radio
	// Idle ends a call whose frames stopped without a terminator. Zero
	// selects routing.StreamTimeout, so the chip is released when the routing
	// core frees the reservation and not before or long after.
	Idle time.Duration
	// Log records what happened. Nil logs nothing.
	Log *slog.Logger

	// RadioID is the DMR source every transmission built from USRP audio
	// carries: the gateway's own ID, never a Zello user's (ADR-0064).
	RadioID uint32
	// Talkgroup and Timeslot are what a transmission built from USRP audio
	// is sent on — the transcoder's endpoint in its bridge, so routing
	// recognises it as coming from that bridge.
	Talkgroup uint32
	Timeslot  hbp.Timeslot
	// Deliver hands a built burst to routing. Nil means audio from USRP is
	// received and discarded, which is how a channel is tested one-way.
	Deliver func(frame hbp.Data)

	// GainToUSRPDB and GainToDMRDB scale audio in each direction, in
	// decibels, soft-limited; see Gain. Zero changes nothing.
	GainToUSRPDB float64
	GainToDMRDB  float64

	// Alias is the Talker Alias text sent with every transmission built from
	// USRP audio, empty for none.
	//
	// **Configuration is the only source, per ADR-0064 §3.** A Zello display
	// name is chosen by its user, so an alias derived from one would let a
	// Zello user appear on a licensed operator's repeater under that
	// operator's callsign. This field is administrator-set or empty; nothing
	// in the audio path can reach it.
	//
	// Display data rather than station identification: a receiving radio may
	// not support it, may have it switched off, and a network may strip it.
	// The operator identifies by voice.
	Alias string
}

// Channel carries calls for one transcoder.
type Channel struct {
	name  string
	chip  func() Chip
	radio Radio
	idle  time.Duration
	log   *slog.Logger
	queue chan hbp.Data
	seq   atomic.Uint32

	radioID   uint32
	talkgroup uint32
	timeslot  hbp.Timeslot
	deliver   func(hbp.Data)
	fromUSRP  chan audio.Frame
	toUSRP    Gain
	toDMR     Gain
	// aliasMiddles is the configured Talker Alias, already encoded: one set
	// of four burst middles per PDU, nil when no alias is configured.
	//
	// **Encoded once, here, rather than per call.** The alias is fixed
	// configuration, and a transmission that has to build it is a
	// transmission that can fail on it -- badly, because by then the header
	// has gone out and a radio is listening.
	aliasMiddles [][dmrfec.EmbeddedLCBursts]uint64
	// started is when this channel's counters began: QSP's start.
	started time.Time

	calls, frames, dropped, notVoice, refused, failed, abandoned, fromRadio atomic.Uint64

	// The other direction's counters: transmissions built from USRP, bursts
	// handed to routing, USRP frames dropped because Run was not keeping up,
	// keyups refused, and encoded frames that failed DMR's own FEC check.
	txCalls, txBursts, txDropped, txRefused, txBadFEC, txAbandoned atomic.Uint64
	// txLate counts voice bursts released after their 60 ms slot: an
	// underrun, audible as a gap. It is the number that says whether
	// PacerHeadStart is large enough on this server's Zello traffic.
	txLate atomic.Uint64

	// pace orders and times everything this channel sends. It is touched
	// only on the Run goroutine, like the rest of the call state. See pace.go.
	pace pacer

	mu      sync.Mutex
	problem string
	holder  string
}

// New builds a channel.
func New(opts Options) (*Channel, error) {
	if strings.TrimSpace(opts.Name) == "" {
		return nil, errors.New("vocoderlink: a channel needs a name")
	}
	if opts.Chip == nil {
		return nil, fmt.Errorf("vocoderlink: channel %q has no vocoder", opts.Name)
	}
	if opts.Radio == nil {
		return nil, fmt.Errorf("vocoderlink: channel %q has no USRP socket", opts.Name)
	}
	if opts.Idle <= 0 {
		opts.Idle = routing.StreamTimeout
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if opts.Deliver != nil && opts.RadioID == 0 {
		return nil, fmt.Errorf("vocoderlink: channel %q would build transmissions with no "+
			"source; a frame from radio 0 is not a DMR frame", opts.Name)
	}
	aliasMiddles, err := encodeAlias(opts.Alias)
	if err != nil {
		return nil, fmt.Errorf("vocoderlink: channel %q: %w", opts.Name, err)
	}
	return &Channel{
		name:      opts.Name,
		chip:      opts.Chip,
		radio:     opts.Radio,
		idle:      opts.Idle,
		log:       log.With(slog.String("transcoder", opts.Name)),
		queue:     make(chan hbp.Data, QueueDepth),
		radioID:   opts.RadioID,
		talkgroup: opts.Talkgroup,
		timeslot:  opts.Timeslot,
		deliver:   opts.Deliver,
		fromUSRP:  make(chan audio.Frame, QueueDepth),
		started:   time.Now(),
		toUSRP:    NewGain(opts.GainToUSRPDB),
		toDMR:     NewGain(opts.GainToDMRDB),

		aliasMiddles: aliasMiddles,
	}, nil
}

// encodeAlias turns configured alias text into burst middles, one set per PDU.
//
// **7-bit, and there is no choice to make.** An operator setting an alias is
// answering "what should radios show", not "which of table 7.25's four
// encodings", and the encodings decide it anyway: 7-bit carries the most
// characters -- 31, the most the length element can even state -- and is what a
// radio sends for plain text.
//
// **Non-ASCII is refused rather than encoded.** The multi-byte formats are 8
// bits per character, and dmrfec will not encode non-ASCII at that width
// because §7.2.19 calls the length element bytes while its own table 7.26 calls
// it characters: the two differ for multi-byte text, and a radio told the wrong
// number shows a truncated or padded alias. Refusing at startup is a
// configuration error the operator can read; guessing is an alias that looks
// wrong on somebody else's radio and cannot be diagnosed from here.
func encodeAlias(alias string) ([][dmrfec.EmbeddedLCBursts]uint64, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, nil
	}
	pdus, err := dmrfec.TalkerAliasPDUs(alias, dmrfec.TalkerAlias7Bit)
	if err != nil {
		return nil, fmt.Errorf("talker alias %q: %w", alias, err)
	}
	out := make([][dmrfec.EmbeddedLCBursts]uint64, 0, len(pdus))
	for i, pdu := range pdus {
		middles, err := dmrfec.EmbeddedLCMiddles(pdu, GeneratedColourCode)
		if err != nil {
			return nil, fmt.Errorf("talker alias %q, PDU %d of %d: %w", alias, i+1, len(pdus), err)
		}
		out = append(out, middles)
	}
	return out, nil
}

// SupervisedChip adapts a supervisor to Options.Chip.
//
// ClientFor returns a nil *ambe.Client when the vocoder is down, and a nil
// pointer stored in an interface is not a nil interface: the channel would
// call Acquire on it and panic on the first call after AMBEserver stopped.
func SupervisedChip(s *ambe.Supervisor, name string) func() Chip {
	return func() Chip {
		if c := s.ClientFor(name); c != nil {
			return c
		}
		return nil
	}
}

// Send queues a frame without blocking.
func (c *Channel) Send(frame hbp.Data) error {
	select {
	case c.queue <- frame:
		return nil
	default:
		c.dropped.Add(1)
		return ErrQueueFull
	}
}

// call is the transmission a channel is carrying.
type call struct {
	stream    hbp.StreamID
	source    uint32
	talkgroup uint32
	lastSeen  time.Time
	// chip is nil when the call could not get one, or lost it; the rest of
	// the transmission is then absorbed without a line per frame.
	chip Chip
}

// Run carries calls until ctx is done. A call in progress is closed with a
// USRP release on the way out, so the far side is not left keyed.
func (c *Channel) Run(ctx context.Context) {
	go c.drainRadio(ctx)

	tick := time.NewTicker(c.idle / 4)
	defer tick.Stop()

	// The pacing timer, armed to the next frame due and idle otherwise.
	pace := time.NewTimer(time.Hour)
	pace.Stop()
	defer pace.Stop()
	arm := func(now time.Time) {
		if when, ok := c.pace.due(); ok {
			pace.Reset(max(when.Sub(now), 0))
		}
	}

	// **Both directions' state lives on this goroutine and nowhere else**, so
	// "is the chip held by the other direction" is a plain read, not a race.
	var cur *call
	var tx *outbound
	for {
		select {
		case <-ctx.Done():
			c.finish(cur, "shutting down")
			c.endOutbound(tx, "shutting down")
			// **Everything queued goes now, terminator included.** A repeater
			// sent a header and no terminator stays keyed, and waiting out
			// the cadence during shutdown is waiting on a process that is
			// leaving.
			c.flushPaced()
			return
		case now := <-pace.C:
			c.releasePaced(now)
		case f := <-c.queue:
			if tx != nil && tx.chip != nil && !f.IsUserData() {
				// A Zello user holds the chip. The DMR call is refused once,
				// at its first frame, and absorbed after that.
				if cur == nil || cur.stream != f.StreamID {
					c.refused.Add(1)
					c.note("a DMR call arrived while audio from USRP held the vocoder")
					cur = &call{stream: f.StreamID, source: f.SourceID, talkgroup: f.TargetID}
				}
				cur.lastSeen = time.Now()
				continue
			}
			cur = c.handle(cur, f, time.Now())
		case f := <-c.fromUSRP:
			tx = c.handleUSRP(tx, cur, f, time.Now())
			c.releasePaced(time.Now())
		case now := <-tick.C:
			if cur != nil && now.Sub(cur.lastSeen) > c.idle {
				if cur.chip != nil {
					c.abandoned.Add(1)
					c.finish(cur, "the call stopped without a terminator")
				}
				cur = nil
			}
			if tx != nil && now.Sub(tx.lastSeen) > c.idle {
				if tx.chip != nil {
					c.txAbandoned.Add(1)
				}
				c.endOutbound(tx, "USRP audio stopped without a release")
				tx = nil
			}
		}
		arm(time.Now())
	}
}

// releasePaced delivers every frame the pacer says is due by now.
func (c *Channel) releasePaced(now time.Time) {
	out, late := c.pace.release(now)
	c.txLate.Add(uint64(late))
	for _, f := range out {
		c.deliver(f)
	}
}

// flushPaced delivers everything queued, without waiting for its slot.
func (c *Channel) flushPaced() {
	for _, f := range c.pace.flush() {
		c.deliver(f)
	}
}

// drainRadio receives what the far side sends and queues it for Run.
//
// A full queue drops the frame and counts it rather than blocking, for the
// same reason Send does: the socket buffer behind it would fill instead, and
// the loss would be the kernel's and invisible.
func (c *Channel) drainRadio(ctx context.Context) {
	for {
		f, err := c.radio.Receive(ctx)
		if err != nil {
			if ctx.Err() == nil {
				c.note(fmt.Sprintf("the USRP socket stopped receiving: %v", err))
			}
			return
		}
		c.fromRadio.Add(1)
		select {
		case c.fromUSRP <- f:
		default:
			c.txDropped.Add(1)
		}
	}
}

func (c *Channel) handle(cur *call, f hbp.Data, now time.Time) *call {
	// A text message is not audio and never reaches the chip: its bursts are
	// rate-coded blocks, and decoding them produces noise at full scale.
	if f.IsUserData() || f.FrameType == hbp.FrameTypeReserved {
		c.notVoice.Add(1)
		return cur
	}

	// **A different transmission means the last one ended unannounced.** The
	// routing core refuses a second call while one holds this chip, so a new
	// stream reaching here is one the core let through after the first went
	// silent — its terminator was lost, and it still holds the far side keyed.
	if cur != nil && (f.StreamID != cur.stream || f.SourceID != cur.source) {
		if cur.chip != nil {
			c.abandoned.Add(1)
		}
		c.finish(cur, "another transmission began before this one's terminator arrived")
		cur = nil
	}

	if f.IsTerminator() {
		c.finish(cur, "")
		return nil
	}

	if cur == nil {
		// A voice header opens a call, and so does a voice burst when the
		// header was lost: late entry is ordinary on a radio network.
		cur = c.start(f, now)
	}
	cur.lastSeen = now

	if cur.chip == nil || f.FrameType == hbp.FrameTypeSync {
		return cur
	}

	// The payload is a fixed 33 bytes, so this cannot refuse; the check stays
	// because VocoderFrames is the one place that says what a burst is.
	frames, ok := dmrfec.VocoderFrames(f.Payload[:])
	if !ok {
		c.fail(cur, "a voice burst is not a DMR burst")
		return cur
	}
	for _, bits := range frames {
		reply, err := cur.chip.Decode(ambe.ChannelFrame{Bits: dmrfec.ProtectedBits, Data: packBits(bits)})
		if err != nil {
			c.fail(cur, fmt.Sprintf("decoding a frame: %v", err))
			return cur
		}
		if len(reply.Samples) != audio.SamplesPerFrame {
			c.fail(cur, fmt.Sprintf("the vocoder returned %d samples for a 20 ms frame, want %d",
				len(reply.Samples), audio.SamplesPerFrame))
			return cur
		}
		if err := c.radio.Send(audio.Frame{Sequence: c.seq.Add(1), PTT: true,
			Talkgroup: cur.talkgroup, Samples: c.toUSRP.Apply(reply.Samples)}); err != nil {
			c.fail(cur, fmt.Sprintf("sending audio: %v", err))
			return cur
		}
		c.frames.Add(1)
	}
	return cur
}

// start opens a call: acquire the chip, then key the far side.
func (c *Channel) start(f hbp.Data, now time.Time) *call {
	cur := &call{stream: f.StreamID, source: f.SourceID, talkgroup: f.TargetID, lastSeen: now}

	chip := c.chip()
	if chip == nil {
		c.refused.Add(1)
		c.note("a call arrived and the vocoder is not reachable")
		return cur
	}
	// **The rate is checked before a frame is sent, not inferred from bad
	// audio.** A DMR burst carries 72-bit frames; a chip at any other rate
	// accepts them and produces sound nothing identifies as a rate problem.
	if bits, ok := ambe.FrameBitsForRate(chip.Rate()); !ok || bits != dmrfec.ProtectedBits {
		c.refused.Add(1)
		c.note(fmt.Sprintf("the vocoder is at rate index %d, which does not carry DMR's %d-bit frames",
			chip.Rate(), dmrfec.ProtectedBits))
		return cur
	}
	if err := chip.Acquire(ambe.Holder{Talkgroup: f.TargetID, Source: f.SourceID,
		Reason: "DMR to USRP"}); err != nil {
		// The routing core already refuses a second call, so this is the two
		// layers disagreeing — worth a count of its own, not a quiet retry.
		c.refused.Add(1)
		c.note(fmt.Sprintf("the vocoder refused a call: %v", err))
		return cur
	}
	if err := c.radio.Send(audio.Frame{Sequence: c.seq.Add(1), PTT: true, Talkgroup: f.TargetID}); err != nil {
		chip.Release()
		c.failed.Add(1)
		c.note(fmt.Sprintf("cannot key the USRP side: %v", err))
		return cur
	}
	cur.chip = chip
	c.calls.Add(1)
	c.setHolder(fmt.Sprintf("radio %d on talkgroup %d", f.SourceID, f.TargetID))
	c.log.Info("transcoding a call",
		slog.Uint64("radio", uint64(f.SourceID)),
		slog.Uint64("talkgroup", uint64(f.TargetID)))
	return cur
}

// finish closes a call. **The release is sent before the chip is freed**, so a
// far side never sees audio from the next call while still keyed for this one.
func (c *Channel) finish(cur *call, why string) {
	if cur == nil || cur.chip == nil {
		return
	}
	if err := c.radio.Send(audio.Frame{Sequence: c.seq.Add(1), PTT: false, Talkgroup: cur.talkgroup}); err != nil {
		c.failed.Add(1)
		c.note(fmt.Sprintf("cannot unkey the USRP side: %v", err))
	}
	cur.chip.Release()
	cur.chip = nil
	c.setHolder("")
	if why != "" {
		c.note(why)
	}
}

// fail ends a call's audio and keeps absorbing its frames.
func (c *Channel) fail(cur *call, why string) {
	c.failed.Add(1)
	c.finish(cur, why)
}

func (c *Channel) note(problem string) {
	c.mu.Lock()
	c.problem = problem
	c.mu.Unlock()
	c.log.Warn("transcoder problem", slog.String("problem", problem))
}

func (c *Channel) setHolder(h string) {
	c.mu.Lock()
	c.holder = h
	c.mu.Unlock()
}

// packBits turns one-bit-per-byte values into bytes, most significant first.
func packBits(bits []byte) []byte {
	out := make([]byte, (len(bits)+7)/8)
	for i, b := range bits {
		if b&1 == 1 {
			out[i/8] |= 1 << uint(7-i%8)
		}
	}
	return out
}

// Set routes a transcoder name to its channel. It satisfies the peers
// listener's TranscoderSender.
type Set struct {
	channels map[string]*Channel
}

// NewSet builds a set. Names are matched as configuration matches them:
// trimmed and without regard to case, so a bridge naming "DVstick" reaches the
// transcoder named "dvstick" that validation already said it reaches.
func NewSet(channels ...*Channel) *Set {
	s := &Set{channels: make(map[string]*Channel, len(channels))}
	for _, ch := range channels {
		s.channels[key(ch.name)] = ch
	}
	return s
}

func key(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Send queues a frame for the named channel.
func (s *Set) Send(transcoder string, frame hbp.Data) error {
	ch, ok := s.channels[key(transcoder)]
	if !ok {
		return fmt.Errorf("no transcoder channel is running for %q", transcoder)
	}
	return ch.Send(frame)
}

// Run runs every channel and returns when all have stopped.
func (s *Set) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, ch := range s.channels {
		wg.Go(func() { ch.Run(ctx) })
	}
	wg.Wait()
}
