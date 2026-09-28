package peers

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/tms"
)

// Sending a text QSP composed to one hotspot.
//
// This is ADR-0067 phase 2: the sending path, whose instrument is a radio's
// display. It uses the same pacing as parrot and the same narrow exception to
// ADR-0002 — a goroutine writing to the peer socket that is not the listener —
// and for the same reason: frames must leave sixty milliseconds apart and the
// sweep runs once a second. It touches no routing state. Everything it reads
// about the network comes from the snapshots the serve loop publishes for
// other goroutines, never from Master or the call tracker directly.
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
)

// SendText composes a group text and plays it out to one peer on a timeslot.
//
// It returns once the playback has started; the frames take a little over a
// second to leave. Safe to call from any goroutine.
func (l *Listener) SendText(peer hbp.RepeaterID, slot hbp.Timeslot, m tms.Message) error {
	ctx := l.textCtx.Load()
	if ctx == nil {
		return ErrTextNotListening
	}

	var target *Peer
	for _, p := range l.Snapshot() {
		if p.ID == peer && p.State.CanPassTraffic() && p.Config != nil {
			target = &p
			break
		}
	}
	if target == nil {
		return fmt.Errorf("%w: %d", ErrTextUnknownPeer, peer)
	}
	cc, err := colourCodeOf(target.Config)
	if err != nil {
		return err
	}

	l.textMu.Lock()
	defer l.textMu.Unlock()

	if l.texts.Busy(peer) || (l.playback != nil && l.playback.Busy(peer)) {
		return fmt.Errorf("%w: %d", ErrTextBusy, peer)
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
		return fmt.Errorf("peers: composing a text for %d: %w", peer, err)
	}
	l.texts.Start(*ctx, parrot.Recording{Peer: peer, Frames: frames}, target.Addr)

	l.log.Info("text composed",
		slog.Uint64("peer", uint64(peer)),
		slog.Uint64("from", uint64(m.From)),
		slog.Uint64("to", uint64(m.To)),
		slog.String("timeslot", slot.String()),
		slog.Int("frames", len(frames)),
		slog.Int("characters", len([]rune(m.Text))))
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
