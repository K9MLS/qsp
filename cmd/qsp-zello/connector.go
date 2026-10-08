//go:build zello

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k9mls/qsp/internal/audio"
	"github.com/k9mls/qsp/internal/routing"
	"github.com/k9mls/qsp/internal/zello"
	"github.com/k9mls/qsp/internal/zellobridge"
	"github.com/k9mls/qsp/internal/zellologon"
)

// session is what the connector needs from a Zello connection.
type session interface {
	zellobridge.Stream
	Done() <-chan struct{}
	Close() error
}

// bridge is what the connector needs from zellobridge.
type bridge interface {
	RadioKeyup() error
	RadioFrame(pcm []int16) error
	RadioRelease() error
	ZelloPacket(p zello.IncomingPacket) error
	ZelloStreamStopped(streamID uint32) error
	Close()
}

// States an operator reads on /healthz. Each names the action it needs.
const (
	stateStarting           = "starting"
	stateConnected          = "connected"
	stateQSPUnreachable     = "qsp_unreachable"     // is QSP running, with zello.logon_socket set?
	stateCredentialsMissing = "credentials_missing" // enter them in QSP's console
	stateCredentialsBad     = "credentials_unusable"
	stateZelloRefused       = "zello_refused"     // Zello said no: credentials or channel membership
	stateZelloUnreachable   = "zello_unreachable" // network, or Zello is down
)

// idle ends a transmission from QSP whose release never came. It matches
// QSP's routing.StreamTimeout: QSP has given the call up by then too.
const idle = 2 * time.Second

// zelloIdle ends a Zello stream whose on_stream_stop never came, by sending
// QSP the release the stop would have sent.
//
// **It must fire before QSP's own timer, so it is shorter, not equal.** QSP
// ends a transmission from USRP that is silent for routing.StreamTimeout and
// records "USRP audio stopped without a release" -- a line that means this
// program crashed or lost the socket. When both timers were two seconds, on
// ticks of the same length, QSP's fired first about half the time and blamed
// a fault that had not happened, for an over this side was about to release.
// A second of silence is already seventeen missed packets, so nothing that is
// still a transmission is cut short; and if audio does resume on the same
// stream, the bridge keys up again on its first packet.
const zelloIdle = time.Second

// pumpTick is how often pump looks for a transmission that went quiet, so a
// timeout is acted on up to this long after it passes.
const pumpTick = idle / 4

// The relationship above, checked by the compiler: the latest this side can
// release -- the timeout plus a whole tick -- with half a second in hand for
// the release to cross the socket and wait its turn in QSP's queue. A change
// to either constant that breaks it is a negative constant converted to an
// unsigned type, which does not build.
const _ = uint64(routing.StreamTimeout - (zelloIdle + pumpTick) - 500*time.Millisecond)

// connector owns the loop. Its dependencies are fields so a test can replace
// the network at each boundary without replacing the logic between them.
type connector struct {
	cfg   Config
	log   *slog.Logger
	fetch func(ctx context.Context) (zellologon.Logon, error)
	dial  func(ctx context.Context, opts zello.Options) (session, error)
	newBr func(s session, r zellobridge.Radio) (bridge, error)

	mu        sync.Mutex
	state     string
	detail    string
	channel   string // as QSP last handed it out
	since     time.Time
	connected atomic.Uint64
	discarded atomic.Uint64 // USRP frames that arrived with no Zello session
	// lost reads how many frames the queue from QSP gave up because it was
	// full, and is nil in a test that does not ask.
	lost func() uint64
}

func run(ctx context.Context, cfg Config, log *slog.Logger) error {
	conn, err := audio.Listen(cfg.USRPListen, cfg.USRPPeer)
	if err != nil {
		return fmt.Errorf("opening the USRP socket: %w", err)
	}
	defer conn.Close()

	c := &connector{
		cfg:   cfg,
		log:   log,
		fetch: func(ctx context.Context) (zellologon.Logon, error) { return zellologon.Fetch(ctx, cfg.LogonSocket) },
		dial: func(ctx context.Context, opts zello.Options) (session, error) {
			s, err := zello.Connect(ctx, opts)
			if err != nil {
				return nil, err
			}
			return s, nil
		},
		newBr: func(s session, r zellobridge.Radio) (bridge, error) {
			return zellobridge.New(zellobridge.Options{Stream: s, Radio: r, Log: log})
		},
	}
	c.setState(stateStarting, "")

	if h := cfg.HealthListen; h != "" {
		srv := &http.Server{Addr: h, Handler: c.healthHandler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Warn("health endpoint stopped", slog.String("error", err.Error()))
			}
		}()
		defer srv.Close()
	}

	frames := audio.NewQueue(usrpQueue)
	c.lost = frames.Lost
	go receive(ctx, conn, frames, log)
	return c.loop(ctx, frames, &usrpRadio{conn: conn})
}

// usrpQueue is how many frames from QSP wait for the pump: five seconds.
const usrpQueue = 256

// receive reads USRP for the connector's whole life, not per session, so a
// reconnection never leaves the socket unread and its buffer full of stale
// audio that would play the moment Zello came back.
//
// **A full queue gives up its oldest audio and never a keyup or a release**
// (audio.Queue). It fills only when the pump has been stuck for five seconds,
// which is a stalled connection to Zello, and that is said once each time it
// happens rather than once a frame: see losses.
func receive(ctx context.Context, conn *audio.Conn, out *audio.Queue, log *slog.Logger) {
	var l losses
	for {
		f, err := conn.Receive(ctx)
		if err != nil {
			return
		}
		if l.pushed(out.Push(f), time.Now()) {
			log.Warn("audio from QSP is arriving faster than it reaches Zello; the oldest is being dropped")
		}
	}
}

// lossQuiet is how long without a dropped frame ends one spell of them.
const lossQuiet = 5 * time.Second

// losses decides when dropping audio is news.
//
// **Once a spell, not once a change.** This said it whenever a frame was lost
// after one that was not, and a full queue does exactly that: each push drops
// the oldest, the pump takes one, the next push fits. 93 frames lost were 93
// warnings (2026-10-07, H3). A spell now ends after lossQuiet with nothing
// lost.
type losses struct {
	last time.Time
}

// pushed records whether a push dropped a frame, and reports whether that
// begins a spell.
func (l *losses) pushed(lost bool, now time.Time) bool {
	if !lost {
		return false
	}
	begins := l.last.IsZero() || now.Sub(l.last) > lossQuiet
	l.last = now
	return begins
}

// loop connects, carries audio until the session ends, and connects again.
func (c *connector) loop(ctx context.Context, frames *audio.Queue, radio zellobridge.Radio) error {
	failures := 0
	for ctx.Err() == nil {
		err := c.once(ctx, frames, radio)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		state, wait := classify(err, failures)
		if state == stateConnected {
			// A session that ran and then ended is not a failure to back off
			// from; reconnect promptly and start counting again.
			failures = 0
		} else {
			failures++
		}
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		c.setState(state, detail)
		c.log.Warn("not connected to Zello", slog.String("state", state),
			slog.String("reason", detail), slog.Duration("retry_in", wait))
		if err := c.drainFor(ctx, frames, wait); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// once is one logon and one session. A nil error is a session that connected
// and later ended.
func (c *connector) once(ctx context.Context, frames *audio.Queue, radio zellobridge.Radio) error {
	logon, err := c.fetch(ctx)
	if err != nil {
		return err
	}
	s, err := c.dial(ctx, zello.Options{
		Endpoint: c.cfg.Endpoint, AuthToken: logon.Token,
		Username: logon.Username, Password: logon.Password,
		Channel: logon.Channel, Log: c.log,
	})
	if err != nil {
		return err
	}
	defer s.Close()
	br, err := c.newBr(s, radio)
	if err != nil {
		return fmt.Errorf("building the audio bridge: %w", err)
	}
	defer br.Close()

	c.connected.Add(1)
	c.mu.Lock()
	c.channel = logon.Channel
	c.mu.Unlock()
	c.setState(stateConnected, "")
	c.log.Info("connected to Zello", slog.String("channel", logon.Channel))
	// **What QSP sent while the logon was in progress is not played.** Nothing
	// empties the queue during the logon, so it would reach the channel the
	// moment the session opened, as much as five seconds late. An over still
	// in progress loses its keyup here and no more: the pump opens a stream
	// on the first audio it is given.
	c.discarded.Add(uint64(frames.Discard()))
	c.pump(ctx, s, br, frames)
	return nil
}

// pump carries audio for one session, on one goroutine, so the bridge's two
// directions are never driven concurrently from here.
func (c *connector) pump(ctx context.Context, s session, br bridge, frames *audio.Queue) {
	tick := time.NewTicker(pumpTick)
	defer tick.Stop()

	var (
		toZello        bool // a transmission from QSP is open toward Zello
		lastUSRP       time.Time
		toRadio        bool // a Zello stream is being played toward QSP
		zelloStream    uint32
		lastZelloAudio time.Time
	)
	release := func() {
		if toZello {
			if err := br.RadioRelease(); err != nil {
				c.log.Warn("cannot close the Zello stream", slog.String("error", err.Error()))
			}
			toZello = false
		}
	}
	stopRadio := func() {
		if toRadio {
			if err := br.ZelloStreamStopped(zelloStream); err != nil {
				c.log.Warn("cannot end the transmission toward QSP", slog.String("error", err.Error()))
			}
			toRadio = false
		}
	}
	play := func(p zello.IncomingPacket) {
		lastZelloAudio = time.Now()
		zelloStream, toRadio = p.StreamID, true
		if err := br.ZelloPacket(p); err != nil {
			c.log.Warn("cannot play Zello audio toward QSP", slog.String("error", err.Error()))
		}
	}
	// **Both directions are closed on the way out**, whatever ended the
	// session: a Zello stream left open holds the channel against everyone,
	// and a transmission left open toward QSP holds a repeater keyed.
	defer func() { release(); stopRadio() }()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.Done():
			return
		case <-frames.Ready():
			// One frame for each time round, so a backlog from QSP cannot
			// keep Zello's own audio and events waiting behind it.
			f, ok := frames.Pop()
			if !ok {
				continue
			}
			lastUSRP = time.Now()
			switch {
			case !f.PTT:
				release()
			case len(f.Samples) == 0:
				if err := br.RadioKeyup(); err != nil {
					c.log.Warn("cannot open a Zello stream", slog.String("error", err.Error()))
					continue
				}
				toZello = true
			default:
				if !toZello {
					// A lost keyup: open the stream on the first audio rather
					// than dropping the whole transmission.
					if err := br.RadioKeyup(); err != nil {
						c.log.Warn("cannot open a Zello stream", slog.String("error", err.Error()))
						continue
					}
					toZello = true
				}
				if err := br.RadioFrame(f.Samples); err != nil {
					c.log.Warn("cannot send audio to Zello", slog.String("error", err.Error()))
				}
			}
		case p, ok := <-s.Audio():
			if !ok {
				// **A closed channel is the session ending, not a packet.**
				// The session's reader closes Audio on its way out, and a
				// receive from a closed channel yields a zero packet for as
				// long as anyone asks. Played, it is stream 0 with no Opus:
				// the bridge keys nothing for it, the decoder conceals the
				// "loss" into real audio frames, and QSP receives audio with
				// no keyup and -- because nothing is keyed here -- no
				// release, so it holds a repeater keyed for two seconds.
				return
			}
			play(p)
		case ev, ok := <-s.Events():
			if !ok {
				// Closed with Audio, by the same reader: the session is over.
				return
			}
			if ev.Command == zello.EventStreamStart {
				// **Said once per stream**: what the far side chose to send.
				// The first real connection failed on a packet shape nothing
				// here had seen, and the only evidence was a decode error.
				logStreamStart(c.log, ev)
				continue
			}
			if ev.Command != zello.EventStreamStop {
				continue
			}
			// **Drain the audio queued before the stop, then act on it.**
			// Packets and events arrive in order on one WebSocket but reach
			// here on two channels, and select chooses between ready channels
			// at random. Acting on the stop first played the last packet as a
			// new transmission: a 60 ms keyup and a two-second hang after
			// nearly every Zello over. Everything sent before the stop is
			// already queued by the time the stop is visible.
		drain:
			for {
				select {
				case p, ok := <-s.Audio():
					if !ok {
						// Closed is always ready: without this the drain
						// would never reach its default and never end.
						break drain
					}
					play(p)
				default:
					break drain
				}
			}
			if toRadio && ev.StreamID == zelloStream {
				stopRadio()
			}
		case now := <-tick.C:
			if toZello && now.Sub(lastUSRP) > idle {
				c.log.Warn("audio from QSP stopped without a release; closing the Zello stream")
				release()
			}
			if toRadio && now.Sub(lastZelloAudio) > zelloIdle {
				c.log.Warn("a Zello stream stopped without on_stream_stop; ending it toward QSP")
				stopRadio()
			}
		}
	}
}

// logStreamStart records an incoming stream's declared audio parameters.
func logStreamStart(log *slog.Logger, ev zello.Event) {
	attrs := []any{
		slog.Uint64("stream", uint64(ev.StreamID)),
		slog.String("from", ev.From),
		slog.String("codec", ev.Codec),
		slog.Int("packet_duration_ms", ev.PacketDuration),
	}
	if h, err := zello.DecodeCodecHeader(ev.CodecHeader); err == nil {
		attrs = append(attrs,
			slog.Int("sample_rate", h.SampleRate),
			slog.Int("frames_per_packet", h.FramesPerPacket),
			slog.Int("frame_ms", h.FrameSizeMS))
	} else {
		attrs = append(attrs, slog.String("codec_header", ev.CodecHeader),
			slog.String("codec_header_error", err.Error()))
	}
	log.Info("zello stream started", attrs...)
}

// drainFor waits between attempts while discarding USRP audio, so a keyup
// during an outage does not play into the next session seconds late.
func (c *connector) drainFor(ctx context.Context, frames *audio.Queue, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-frames.Ready():
			c.discarded.Add(uint64(frames.Discard()))
		case <-timer.C:
			return nil
		}
	}
}

// classify turns why a session ended into a state and how long to wait.
//
// **Zello refusing a logon is not retried every few seconds.** Bad credentials
// or a channel the account has not joined will not fix themselves, and hammering
// a service that said no is how an account gets rate-limited or suspended.
func classify(err error, failures int) (string, time.Duration) {
	if err == nil {
		return stateConnected, time.Second
	}
	var le *zellologon.Error
	if errors.As(err, &le) {
		switch le.Kind {
		case zellologon.KindMissing:
			return stateCredentialsMissing, 30 * time.Second
		case zellologon.KindUnusable:
			return stateCredentialsBad, 60 * time.Second
		}
		return stateQSPUnreachable, backoff(failures)
	}
	var ce *zello.CommandError
	if errors.As(err, &ce) && ce.Fatal() {
		return stateZelloRefused, 5 * time.Minute
	}
	if errors.Is(err, zellologon.ErrUnreachable) {
		return stateQSPUnreachable, backoff(failures)
	}
	return stateZelloUnreachable, backoff(failures)
}

// backoff doubles from two seconds to a minute.
func backoff(failures int) time.Duration {
	d := 2 * time.Second
	for range min(failures, 5) {
		d *= 2
	}
	return min(d, time.Minute)
}

func (c *connector) setState(state, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != state {
		c.since = time.Now()
	}
	c.state, c.detail = state, detail
}

// usrpRadio is the QSP side of the bridge.
type usrpRadio struct {
	conn *audio.Conn
	seq  atomic.Uint32
}

func (r *usrpRadio) Keyup() error {
	return r.conn.Send(audio.Frame{Sequence: r.seq.Add(1), PTT: true})
}

func (r *usrpRadio) SendFrame(pcm []int16) error {
	return r.conn.Send(audio.Frame{Sequence: r.seq.Add(1), PTT: true, Samples: pcm})
}

func (r *usrpRadio) Release() error {
	return r.conn.Send(audio.Frame{Sequence: r.seq.Add(1), PTT: false})
}
