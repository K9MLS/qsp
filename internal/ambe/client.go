package ambe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A client for an AMBEserver holding one AMBE-3000F.
//
// # What this is for
//
// [ADR-0062] decided that as much as possible is built into QSP, and that the
// vocoder is one of the two things that stay outside: AMBEserver already owns
// the serial port and serves the chip on a socket, which is what keeps the
// operator's hardware available to the operator. So this is the piece QSP
// builds — a UDP client speaking the AMBE-3000 packet format to a vocoder it
// does not own, per [ADR-0061].
//
// The exchange it is built on was captured on 2026-09-14 and lives in
// testdata/ambe/observed-exchanges.hex. A 20 ms speech packet of 160 linear
// samples produced a channel packet carrying 72 bits, which is 3600 bps, which
// is DMR.
//
// # One chip is one channel
//
// **A second simultaneous call is refused, not interleaved.** BLUEPRINT §7 is
// explicit that capacity is a first-class concept rather than an afterthought,
// and the reason is the same one contention exists for everywhere else in this
// server: two people's audio through one channel is audio nobody can
// understand. Acquire takes the channel and names its holder; a second Acquire
// fails with a reason an operator can read.
//
// # Request and response, strictly in order
//
// Packet mode is request and response: the chip replies to every packet, in
// the order the packets arrived (§5.3). Its receive queue holds two speech and
// two channel packets, so pipelining is possible — and this client does not do
// it, because a reply carries nothing identifying which request it answers.
// One exchange at a time, under a mutex, is the only version of this that can
// attribute a frame to a caller.
type Client struct {
	conn    net.Conn
	timeout time.Duration
	log     *slog.Logger
	now     func() time.Time

	// mu serialises exchanges. See the note about ordering above.
	mu sync.Mutex

	// What the chip said about itself at startup, for the health report.
	product string
	version string
	cfg     [3]byte
	rate    int

	// holder is the call currently occupying the channel, or nil.
	holder atomic.Pointer[Holder]

	encoded atomic.Uint64
	decoded atomic.Uint64
	refused atomic.Uint64
	failed  atomic.Uint64

	// broken is set by the first exchange that fails: a vocoder that did not
	// answer once is not trusted with the next call. The supervisor closes a
	// broken client and opens a new one with the whole handshake.
	broken atomic.Bool
}

// Holder names the call occupying the vocoder.
//
// It exists so that a refusal can say what is already on the channel. A count
// on its own cannot answer the question an operator actually has, which this
// project learned from COLLISIONS on 2026-09-12.
type Holder struct {
	Talkgroup uint32
	Source    uint32
	Since     time.Time
	// Reason is what asked for the channel: a bridge name, a peer, a
	// direction. Free text because the caller knows more than this package
	// does.
	Reason string
}

// String names a holder for a log line or a console.
func (h Holder) String() string {
	if h.Source == 0 && h.Talkgroup == 0 {
		return h.Reason
	}
	return fmt.Sprintf("%s (radio %d on talkgroup %d)", h.Reason, h.Source, h.Talkgroup)
}

// Options configures a Client. The zero value is usable.
type Options struct {
	// Timeout bounds one exchange. Zero selects DefaultTimeout.
	Timeout time.Duration
	// Rate is the built-in rate index to set at startup. Zero selects
	// RateIndexDMR.
	Rate int
	// Now is the clock, for tests. Nil selects time.Now.
	Now func() time.Time
}

// DefaultTimeout bounds one exchange with the chip.
//
// **Generous on purpose, and the manual says why.** A hard reset takes up to
// 20 ms to produce PKT_READY and a soft reset about 7 ms (§3.6.1), and an
// encode is one 20 ms frame of work. So the chip's own latencies are tens of
// milliseconds and everything above that is the network and AMBEserver's own
// scheduling. Half a second is long enough that a timeout means something is
// wrong rather than busy, and short enough that a dead vocoder does not stall
// a caller for a noticeable time.
const DefaultTimeout = 500 * time.Millisecond

// Errors a caller can act on.
var (
	// ErrBusy is returned when the channel is already held. The message names
	// the holder.
	ErrBusy = errors.New("ambe: the vocoder is already carrying a call")
	// ErrNotHeld is returned when a frame is offered without the channel.
	ErrNotHeld = errors.New("ambe: the vocoder channel was not acquired")
	// ErrParityEnabled is returned at startup when the chip's PARITY_ENABLE
	// pin is set.
	ErrParityEnabled = errors.New("ambe: the chip has parity enabled and this client sends none")
	// ErrNotAnAMBE3000F is returned when whatever answered is not the part
	// this client speaks to.
	ErrNotAnAMBE3000F = errors.New("ambe: the product identifier is not an AMBE-3000")
)

// Open dials an AMBEserver and brings the chip to a known state.
//
// The startup sequence, in order, and every step of it is an exchange this
// project has run against real hardware:
//
//  1. PKT_RESET, answered by PKT_READY. The chip loses all prior
//     configuration and re-reads its pins, so this is what makes the state
//     below knowable rather than inherited from whatever ran before.
//  2. PKT_PRODID and PKT_VERSTRING, which are the manual's own recommendation
//     for proving the link works: two packets with known replies (§6.6.1).
//  3. PKT_GETCFG, because three things that matter are pin-settable and none
//     of them should be assumed. See below.
//  4. PKT_RATET, because the rate has to be set. On the operator's board every
//     RATE pin reads low, so it does not boot at the DMR rate — this is a
//     precondition for audio and not a refinement of it.
//
// **Parity is a refusal rather than a warning.** Parity is enabled by default
// (§6.5.5) and a chip with it enabled silently discards every packet that
// lacks a valid parity field. This client sends none. If the pin is set,
// nothing would work and the failure would look like a dead dongle, so Open
// says so instead. The operator's DVstick 30 ties that pin low, which is why
// stock AMBEserver drives it at all.
//
// **Companding is reported rather than refused**, because it changes what a
// speech packet may contain — 8-bit companded samples rather than 16-bit
// linear — and the caller that supplies samples is the one that can act on it.
func Open(ctx context.Context, log *slog.Logger, address string, opts Options) (*Client, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	rate := opts.Rate
	if rate == 0 {
		rate = RateIndexDMR
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", address)
	if err != nil {
		return nil, fmt.Errorf("ambe: cannot reach a vocoder at %s: %w", address, err)
	}

	c := &Client{conn: conn, timeout: timeout, log: log, now: now, rate: rate}

	if err := c.handshake(rate); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

// handshake runs the startup sequence. See Open.
func (c *Client) handshake(rate int) error {
	ready, err := c.exchange(MustBuild(TypeControl, Val(0x33)))
	if err != nil {
		return fmt.Errorf("ambe: the vocoder did not answer a reset: %w", err)
	}
	if len(ready) < 5 || ready[4] != 0x39 {
		return fmt.Errorf("ambe: a reset was answered by %x rather than a ready packet", ready)
	}

	reply, err := c.exchange(MustBuild(TypeControl, Val(0x30)))
	if err != nil {
		return fmt.Errorf("ambe: the vocoder did not answer a product query: %w", err)
	}
	_, product, ok := TextFromResponse(reply)
	if !ok {
		return fmt.Errorf("ambe: a product query was answered by %x", reply)
	}
	if !strings.HasPrefix(product, "AMBE3000") {
		return fmt.Errorf("%w: %q answered", ErrNotAnAMBE3000F, product)
	}
	c.product = product

	reply, err = c.exchange(MustBuild(TypeControl, Val(0x31)))
	if err != nil {
		return fmt.Errorf("ambe: the vocoder did not answer a version query: %w", err)
	}
	if _, version, ok := TextFromResponse(reply); ok {
		c.version = version
	}

	reply, err = c.exchange(MustBuild(TypeControl, Val(0x36)))
	if err != nil {
		return fmt.Errorf("ambe: the vocoder did not answer a configuration query: %w", err)
	}
	cfg, ok := ConfigFromResponse(reply)
	if !ok {
		return fmt.Errorf("ambe: a configuration query was answered by %x", reply)
	}
	c.cfg = cfg
	if ParityEnabledIn(cfg) {
		return fmt.Errorf("%w (CFG2 %#02x); every packet would be discarded and "+
			"it would look like a dead device", ErrParityEnabled, cfg[2])
	}

	reply, err = c.exchange(MustBuild(TypeControl, Val(0x09, byte(rate))))
	if err != nil {
		return fmt.Errorf("ambe: the vocoder did not answer a rate index: %w", err)
	}
	field, status, ok := AckedField(reply)
	if !ok || field != 0x09 || status != 0x00 {
		return fmt.Errorf("ambe: rate index %d was answered by %x rather than accepted", rate, reply)
	}

	if c.log != nil {
		c.log.Info("vocoder ready",
			"product", c.product,
			"version", c.version,
			"mode", Mode(cfg),
			"companding", CompandingEnabledIn(cfg),
			"boot_rate_control_word", RateControlWordIn(cfg),
			"rate_index", rate)
	}
	return nil
}

// Product, Version and Config report what the chip said about itself at
// startup, for a health report.
func (c *Client) Product() string { return c.product }

// Version reports the chip's version string.
func (c *Client) Version() string { return c.version }

// Config reports the configuration pins as latched at the startup reset.
func (c *Client) Config() [3]byte { return c.cfg }

// CompandingEnabled reports whether the chip expects companded samples rather
// than 16-bit linear.
func (c *Client) CompandingEnabled() bool { return CompandingEnabledIn(c.cfg) }

// Acquire takes the single vocoder channel.
//
// **A second caller is refused and told what holds it.** One AMBE-3000 is one
// channel, and a club bridge wanting four simultaneous transcoded talkgroups
// needs four chips rather than one working harder (BLUEPRINT §7).
//
// It also clears the chip's vocoder state, per §4.4: a PKT_INIT between
// unrelated audio streams stops a new call decoding with state left by the
// last one. That matters here precisely because one chip serves consecutive
// transmissions from different radios.
func (c *Client) Acquire(h Holder) error {
	h.Since = c.now()
	if !c.holder.CompareAndSwap(nil, &h) {
		c.refused.Add(1)
		held := c.holder.Load()
		if held == nil {
			// Released between the swap and the load; the caller may retry.
			return fmt.Errorf("%w", ErrBusy)
		}
		return fmt.Errorf("%w: %s since %s", ErrBusy, held,
			held.Since.Format("15:04:05"))
	}

	// Encoder, decoder and echo canceller all initialised: Table 48's 0x07.
	// **The rate is set again at the start of every call, before the init.**
	// The handshake set it once, when the channel opened; a chip or an
	// AMBEserver that restarted since is back at its default rate, and nothing
	// fails — every DMR frame is simply decoded at the wrong rate into
	// garbled audio. That happened on production on 2026-09-16, when an
	// AMBEserver was restarted under a QSP that never noticed. One exchange a
	// call, in the handshake's own order: rate, then init.
	reply, err := c.exchange(MustBuild(TypeControl, Val(0x09, byte(c.rate))))
	if err == nil {
		if field, status, ok := AckedField(reply); !ok || field != 0x09 || status != 0x00 {
			err = fmt.Errorf("rate index %d was answered by %x rather than accepted", c.rate, reply)
			c.broken.Store(true)
		}
	}
	if err != nil {
		c.holder.Store(nil)
		c.failed.Add(1)
		return fmt.Errorf("ambe: cannot set the rate for a new call: %w", err)
	}
	if _, err := c.exchange(MustBuild(TypeControl, Val(0x0B, 0x07))); err != nil {
		c.holder.Store(nil)
		c.failed.Add(1)
		return fmt.Errorf("ambe: cannot initialise the vocoder for a new call: %w", err)
	}
	return nil
}

// Broken reports whether an exchange with this vocoder has failed. A broken
// client is not handed to a new call; see Supervisor.
func (c *Client) Broken() bool { return c.broken.Load() }

// Release gives the channel up. It is safe to call without holding it.
func (c *Client) Release() { c.holder.Store(nil) }

// Holder reports the call occupying the channel, or nil.
func (c *Client) Holder() *Holder { return c.holder.Load() }

// Encode turns one 20 ms frame of PCM into a compressed frame.
//
// This is the exchange observed on 2026-09-14: 160 linear samples in as a
// speech packet, a channel packet of 72 bits back. The bit count is returned
// as the chip reported it rather than assumed, because the frame size is the
// only evidence that the rate took effect — an acknowledgement says a field
// arrived and nothing more.
func (c *Client) Encode(samples []int16) (ChannelFrame, error) {
	if c.holder.Load() == nil {
		return ChannelFrame{}, ErrNotHeld
	}
	speech, err := SpeechD(samples)
	if err != nil {
		return ChannelFrame{}, err
	}
	pkt, err := Build(TypeSpeech, Val(0x40), speech)
	if err != nil {
		return ChannelFrame{}, err
	}

	reply, err := c.exchange(pkt)
	if err != nil {
		c.failed.Add(1)
		return ChannelFrame{}, fmt.Errorf("ambe: encoding a frame: %w", err)
	}
	frame, ok := ChannelFrameFromResponse(reply)
	if !ok {
		c.failed.Add(1)
		return ChannelFrame{}, fmt.Errorf("ambe: a speech packet was answered by %x, "+
			"which is not a channel frame", reply)
	}
	c.encoded.Add(1)
	return frame, nil
}

// Decode turns one compressed frame into 20 ms of PCM.
//
// **The framing is proved and the audio is not.** On 2026-09-14 the dongle's
// own channel frame was sent back with `ambe-probe -decode` and a 326-byte
// output speech packet came out, 160 samples, correctly framed. Its peak
// sample was 3, where the frame had encoded a 1 kHz tone at amplitude 8000 —
// so something produced comfort noise or nothing.
//
// Four things look identical in the samples: comfort noise, a frame repeat, a
// tone frame decoded out of context, and a decoder that has not ramped up. The
// reply's DCMODE_OUT flags distinguish three of them, which is why Decode
// returns them rather than only the samples, and why AskForDecoderFlags exists.
// **Until that reading is taken, nothing should depend on decoded audio.**
func (c *Client) Decode(frame ChannelFrame) (SpeechReply, error) {
	if c.holder.Load() == nil {
		return SpeechReply{}, ErrNotHeld
	}
	chand, err := Chand(frame.Bits, frame.Data)
	if err != nil {
		return SpeechReply{}, err
	}
	pkt, err := Build(TypeChannel, Val(0x40), chand)
	if err != nil {
		return SpeechReply{}, err
	}

	reply, err := c.exchange(pkt)
	if err != nil {
		c.failed.Add(1)
		return SpeechReply{}, fmt.Errorf("ambe: decoding a frame: %w", err)
	}
	speech, ok := SpeechReplyFromResponse(reply)
	if !ok {
		c.failed.Add(1)
		return SpeechReply{}, fmt.Errorf("ambe: a channel packet was answered by %x, "+
			"which is not a speech frame", reply)
	}
	c.decoded.Add(1)
	return speech, nil
}

// AskForDecoderFlags configures the chip to report DCMODE_OUT in every output
// speech packet, so that Decode can say what the decoder did rather than
// leaving a caller to infer it from near-silent samples.
//
// PKT_SPCHFMT, field 0x16, Table 65. It is a separate call rather than part of
// Open because it changes the shape of every subsequent speech reply, and a
// caller that does not read the flags should not be paying three bytes a frame
// for them.
func (c *Client) AskForDecoderFlags() error {
	reply, err := c.exchange(MustBuild(TypeControl, Val(0x16, SpchFmtAlwaysDCMode...)))
	if err != nil {
		return fmt.Errorf("ambe: cannot ask for the decoder flags: %w", err)
	}
	field, status, ok := AckedField(reply)
	if !ok || field != 0x16 || status != 0x00 {
		return fmt.Errorf("ambe: PKT_SPCHFMT was answered by %x rather than accepted", reply)
	}
	return nil
}

// Counters reports what the link has carried, for a health report.
func (c *Client) Counters() (encoded, decoded, refused, failed uint64) {
	return c.encoded.Load(), c.decoded.Load(), c.refused.Load(), c.failed.Load()
}

// Close releases the socket.
func (c *Client) Close() error {
	c.Release()
	return c.conn.Close()
}

// exchange sends one packet and reads its reply, under the mutex.
func (c *Client) exchange(out []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.send(out)
}

// send is the wire exchange. Caller holds mu.
func (c *Client) send(out []byte) ([]byte, error) {
	if err := c.conn.SetDeadline(c.now().Add(c.timeout)); err != nil {
		c.broken.Store(true)
		return nil, err
	}
	if _, err := c.conn.Write(out); err != nil {
		c.broken.Store(true)
		return nil, err
	}
	buf := make([]byte, 2048)
	n, err := c.conn.Read(buf)
	if err != nil {
		c.broken.Store(true)
		return nil, err
	}
	return buf[:n], nil
}
