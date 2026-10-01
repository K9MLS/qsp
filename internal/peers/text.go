package peers

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
	"github.com/k9mls/qsp/internal/tms"
)

// Sending a text QSP composed: to everybody on a talkgroup, or to one hotspot.
//
// **To the talkgroup is the ordinary case.** Each frame goes through routing
// exactly as any transmission on that talkgroup would — every hotspot
// attached to it, every talkgroup it is bridged to, every linked network the
// bridges allow — and on to every Motorola repeater through the same
// Homebrew-to-IPSC conversion that already carries a hotspot's texts. It
// enters through routing.RouteFromServer, the way Zello audio enters through
// RouteFromTranscoder.
//
// **To one hotspot is for testing**, so a test does not have to reach every
// radio on the network: the frames go straight to that peer's socket and
// nowhere else.
//
// Either way the frames are paced by a parrot.Player, sixty milliseconds
// apart, on a goroutine of their own — the same narrow exception to ADR-0002
// that parrot makes, for the same reason. The routing core, the master and
// the call tracker are locked, and the socket is safe for concurrent writes.
// A text sent to the talkgroup is recorded like any transmission, so it
// appears in Last heard under its sender's ID.
//
// **It refuses rather than queues.** A text dropped into the middle of a call
// on the same timeslot is discarded by MMDVMHost, and the failure would look
// exactly like a wrong encoder. So a send while the timeslot is carrying
// anything, while the peer is hearing a parrot, or while a previous text is
// still going out is refused with the reason, and the administrator sends
// again.

var (
	// ErrTextNotListening: the listener has not started, so there is no
	// socket to send on.
	ErrTextNotListening = errors.New("peers: the listener is not running")
	// ErrTextUnknownPeer: no registered peer has that ID.
	ErrTextUnknownPeer = errors.New("peers: no registered peer has that ID")
	// ErrTextBusy: the peer is already receiving a text or a parrot replay.
	ErrTextBusy = errors.New("peers: that peer is already receiving a playback")
	// ErrTextChannelBusy: a call is active on the timeslot.
	ErrTextChannelBusy = errors.New("peers: a call is active on that timeslot")
	// ErrTextColourCode: the peer's announced colour code cannot be read, and
	// a burst with the wrong one is ignored by the hotspot.
	ErrTextColourCode = errors.New("peers: the peer's colour code cannot be read")
	// ErrTextNoRouting: this instance does not forward, so there is no path
	// from QSP to anybody on a talkgroup.
	ErrTextNoRouting = errors.New("peers: forwarding is off on this instance, so a text can only go to one hotspot")
)

// networkColourCode is written into the bursts of a text sent to a talkgroup.
//
// **It reaches no radio, which is why it can be a constant.** A hotspot
// regenerates the slot type with its own colour code before it transmits —
// that is how two wrong Golay bits went unnoticed until 0439 — and the IPSC
// encoder decodes the block and writes the repeater link's colour code. A
// text sent to one hotspot still uses that hotspot's own, because it is what
// the differential test compares against.
const networkColourCode = 1

// SendText composes a group text and sends it on a timeslot: to everybody on
// the message's talkgroup when only is zero, or to that one peer when it is
// not.
//
// It returns once the frames have started; they take a little over a second
// to leave. Safe to call from any goroutine.
func (l *Listener) SendText(slot hbp.Timeslot, m tms.Message, only hbp.RepeaterID) error {
	return l.sendText(slot, m, only, false)
}

// SendLocalText sends a group text to the message's talkgroup on this
// server's own stations only — its hotspots and its Motorola repeaters — and
// never over a link, to a linked QSP server, or to a transcoder. It is how a
// weather alert goes out: weather is local, and a Denton warning has no place
// on a server in Iowa (ADR-0068, as amended). Otherwise exactly SendText to
// the talkgroup, including its refusals.
func (l *Listener) SendLocalText(slot hbp.Timeslot, m tms.Message) error {
	return l.sendText(slot, m, 0, true)
}

func (l *Listener) sendText(slot hbp.Timeslot, m tms.Message, only hbp.RepeaterID, local bool) error {
	ctx := l.textCtx.Load()
	if ctx == nil {
		return ErrTextNotListening
	}

	var target *Peer
	cc := uint8(networkColourCode)
	if only != 0 {
		for _, p := range l.Snapshot() {
			if p.ID == only && p.State.CanPassTraffic() && p.Config != nil {
				target = &p
				break
			}
		}
		if target == nil {
			return fmt.Errorf("%w: %d", ErrTextUnknownPeer, only)
		}
		var err error
		if cc, err = colourCodeOf(target.Config); err != nil {
			return err
		}
	} else if l.cfg.Routing == nil {
		return ErrTextNoRouting
	}

	l.textMu.Lock()
	defer l.textMu.Unlock()

	if only != 0 && (l.texts.Busy(only) || (l.playback != nil && l.playback.Busy(only))) {
		return fmt.Errorf("%w: %d", ErrTextBusy, only)
	}
	// One composed text on the air at a time, whichever path it takes: two
	// interleaved on a timeslot are both lost.
	if only == 0 && (l.network.Busy(routing.ServerOrigin) || l.local.Busy(routing.ServerOrigin)) {
		return fmt.Errorf("%w: the network", ErrTextBusy)
	}
	// **Any call on the timeslot, anywhere on the network.** A call on
	// another peer can be routed to this one at any moment, and the snapshot
	// is up to a second old. Refusing more than strictly necessary costs an
	// administrator a retry; refusing less costs a message that silently
	// never appears.
	for _, c := range l.Calls().Active {
		if c.Key.Timeslot == slot {
			return fmt.Errorf("%w: %d is transmitting to %d on %v", ErrTextChannelBusy, c.Source, c.Target, slot)
		}
	}

	frames, err := tms.Frames(m, slot, cc, nextTextStream)
	if err != nil {
		return fmt.Errorf("peers: composing a text: %w", err)
	}
	where := "network"
	switch {
	case only != 0:
		l.texts.Start(*ctx, parrot.Recording{Peer: only, Frames: frames}, target.Addr)
		where = strconv.FormatUint(uint64(only), 10)
	case local:
		l.local.Start(*ctx, parrot.Recording{Peer: routing.ServerOrigin, Frames: frames})
		where = "this server only"
	default:
		l.network.Start(*ctx, parrot.Recording{Peer: routing.ServerOrigin, Frames: frames})
	}

	l.log.Info("text composed",
		slog.String("to_peers", where),
		slog.Uint64("from", uint64(m.From)),
		slog.Uint64("to", uint64(m.To)),
		slog.String("timeslot", slot.String()),
		slog.Int("frames", len(frames)),
		slog.Int("characters", len([]rune(m.Text))))
	return nil
}

// networkSink routes each frame of a composed text as a transmission on its
// talkgroup: to the Homebrew side through routing, and to the Motorola side
// through the same path a hotspot's frames take. Local keeps it to this
// server's own stations (routing.RouteLocalFromServer).
type networkSink struct {
	l     *Listener
	local bool
}

// Deliver implements parrot.Sink.
func (s networkSink) Deliver(_ hbp.RepeaterID, frame hbp.Data) error {
	l := s.l
	// Last heard: a transmission on the talkgroup is a transmission, whoever
	// composed it, as DeliverFromTranscoder records a Zello call.
	l.observe(0, frame)
	var res routing.Result
	if s.local {
		res = l.cfg.Routing.RouteLocalFromServer(frame, time.Now())
	} else {
		res = l.cfg.Routing.RouteFromServer(frame, time.Now())
	}
	l.deliver(routing.ServerOrigin, res)
	// Origin 0: no Motorola repeater sent this, so none is excluded.
	l.sendToIPSC(0, frame, res)

	// **One line per text, on its first frame**, saying where routing sent
	// it. A text that reached nobody must say why, or an administrator
	// watching a silent radio has nothing to go on.
	if frame.Sequence == 0 {
		attrs := []any{
			slog.Uint64("to", uint64(frame.TargetID)),
			slog.String("timeslot", frame.Timeslot.String()),
			slog.Int("hotspots", len(res.Deliveries)),
			slog.Int("links", len(res.Upstreams)),
			slog.Bool("repeaters", l.cfg.IPSC != nil && (res.Reason == "" || res.NoHomebrewDestination)),
			slog.Bool("this_server_only", s.local),
		}
		if res.Reason != "" {
			attrs = append(attrs, slog.String("reason", res.Reason))
		}
		l.log.Info("text routed", attrs...)
	}
	if res.Reason != "" && !res.NoHomebrewDestination {
		return fmt.Errorf("peers: routing refused a text frame: %s", res.Reason)
	}
	return nil
}

// colourCodeOf reads the colour code a peer announced in its configuration.
// It arrives as two ASCII digits.
func colourCodeOf(cfg *hbp.Config) (uint8, error) {
	raw := strings.TrimSpace(cfg.ColorCode)
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 15 {
		return 0, fmt.Errorf("%w: %q", ErrTextColourCode, raw)
	}
	return uint8(n), nil
}

// nextTextStream is a fresh, nonzero stream ID. Random rather than counted so
// that two servers, or one restarted, do not reuse a hotspot's recent IDs.
func nextTextStream() hbp.StreamID {
	for {
		if v := rand.Uint32(); v != 0 {
			return hbp.StreamID(v)
		}
	}
}
