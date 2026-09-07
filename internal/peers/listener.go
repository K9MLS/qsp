package peers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// maxDatagram bounds a single read.
//
// The largest HBP message observed is RPTC at 302 bytes. This is deliberately
// far larger so that an oversized datagram is read and rejected by the parser
// with an explanation, rather than silently truncated into something that might
// parse as a valid shorter message.
const maxDatagram = 1500

// sweepInterval is how often idle peers and lost calls are swept.
//
// It also serves as the read deadline, which is what lets the listener run in
// one goroutine: a read that times out is the signal to sweep.
//
// It must be no coarser than the shortest thing it detects. Call tracking gives
// up on a stream after calls.StreamTimeout (2 s), so sweeping every 5 s would
// leave a transmission that lost its terminator displayed as live for up to
// five seconds on an otherwise quiet master — a phantom the operator cannot
// distinguish from somebody actually keyed up.
//
// One second bounds that error to a second and costs one timed-out read per
// second, which is nothing. sweepIntervalIsFineEnough in the tests pins the
// relationship so that raising either value fails loudly.
const sweepInterval = 1 * time.Second

// ListenerConfig configures a Listener.
type ListenerConfig struct {
	// ListenAddress is the UDP host:port to bind, for example "0.0.0.0:62031".
	ListenAddress string
	// Master handles the protocol. Required.
	Master *Master
	// Bus receives peer events. Optional; nil disables publishing.
	Bus *events.Bus
	// Routing relays traffic between peers. Optional; nil means QSP observes
	// traffic and forwards none of it.
	//
	// It is owned by the serve goroutine, like Master, and must not be touched
	// by the caller after Start.
	Routing *routing.Core
	// Upstreams sends frames over links to other networks. Optional; nil means
	// no links are configured and upstream deliveries are discarded with a
	// reason rather than silently.
	Upstreams UpstreamSender
	// ScheduleState reports which bridges should be enabled at an instant.
	// Optional; nil means bridges follow their configured Enabled flag.
	//
	// It is a function rather than a *scheduler.Schedule so that the listener
	// depends on the answer, not on how it is computed — which keeps this
	// package independent of the scheduler and lets the gating be tested
	// without calendar arithmetic.
	ScheduleState func(now time.Time) map[string]bool
	// Triggers open bridges on demand. Optional; nil disables triggering.
	//
	// Owned by the serve goroutine, like Master.
	Triggers *routing.Triggers
	// Rebuild produces the routing table for an instant. Required when
	// ScheduleState is set.
	Rebuild func(now time.Time) (*routing.Table, error)
	// IPSC receives every relayed frame, for onward delivery to Motorola
	// repeaters. Optional; nil leaves IPSC one-way as it was before 0192.
	//
	// **This reverses a rule that used to be structural.** Until now a frame
	// could not reach an IPSC repeater at all, because nothing had captured a
	// master sending voice and QSP would not invent one. That capture still
	// does not exist; this path is built from inference at the operator's
	// direction and is recorded as an exception in ADR-0041, not as a
	// discovery.
	IPSC func(origin uint32, frame hbp.Data)

	// Calls observes transmissions. Optional; nil disables call tracking.
	//
	// It is owned by the serve goroutine, like Master, and must not be touched
	// by the caller after Start.
	Calls *calls.Tracker
	// UnlinkTalkgroup, transmitted on, drops a peer's dynamic attachments.
	// Zero means the network offers no such thing.
	UnlinkTalkgroup uint32
	// UnlinkTimeslot restricts which slot that works on. Zero means either.
	UnlinkTimeslot int
	// CallStore keeps completed calls beyond the life of the process.
	// Optional; nil keeps only the in-memory list.
	//
	// The ring buffer is a display and this is the record. A net control
	// station recovering a check-in they missed needs the second, and fifty
	// entries in memory survive neither the evening nor the deploy that
	// follows it. See ADR-0033.
	CallStore *calls.Store
	// Parrot records and replays on one talkgroup. Optional; nil disables it.
	//
	// A frame parrot handles never reaches the routing core: a recording faces
	// no access control, no contention and no bridge on its way back, because
	// none of those questions is about a member hearing their own voice. See
	// ADR-0028.
	Parrot *parrot.Recorder
}

// Listener owns the UDP socket and drives a Master.
//
// # Concurrency
//
// One goroutine owns everything. It reads a datagram, handles it, writes any
// responses, and sweeps expired peers when a read times out. Master is
// therefore never touched concurrently, which is what lets it carry no locks at
// all — consistent with the single-writer model in ADR-0002.
//
// The only values shared with other goroutines are the atomic counters behind
// Stats, so that the health endpoint can report without disturbing the loop.
type Listener struct {
	cfg  ListenerConfig
	log  *slog.Logger
	conn *net.UDPConn

	// Observability counters, read by the health check from other goroutines.
	received atomic.Uint64
	sent     atomic.Uint64
	dropped  atomic.Uint64
	refused  atomic.Uint64
	ignored  atomic.Uint64
	// recentDrops explains the counters without a restart. Guarded by its own
	// mutex because it is written from the serve goroutine and read by the
	// console.
	// lastUnlink stops one keyup on the unlink talkgroup being acted on fifty
	// times. Serve-goroutine only.
	lastUnlink map[hbp.RepeaterID]hbp.StreamID

	// collisionMu guards the record of which shared radio IDs have been
	// reported. It is written from the IPSC listener's goroutine rather than
	// this listener's, so it is locked rather than owned.
	collisionMu sync.Mutex
	// reportedCollisions notes IDs already warned about, so a repeater that
	// shares an ID produces one line rather than one per transmission.
	reportedCollisions map[hbp.RepeaterID]bool

	dropMu      sync.Mutex
	recentDrops []DropNote
	frames      atomic.Uint64
	forwarded   atomic.Uint64
	// preambles counts CSBK preambles skipped by observe, so that suppressing
	// them from Last heard does not make them invisible. See isPreamble.
	preambles atomic.Uint64
	collided  atomic.Uint64
	peers     atomic.Int64
	writeErr  atomic.Uint64

	// scheduleState is the set of bridges the schedule last said should be
	// enabled, so a change can be detected without rebuilding every sweep.
	// Owned by the serve goroutine.
	scheduleState map[string]bool

	// enabledBridges publishes the same information for readers on other
	// goroutines, for the same reason as snapshot: Core and Master carry no
	// locks because one goroutine owns them, so observers must be handed an
	// immutable copy rather than reaching in.
	enabledBridges atomic.Pointer[[]string]
	// pending holds a configuration change waiting to be applied, at most one.
	// See reload.go.
	pending atomic.Pointer[Reload]
	// routingDrops remembers when each destination and reason was last
	// explained, so that a refusal is logged once per transmission.
	routingDropMu sync.Mutex
	routingDrops  map[string]time.Time

	// ipscRefused is the most recent transmission refused on the IPSC path,
	// so the log names each one once rather than once per frame.
	ipscRefusedMu sync.Mutex
	ipscRefused   ipscRefusal

	// dataRun remembers the last data burst logged, so that a run of them
	// reads as one event in the journal as it already does in the history.
	//
	// It is guarded because observe runs on two goroutines: the serve loop for
	// Homebrew traffic and the IPSC listener's for a Motorola repeater's.
	dataRunMu sync.Mutex
	dataRun   dataRun

	// playback replays parrot recordings. Nil when parrot is off.
	playback *playback
	// ctx bounds every playback goroutine, so shutdown stops them.
	ctx context.Context

	// running reports whether the loop is active, so health can distinguish
	// "not started" from "started and quiet".
	running atomic.Bool

	// callSnapshot holds the most recent active and recent call lists, for the
	// same reason as snapshot below.
	callSnapshot atomic.Pointer[CallSnapshot]

	// snapshot holds the most recent peer list for readers on other
	// goroutines.
	//
	// Master is owned by the serve loop and has no locks, so calling its
	// accessors from an HTTP handler would be a data race. The loop publishes
	// an immutable slice here after every change instead, which readers load
	// without synchronising with the loop at all.
	snapshot atomic.Pointer[[]Peer]
}

// NewListener constructs a Listener. It does not bind; call Start.
func NewListener(log *slog.Logger, cfg ListenerConfig) (*Listener, error) {
	if cfg.Master == nil {
		return nil, errors.New("peers: a Master is required")
	}
	if cfg.ListenAddress == "" {
		return nil, errors.New("peers: a listen address is required, for example \"0.0.0.0:62031\"")
	}
	l := &Listener{cfg: cfg, log: logging.Subsystem(log, "network")}
	if cfg.Parrot != nil {
		// Constructed here rather than when the socket opens, so that the
		// field is written once before any other goroutine exists. Stats reads
		// it, and a field assigned during serve would be a race the tests
		// would only sometimes schedule.
		l.playback = newPlayback(l.log, nil)
	}

	// **Published once at construction**, so a tracker seeded from the record
	// is visible before the first frame arrives. The snapshot is otherwise
	// refreshed only when one does, which left Last heard empty after a restart
	// until somebody transmitted — with the history sitting in the tracker the
	// whole time.
	l.refreshCalls()

	return l, nil
}

// Start binds the socket and serves in a background goroutine.
//
// Binding is synchronous so that a port conflict is reported to the caller
// rather than appearing later in a log line nobody reads. UDP port 62031 is
// the observed HBP default and is below 1024 on no system, but binding it may
// still require the port to be free of another DMR service on the same host.
func (l *Listener) Start(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp", l.cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("cannot understand listen address %q: %w (use host:port, for example \"0.0.0.0:62031\")",
			l.cfg.ListenAddress, err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w (check that the port is free; another DMR service such as DMRGateway may already hold it)",
			l.cfg.ListenAddress, err)
	}
	l.conn = conn
	// The socket exists now, so playback can hold it. Its own field was set in
	// NewListener and is not written again — Stats reads it from another
	// goroutine, and a field assigned during serve would be a data race the
	// tests would only sometimes schedule.
	if l.playback != nil {
		l.playback.conn = conn
	}
	l.running.Store(true)
	l.refresh()
	if l.cfg.ScheduleState != nil {
		empty := []string{}
		l.enabledBridges.Store(&empty)
	}
	l.log.Info("listening for peers", slog.String("address", conn.LocalAddr().String()))

	go l.serve(ctx)
	return nil
}

// Address returns the bound address, useful when the configured port was zero.
func (l *Listener) Address() string {
	if l.conn == nil {
		return l.cfg.ListenAddress
	}
	return l.conn.LocalAddr().String()
}

// Close stops the listener and releases the socket. It is safe to call more
// than once and safe on a listener that was never started.
func (l *Listener) Close() error {
	if l.conn == nil {
		return nil
	}
	l.running.Store(false)
	// Closing unblocks the read in serve.
	if err := l.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("closing the peer listener: %w", err)
	}
	return nil
}

// serve is the single goroutine that owns the Master.
func (l *Listener) serve(ctx context.Context) {
	defer l.running.Store(false)

	// Kept so a recording finishing on the sweep can start a playback bounded
	// by the same lifetime as the listener itself. Only ever read from this
	// goroutine.
	l.ctx = ctx

	buf := make([]byte, maxDatagram)

	for {
		if ctx.Err() != nil {
			return
		}

		// The deadline doubles as the expiry timer: a timed-out read means it
		// is time to sweep, and no second goroutine or channel is needed.
		if err := l.conn.SetReadDeadline(time.Now().Add(sweepInterval)); err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			l.log.Error("cannot set a read deadline", slog.String("error", err.Error()))
			return
		}

		n, from, err := l.conn.ReadFromUDPAddrPort(buf)
		switch {
		case err == nil:
			l.handle(buf[:n], from)
		case errors.Is(err, net.ErrClosed):
			return
		case isTimeout(err):
			// Expected: nothing arrived within the deadline.
		default:
			if ctx.Err() != nil {
				return
			}
			// A read error on a UDP socket is usually transient, for example an
			// ICMP port-unreachable from a peer that went away. Log and carry
			// on rather than tearing down every other peer's session.
			l.log.Warn("read failed", slog.String("error", err.Error()))
		}

		// Before anything else in the sweep, so a change lands on a
		// consistent view rather than half of one.
		l.applyPending()
		l.expire()
		l.expireCalls()
		l.expireRoutes()
		l.expireTriggers()
		l.expireParrot()
		l.applySchedule()
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// handle processes one datagram and writes any responses.
func (l *Listener) handle(datagram []byte, from netip.AddrPort) {
	l.received.Add(1)

	out := l.cfg.Master.Handle(datagram, from)

	if out.Dropped != "" {
		l.dropped.Add(1)
		// **A datagram QSP answered is a different thing from one it ignored.**
		// A keepalive from a peer that has not registered is refused and
		// answered with MSTNAK so the peer logs in again — the protocol working
		// exactly as ADR-0011 intends — and counting it beside a stray scan
		// produces a permanently non-zero number that looks like a fault and
		// is not.
		if len(out.Responses) > 0 {
			l.refused.Add(1)
		} else {
			l.ignored.Add(1)
		}

		// Debug rather than warn: a busy master on the public internet is
		// scanned constantly, and warning on every stray packet would bury the
		// signal. Refusals that an operator needs to see, such as a failed
		// password, are logged at warn by the Master itself.
		l.log.Debug("datagram dropped",
			slog.String("from", from.String()),
			slog.String("reason", out.Dropped),
		)

		// **Kept in memory as well as logged.** The reason was written only at
		// debug, production runs at info, and raising the level needs a restart
		// which resets the counter — so an operator could not see why a number
		// was what it was without destroying the number. Twenty is enough to
		// explain a small count and too few to be a log.
		l.noteDrop(from, out.Dropped, len(out.Responses) > 0)
	}

	for _, r := range out.Responses {
		if _, err := l.conn.WriteToUDPAddrPort(r.Payload, r.To); err != nil {
			l.writeErr.Add(1)
			l.log.Warn("cannot send a response",
				slog.String("to", r.To.String()),
				slog.String("error", err.Error()),
			)
			continue
		}
		l.sent.Add(1)
	}

	if out.Data != nil {
		l.frames.Add(1)
		// Phase 2 routes this. Until the routing engine exists the frame is
		// observed and discarded: it is not queued anywhere that could grow
		// without bound, and nothing pretends it was delivered.
		l.observe(out.From, *out.Data)
		l.trigger(out.From, *out.Data)
		l.forward(out.From, *out.Data)
	}

	l.publish(out.Events)
	l.refresh()
}

// trigger opens any bridge this transmission demands.
//
// It runs before forwarding so that the frame which opened a bridge is itself
// relayed. Opening on the second frame would clip the first syllable of every
// on-demand transmission, which is exactly the complaint operators report about
// systems that get this wrong.
func (l *Listener) trigger(from hbp.RepeaterID, frame hbp.Data) {
	if l.cfg.Triggers == nil {
		return
	}
	opened := l.cfg.Triggers.Observe(routing.EndpointOf(from, frame), time.Now())
	if len(opened) == 0 {
		return
	}
	for _, bridge := range opened {
		l.log.Info("bridge opened on demand",
			slog.String("bridge", bridge),
			logging.PeerID(frame.SourceID),
			logging.Talkgroup(frame.TargetID),
		)
	}
	// Apply immediately rather than waiting for the next sweep, so the opening
	// transmission is carried.
	l.applySchedule()
}

// A frame that is refused is counted and logged rather than silently dropped:
// Constitution §18. The usual cause is two people keying the same talkgroup at
// once, which an operator should be able to see.
func (l *Listener) forward(from hbp.RepeaterID, frame hbp.Data) {
	// Parrot first, and it consumes what it handles. The talkgroup is one the
	// operator gave up for this, so nothing else should see it.
	if l.cfg.Parrot != nil {
		if l.cfg.Parrot.Handles(frame) {
			if rec := l.cfg.Parrot.Observe(from, frame); rec != nil {
				l.replay(*rec)
			}
			return
		}
		// Keying up elsewhere abandons a recording in progress and stops a
		// replay already running: a member who has moved on should not be
		// surprised by their own voice a moment later.
		l.cfg.Parrot.Cancel(from)
		if l.playback != nil {
			l.playback.Stop(from)
		}
	}

	// **Unlink is handled here, before routing, and goes no further.** A member
	// dialling the disconnect talkgroup is addressing this server rather than
	// anybody else on the network, and relaying it would put a burst of their
	// audio onto whatever that number happens to mean elsewhere.
	if l.unlink(from, frame) {
		return
	}

	if l.cfg.Routing == nil {
		return
	}
	res := l.cfg.Routing.Route(from, frame, time.Now())
	l.deliver(from, res)
	l.sendToIPSC(from, frame, res)
}

// unlink drops a peer's dynamic attachments when it transmits on the talkgroup
// configured for it, and reports whether the frame was consumed.
//
// Static attachments survive: a member pressing disconnect says what they want
// to stop hearing, and an administrator's static attachment is a statement about
// what a peer must always carry.
func (l *Listener) unlink(from hbp.RepeaterID, frame hbp.Data) bool {
	if l.cfg.UnlinkTalkgroup == 0 {
		return false
	}
	if frame.CallType != hbp.CallGroup || frame.TargetID != l.cfg.UnlinkTalkgroup {
		return false
	}
	if l.cfg.UnlinkTimeslot != 0 && int(frame.Timeslot) != l.cfg.UnlinkTimeslot {
		return false
	}
	// **Once per keyup, keyed on the stream.** A three-second transmission is
	// about fifty frames; acting on each would drop the attachments once and
	// then log forty-nine times. There is no voice-header predicate on hbp.Data
	// and inventing one from a data-type guess is how this project has been
	// wrong before, so this uses the field that already identifies one keyup.
	//
	// The map is touched only from the serve goroutine, which is the sole
	// caller of handle.
	if l.lastUnlink == nil {
		l.lastUnlink = make(map[hbp.RepeaterID]hbp.StreamID)
	}
	if seen, ok := l.lastUnlink[from]; ok && seen == frame.StreamID {
		return true
	}
	l.lastUnlink[from] = frame.StreamID

	n := l.cfg.Master.DropAttachments(from)
	l.log.Info("talkgroups dropped at the member's request",
		logging.PeerID(uint32(from)),
		slog.Int("dropped", n),
	)
	return true
}

// replay hands a finished recording to the playback goroutine.
func (l *Listener) replay(rec parrot.Recording) {
	if l.playback == nil {
		return
	}
	peer, ok := l.cfg.Master.Lookup(rec.Peer)
	if !ok {
		// The peer went away between transmitting and its recording
		// completing. Nothing to play it to.
		return
	}
	l.playback.Start(l.ctx, rec, peer.Addr)
}

// UpstreamSender carries a frame over a link to another network.
//
// It is an interface rather than a concrete type so that internal/peers does
// not depend on internal/upstream: the listener knows a frame should leave by
// some link, and nothing about which protocol that link speaks.
type UpstreamSender interface {
	Send(link string, frame hbp.Data) error
}

// DeliverFromUpstream routes a frame that arrived over a link and sends the
// result to peers.
//
// It exists because the listener owns the socket peers are reachable on. A link
// receiving a frame cannot deliver it itself without reaching into that, so it
// hands the frame here instead.
func (l *Listener) DeliverFromUpstream(link string, frame hbp.Data) {
	if l.cfg.Routing == nil {
		return
	}
	// A transmission from a link is a transmission, and last heard is a record
	// of who has been on the network (ADR-0033). Omitting this made a talker on
	// the far end of a bridge invisible to the console while their audio was
	// being relayed — the frame was carried and the record said nobody had
	// spoken.
	l.observe(0, frame)
	l.deliver(0, l.cfg.Routing.RouteFromUpstream(link, frame, time.Now()))
}

// DeliverFromIPSC routes a burst converted from a Motorola repeater's audio.
//
// It exists for the same reason DeliverFromUpstream does: this listener owns
// the socket peers are reachable on, and the IPSC listener has a socket of its
// own that no Homebrew peer is behind.
//
// # Why this direction only
//
// **A frame never travels the other way, and that is a property of the code
// rather than a promise.** Destinations are resolved through
// routing.PeerLookup, which is this listener's Homebrew peer table, and every
// delivery is written to this listener's socket. An IPSC repeater appears in
// neither, so a bridge naming one as a destination resolves to nothing. Nothing
// has ever captured a master sending voice to a Motorola repeater, so QSP
// cannot know what such a frame should contain, and a bridge that guessed would
// be exactly the fake behaviour §7 forbids.
//
// # Parrot runs, and it runs in the IPSC listener rather than here
//
// This comment used to say parrot and unlink could not run because there was no
// path back to an IPSC peer. **That stopped being true when patch 0195 fixed
// the outbound frame shape** and a repeater keyed on QSP's audio; the comment
// outlived the fact and was switching off a feature.
//
// Parrot now runs in internal/ipsclink, before a burst reaches this function,
// with its own recorder. Both recorders key by radio ID and the two protocols
// share the DMR ID space — this network had one ID registered on both listeners
// at once — so a shared recorder would merge two operators' recordings and
// replay one into the other's radio.
//
// Unlink still does not run. It drops a peer's talkgroup attachments and an
// IPSC peer announces none: a repeater receives everything and filters by its
// own codeplug, so there is nothing here for a member to unlink from.
//
// Triggers do run. Opening an on-demand bridge needs no reverse path, and a
// Motorola repeater keying up is as good a reason to open one as any other
// peer.
// ObserveFromIPSC records a transmission from a Motorola repeater without
// routing it.
//
// **Recording and carrying are separate acts.** Parrot runs in the IPSC
// listener and consumes what it handles, so a burst it takes never reaches
// DeliverFromIPSC — and a member keying the parrot talkgroup through a
// repeater would leave no trace anywhere an operator looks, while the same
// member doing it through a hotspot would, because this listener observes
// before it forwards.
func (l *Listener) ObserveFromIPSC(from hbp.RepeaterID, frame hbp.Data) {
	l.observe(from, frame)
}

// **It routes and does not record.** ObserveFromIPSC does the recording, and
// the IPSC listener calls that for every converted burst before offering it to
// parrot — so a frame parrot takes is still in Last heard, and a frame that
// reaches here is recorded exactly once. Recording in both places is how one
// transmission came to appear in the panel twice, with two frame counts and two
// durations that disagreed.
func (l *Listener) DeliverFromIPSC(from hbp.RepeaterID, frame hbp.Data) {
	if l.cfg.Routing == nil {
		return
	}
	l.warnIfIDShared(from)

	// **The subscriber list bans a radio, not a repeater**, so it has to apply
	// here as well as on the Homebrew data path. Without it a banned operator
	// was refused on a hotspot and carried by a Motorola repeater, and which
	// door they used decided the answer.
	//
	// Observed before it is checked, deliberately: an operator looking at the
	// dashboard to find out who is transmitting is best served by seeing the
	// station that is being refused, not by it vanishing. Refused after
	// observing and before routing is the same order the Homebrew path uses.
	if l.cfg.Master != nil && !l.cfg.Master.SubscriberAllowed(frame.SourceID) {
		l.refuseIPSCSubscriber(from, frame)
		return
	}

	l.trigger(from, frame)
	res := l.cfg.Routing.Route(from, frame, time.Now())
	l.deliver(from, res)
	// A Motorola repeater's own audio goes back out to the *other* Motorola
	// repeaters, which is what makes repeater-to-repeater work without a
	// hotspot in between.
	l.sendToIPSC(from, frame, res)
}

// refuseIPSCSubscriber logs a refused transmission once per stream.
//
// Constitution §18: nothing is dropped silently. One line per transmission
// rather than one per frame, because a refused transmission is sixty frames a
// second and a journal full of one radio is a journal nobody reads.
func (l *Listener) refuseIPSCSubscriber(from hbp.RepeaterID, frame hbp.Data) {
	key := ipscRefusal{peer: from, source: frame.SourceID, stream: frame.StreamID}
	l.ipscRefusedMu.Lock()
	seen := l.ipscRefused == key
	l.ipscRefused = key
	l.ipscRefusedMu.Unlock()
	if seen {
		return
	}
	l.log.Info("transmission refused: subscriber not permitted",
		logging.PeerID(uint32(from)),
		slog.Uint64("subscriber", uint64(frame.SourceID)),
		slog.Uint64("talkgroup", uint64(frame.TargetID)),
		slog.String("timeslot", frame.Timeslot.String()),
		slog.String("protocol", "ipsc"),
	)
}

// ipscRefusal identifies one refused transmission, so the log names it once.
type ipscRefusal struct {
	peer   hbp.RepeaterID
	source uint32
	stream hbp.StreamID
}

// warnIfIDShared reports a radio ID belonging to both an IPSC repeater and a
// registered Homebrew peer.
//
// **That peer will never hear the repeater, and nothing else says so.** Routing
// does not send a call back to the peer that transmitted it, and it decides
// that by comparing IDs — so a hotspot sharing its number with a Motorola
// repeater is excluded from every one of that repeater's transmissions. Every
// other member hears it. The one person most likely to be testing does not, and
// the journal shows the frame being relayed, which reads as success.
//
// It cost an afternoon. The static form of this check already exists for
// ipsc.master_id, where a repeater refuses to register with a master carrying
// its own ID; this is the same failure between two peers, and it cannot be a
// startup check because the Homebrew peer list is built as peers register.
func (l *Listener) warnIfIDShared(from hbp.RepeaterID) {
	if l.cfg.Master == nil {
		return
	}
	if _, registered := l.cfg.Master.Lookup(from); !registered {
		return
	}

	l.collisionMu.Lock()
	defer l.collisionMu.Unlock()
	if l.reportedCollisions[from] {
		return
	}
	if l.reportedCollisions == nil {
		l.reportedCollisions = make(map[hbp.RepeaterID]bool)
	}
	l.reportedCollisions[from] = true

	l.log.Warn("an IPSC repeater and a registered peer share a radio ID, so that peer cannot hear the repeater",
		logging.PeerID(uint32(from)),
		slog.String("consequence", "a call is never sent back to the peer that transmitted it, "+
			"and this repeater and that peer look like the same station"),
		slog.String("remedy", "give the repeater a radio ID of its own, or the hotspot an ESSID suffix"),
	)
}

// sendToIPSC offers a frame to the Motorola side.
//
// It is called for every frame that routing accepted, rather than for each
// resolved destination, because IPSC repeaters are not in the destination list
// — they announce no talkgroups and a repeater filters by its own codeplug.
func (l *Listener) sendToIPSC(origin hbp.RepeaterID, frame hbp.Data, res routing.Result) {
	if l.cfg.IPSC == nil {
		return
	}
	// **Routing's verdict decides, not the delivery list.** Reason is set only
	// when routing carried the frame nowhere — refused by access, held by a
	// busy destination, on a talkgroup no bridge names. An empty Deliveries
	// list is not the same thing: a network of Motorola repeaters and no
	// hotspots has nowhere on the Homebrew side to deliver and the frame
	// should still reach the other repeaters.
	//
	// **Except when nothing judged it.** A private call to a radio no hotspot
	// has heard is not refused; there is only nowhere on this side to put it,
	// and a repeater receives everything and filters in its own codeplug. That
	// distinction is the difference between a private call between two
	// Motorola repeaters working and vanishing, which is what it did on
	// 2026-09-06 while the same call the other way worked because the called
	// radio happened to sit on a hotspot.
	if res.Reason != "" && !res.NoHomebrewDestination {
		return
	}
	l.cfg.IPSC(uint32(origin), frame)
}

// routingDropWindow is how long one refusal stands for the ones after it.
//
// Long enough to cover a transmission — an over runs seconds and a text about
// one — and short enough that the next attempt is reported rather than
// swallowed, because an operator who keys up again wants to know it is still
// refused.
const routingDropWindow = 10 * time.Second

// maxRoutingDrops bounds how many distinct refusals are remembered at once.
//
// Sixty-four is far more than a working network produces — a refusal names one
// destination and one reason — and small enough that the memory is never worth
// attacking.
const maxRoutingDrops = 64

// noteRoutingDrop reports whether this refusal should be logged, so that a
// destination and reason are explained once per transmission rather than once
// per frame.
func (l *Listener) noteRoutingDrop(d routing.Drop) bool {
	key := d.To.String() + "|" + d.Reason
	now := time.Now()

	l.routingDropMu.Lock()
	defer l.routingDropMu.Unlock()
	if l.routingDrops == nil {
		l.routingDrops = make(map[string]time.Time)
	}
	if at, seen := l.routingDrops[key]; seen && now.Sub(at) < routingDropWindow {
		return false
	}
	// **Bounded, because the key names a destination.** A peer transmitting to
	// endless talkgroups would otherwise add an entry per talkgroup for as
	// long as it kept going.
	//
	// Expired entries go first. If that is not enough the map is emptied
	// rather than trimmed: the cost is that the next refusal of each kind is
	// logged again, which is a duplicate line, and the alternative is memory a
	// peer controls.
	if len(l.routingDrops) >= maxRoutingDrops {
		for k, at := range l.routingDrops {
			if now.Sub(at) >= routingDropWindow {
				delete(l.routingDrops, k)
			}
		}
		if len(l.routingDrops) >= maxRoutingDrops {
			clear(l.routingDrops)
		}
	}
	l.routingDrops[key] = now
	return true
}

func (l *Listener) deliver(from hbp.RepeaterID, res routing.Result) {
	for _, d := range res.Deliveries {
		peer, ok := l.cfg.Master.Lookup(d.Peer)
		if !ok {
			// The peer left between the routing decision and this write.
			continue
		}
		if _, err := l.conn.WriteToUDPAddrPort(d.Frame.Marshal(), peer.Addr); err != nil {
			l.writeErr.Add(1)
			l.log.Warn("cannot forward a frame",
				logging.PeerID(uint32(d.Peer)),
				slog.String("error", err.Error()),
			)
			continue
		}
		l.forwarded.Add(1)
		l.sent.Add(1)
	}

	for _, u := range res.Upstreams {
		if l.cfg.Upstreams == nil {
			// Constitution §18: a frame that is not carried says why. A bridge
			// naming a link that this build has no sender for is a
			// configuration error the operator needs to see.
			l.collided.Add(1)
			l.log.Warn("a bridge names an upstream, but no links are configured",
				slog.String("upstream", u.Upstream),
				slog.String("bridge", u.Bridge),
			)
			continue
		}
		if err := l.cfg.Upstreams.Send(u.Upstream, u.Frame); err != nil {
			l.writeErr.Add(1)
			l.log.Warn("cannot send a frame upstream",
				slog.String("upstream", u.Upstream),
				slog.String("error", err.Error()),
			)
			continue
		}
		l.forwarded.Add(1)
		l.sent.Add(1)
	}

	// **A transmission carried nowhere says why.** Result.Reason was set by
	// routing, read once to decide whether the Motorola repeaters should get
	// the frame, and printed nowhere at all — so a private call to a radio
	// nothing had located produced a `call started` line and then silence. An
	// operator keyed up, heard nothing, and the sentence explaining it existed
	// in memory and went to no journal.
	//
	// This is the trap the drop reasons below fell into, in the same function,
	// and it was fixed for those and left here. Constitution §18.
	//
	// Once per transmission rather than per frame: the drop window keys on a
	// destination and a reason, and a refusal that names no destination still
	// names one reason.
	if res.Reason != "" && l.noteRoutingDrop(routing.Drop{Reason: res.Reason}) {
		// **"Not carried" has to mean nowhere at all.** When the Homebrew side
		// has nowhere to put a frame the Motorola repeaters still take it, so
		// reporting that as a transmission going nowhere is a second lie in
		// place of the silence this replaced — written on 2026-09-06 and
		// corrected the same evening, after an operator read it and reasonably
		// concluded his text had been thrown away.
		msg := "transmission not carried"
		if res.NoHomebrewDestination {
			msg = "no hotspot has this radio; carried to the Motorola repeaters only"
		}
		l.log.Info(msg,
			logging.PeerID(uint32(from)),
			slog.String("reason", res.Reason),
		)
	}

	for _, drop := range res.Drops {
		l.collided.Add(1)
		// **Debug is where a reason goes to be unreachable.** Production runs
		// at info, raising the level needs a restart, and by then the
		// transmission is over — so a refused destination was countable and
		// never explainable. That is the same trap the `dropped` counter fell
		// into, in a different place, and it cost an evening: an operator
		// keying up on a talkgroup a peer was not attached to saw silence, and
		// the sentence explaining it was being written to a level nobody reads.
		//
		// Info, and once per destination and reason rather than per frame: a
		// refused over is fifty frames a second and a refused text is twenty
		// bursts, and a line for each is a line nobody reads either.
		if l.noteRoutingDrop(drop) {
			l.log.Info("frame not forwarded",
				slog.String("to", drop.To.String()),
				slog.String("reason", drop.Reason),
			)
		}
	}

	for _, started := range res.StartedStreams {
		l.log.Info("relaying transmission",
			logging.PeerID(uint32(from)),
			logging.Talkgroup(started.Talkgroup),
			slog.String("to", started.String()),
		)
	}
}

// expireTriggers closes bridges whose hang time has elapsed.
func (l *Listener) expireTriggers() {
	if l.cfg.Triggers == nil {
		return
	}
	for _, bridge := range l.cfg.Triggers.Expire(time.Now()) {
		l.log.Info("bridge closed after hang time", slog.String("bridge", bridge))
	}
}

// applySchedule enables or disables bridges according to the schedule.
//
// The schedule is level-triggered: it is asked what should be enabled now,
// rather than remembering what it enabled earlier. A restart mid-net therefore
// resumes the net, and a missed sweep corrects itself on the next one.
//
// The table is rebuilt only when the answer changes, so the common case costs
// one map comparison per second.
func (l *Listener) applySchedule() {
	if l.cfg.ScheduleState == nil || l.cfg.Rebuild == nil || l.cfg.Routing == nil {
		return
	}
	now := time.Now()
	want := l.cfg.ScheduleState(now)
	if sameState(l.scheduleState, want) {
		return
	}

	table, err := l.cfg.Rebuild(now)
	if err != nil {
		// Keep the current table rather than dropping every bridge: a schedule
		// that fails to rebuild must not take a net off the air.
		l.log.Error("cannot apply the schedule; keeping the current routing table",
			slog.String("error", err.Error()))
		return
	}

	for name := range want {
		if !l.scheduleState[name] {
			l.log.Info("scheduled window opened", slog.String("bridge", name))
		}
	}
	for name := range l.scheduleState {
		if !want[name] {
			l.log.Info("scheduled window closed", slog.String("bridge", name))
		}
	}

	// In-flight transmissions keep their reservations; the new table applies
	// from the next frame (clarification R4).
	l.cfg.Routing.SetTable(table)
	l.scheduleState = want
	enabled := sortedKeys(want)
	l.enabledBridges.Store(&enabled)

	if l.cfg.Bus != nil {
		l.cfg.Bus.Publish(events.TypeRouteChanged, map[string]any{
			"active_bridges": sortedKeys(want),
			"source":         "schedule",
		})
	}
}

func sameState(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// expireRoutes frees destinations held by transmissions that stopped without a
// terminator.
func (l *Listener) expireRoutes() {
	if l.cfg.Routing == nil {
		return
	}
	for _, freed := range l.cfg.Routing.Expire(time.Now()) {
		// **Only audio can be abandoned.** A voice transmission that stops
		// without a terminator has gone wrong and an operator wants to know.
		// A run of data bursts always ends this way — data has no terminator
		// and is not meant to — and warning about it wrote four lines per text
		// message, which is how a warning stops being read at all. The call
		// tracker learned this when a single text produced fifteen of them;
		// this reaper is the same lesson one layer over.
		if !freed.Voice {
			l.log.Debug("released a destination held by a finished data burst",
				slog.String("endpoint", freed.Endpoint.String()),
			)
			continue
		}
		l.log.Warn("released a destination held by an abandoned transmission",
			slog.String("endpoint", freed.Endpoint.String()),
		)
	}
}

// observe records a frame against the call tracker and publishes call events.
func (l *Listener) observe(peer hbp.RepeaterID, frame hbp.Data) {
	if l.cfg.Calls == nil {
		return
	}
	// **A preamble is not a transmission.** One text from a hotspot sends
	// sixteen preamble CSBKs, each with its own stream ID, over 1.87 seconds,
	// and then the data header and content blocks in 142 ms sharing one
	// stream. The tracker groups both correctly, so one message became two
	// rows in Last heard — and the longer, more prominent of them carried no
	// message at all.
	//
	// Measured in testdata/hbp/hbp-text-preambles.pcap: opcode 61, feature ID
	// 0, on all sixteen. **Only that combination is skipped.** A radio check,
	// a call alert and a remote monitor carry different opcodes and keep their
	// rows, which matters most for the last of those: it makes somebody's
	// radio transmit without its operator knowing, and an administrator
	// should see it.
	//
	// The burst is still relayed. This decides what is recorded, not what is
	// carried, and the count means nothing has been made invisible.
	if isPreamble(frame) {
		l.preambles.Add(1)
		return
	}
	now := time.Now()
	started, ended := l.cfg.Calls.Update(peer, frame, now)
	if started != nil {
		// **A run of data bursts is one event, and the journal says so once.**
		// Each burst carries its own stream ID, so each is its own call, and
		// one press of one button on one radio wrote seventeen "call started"
		// lines in a second. The history has merged these into a single entry
		// since the text work; the journal had not, so the console and the
		// journal disagreed about how many things had happened.
		level := slog.LevelInfo
		if l.continuesADataRun(*started, now) {
			level = slog.LevelDebug
		}
		l.log.Log(context.Background(), level, "call started",
			logging.PeerID(started.Source),
			logging.Talkgroup(started.Target),
			logging.Timeslot(int(started.Key.Timeslot)),
			logging.StreamID(uint32(started.Key.Stream)),
		)
		l.publishCall(events.TypeCallStarted, *started)
	}
	if ended != nil {
		l.publishCall(events.TypeCallEnded, *ended)
		l.storeCall(*ended)
	}
	l.refreshCalls()
}

// isPreamble reports whether a frame is a CSBK preamble rather than anything a
// radio operator did.
//
// A burst that does not decode is **not** a preamble as far as this is
// concerned. When QSP cannot tell what something is, the console shows it:
// suppressing an unreadable block is the one way this could hide a command.
func isPreamble(frame hbp.Data) bool {
	if frame.DataType != dataTypeCSBK {
		return false
	}
	c, ok := dmrfec.CSBKOf(frame.Payload[:])
	return ok && c.IsPreamble()
}

// dataTypeCSBK is the Data Type a Control Signalling Block carries, ETSI table
// 9.22. Named here rather than imported because it is the only one this
// package needs to recognise.
const dataTypeCSBK uint8 = 0x3

// continuesADataRun reports whether a starting call carries on the run of data
// bursts the last one began, and records it either way.
//
// **The rule is the history's rule**, because the two numbers an operator sees
// have to agree: same source, same target, same call type, same timeslot, and
// within calls.DataBurstWindow. Voice always starts a run of its own — a
// transmission is an event however soon it follows another.
func (l *Listener) continuesADataRun(c calls.Call, now time.Time) bool {
	l.dataRunMu.Lock()
	defer l.dataRunMu.Unlock()
	if c.Voice {
		l.dataRun = dataRun{}
		return false
	}
	run := dataRun{
		source: c.Source, target: c.Target,
		group: c.Group, timeslot: c.Key.Timeslot, at: now,
	}
	prev := l.dataRun
	l.dataRun = run
	return !prev.at.IsZero() &&
		prev.source == run.source && prev.target == run.target &&
		prev.group == run.group && prev.timeslot == run.timeslot &&
		now.Sub(prev.at) <= calls.DataBurstWindow
}

// dataRun is the last data burst the journal reported, for deciding whether the
// next one is the same event carrying on.
type dataRun struct {
	source, target uint32
	group          bool
	timeslot       hbp.Timeslot
	at             time.Time
}

// expireCalls closes transmissions that stopped without a terminator.
func (l *Listener) expireCalls() {
	if l.cfg.Calls == nil {
		return
	}
	lost := l.cfg.Calls.Expire(time.Now())
	for _, c := range lost {
		// **Data has no terminator and is not meant to**, so warning about one
		// is a false alarm. The console already reserves "no terminator" for
		// voice and labels data for what it is; the journal did not, and a
		// single text message produced fifteen WARN lines about a missing
		// terminator that was never coming.
		//
		// A warning an operator learns to ignore stops working for the case it
		// was written for: a lossy link, or a peer vanishing mid-over.
		if !c.Voice {
			l.log.Debug("data transmission ended",
				logging.PeerID(c.Source),
				logging.Talkgroup(c.Target),
				logging.StreamID(uint32(c.Key.Stream)),
				slog.Int("frames", c.Frames),
			)
			l.publishCall(events.TypeCallEnded, c)
			continue
		}
		// Worth an operator's attention: many of these mean a lossy link or a
		// peer that keeps vanishing mid-transmission.
		l.log.Warn("call ended without a terminator",
			logging.PeerID(c.Source),
			logging.Talkgroup(c.Target),
			logging.StreamID(uint32(c.Key.Stream)),
			slog.Int("frames", c.Frames),
		)
		l.publishCall(events.TypeCallEnded, c)
	}
	if len(lost) > 0 {
		l.refreshCalls()
	}
}

func (l *Listener) publishCall(t events.Type, c calls.Call) {
	if l.cfg.Bus == nil {
		return
	}
	l.cfg.Bus.Publish(t, map[string]any{
		"peer_id":    uint32(c.Key.Peer),
		"source":     c.Source,
		"target":     c.Target,
		"timeslot":   int(c.Key.Timeslot),
		"stream_id":  uint32(c.Key.Stream),
		"group":      c.Group,
		"frames":     c.Frames,
		"end_reason": string(c.EndReason),
	})
}

// CallSnapshot is a point-in-time view of call activity.
type CallSnapshot struct {
	Active []calls.Call
	Recent []calls.Call
}

func (l *Listener) refreshCalls() {
	if l.cfg.Calls == nil {
		return
	}
	snap := CallSnapshot{Active: l.cfg.Calls.Active(), Recent: l.cfg.Calls.History()}
	l.callSnapshot.Store(&snap)
}

// Calls returns the most recent call snapshot. Safe to call from any goroutine.
func (l *Listener) Calls() CallSnapshot {
	if p := l.callSnapshot.Load(); p != nil {
		return *p
	}
	return CallSnapshot{}
}

// refresh republishes the peer snapshot and counters.
//
// Called only from the serve goroutine, which is the sole owner of Master.
func (l *Listener) refresh() {
	l.peers.Store(int64(l.cfg.Master.ConfiguredCount()))
	snap := l.cfg.Master.Peers()
	l.snapshot.Store(&snap)
}

// EnabledBridges returns the bridges the schedule currently has open, sorted.
//
// Safe to call from any goroutine. Nil means no schedule is configured, which
// is different from an empty slice meaning a schedule with nothing open.
func (l *Listener) EnabledBridges() []string {
	if p := l.enabledBridges.Load(); p != nil {
		return *p
	}
	return nil
}

// Snapshot returns the most recent peer list. Safe to call from any goroutine.
//
// The result is a point-in-time copy taken by the serve loop, so it may be
// marginally stale — by at most one datagram or one expiry sweep. That is the
// correct trade for a console view: a slightly old list costs nothing, while
// reaching into live registry state from an HTTP handler would be a race.
func (l *Listener) Snapshot() []Peer {
	if p := l.snapshot.Load(); p != nil {
		return *p
	}
	return nil
}

// expire sweeps idle peers and publishes their departure.
func (l *Listener) expire() {
	if evs := l.cfg.Master.Expire(); len(evs) > 0 {
		l.publish(evs)
		l.refresh()
	}
}

// publish forwards peer events to the bus.
func (l *Listener) publish(evs []Event) {
	if l.cfg.Bus == nil {
		return
	}
	for _, e := range evs {
		var t events.Type
		switch e.Kind {
		case EventConnected:
			t = events.TypePeerConnected
		case EventDisconnected, EventRebound:
			t = events.TypePeerDisconnected
		default:
			continue
		}
		l.cfg.Bus.Publish(t, map[string]any{
			"peer_id":  uint32(e.Peer.ID),
			"callsign": e.Peer.Callsign(),
			"address":  e.Peer.Addr.String(),
			"reason":   e.Reason,
		})
	}
}

// Stats is a snapshot of listener counters.
type Stats struct {
	Received uint64
	Sent     uint64
	// Dropped is every datagram refused, and Refused and Ignored are the two
	// kinds. A refusal QSP answered is the protocol working; one it ignored is
	// traffic nobody asked for.
	Dropped         uint64
	Refused         uint64
	Ignored         uint64
	Frames          uint64
	Forwarded       uint64
	Collisions      uint64
	WriteErrors     uint64
	ConfiguredPeers int64
	// ParrotReplays is how many recordings have been played back, and
	// ParrotFrames how many frames that took. Zero when parrot is off.
	ParrotReplays uint64
	ParrotFrames  uint64
	// ParrotActive is how many replays are running right now, which is the
	// number that tells an operator somebody is testing rather than that
	// somebody once did.
	ParrotActive int
	// Preambles counts CSBK preambles skipped rather than recorded as
	// transmissions.
	//
	// **It exists so that suppressing them is not the same as hiding them.**
	// One text sends sixteen, and each used to be a row in Last heard; the
	// number keeps them accounted for, and it is the thing to read if a
	// command ever stops appearing when it should.
	Preambles uint64
}

// Stats returns current counters. Safe to call from any goroutine.
func (l *Listener) Stats() Stats {
	st := Stats{
		Received:        l.received.Load(),
		Sent:            l.sent.Load(),
		Dropped:         l.dropped.Load(),
		Refused:         l.refused.Load(),
		Ignored:         l.ignored.Load(),
		Frames:          l.frames.Load(),
		Forwarded:       l.forwarded.Load(),
		Collisions:      l.collided.Load(),
		WriteErrors:     l.writeErr.Load(),
		ConfiguredPeers: l.peers.Load(),
		Preambles:       l.preambles.Load(),
	}
	// playback is created when the listener starts serving, so a Stats call
	// before that must not dereference it.
	if l.playback != nil {
		played, frames, _, _ := l.playback.stats()
		st.ParrotReplays = played
		st.ParrotFrames = frames
		st.ParrotActive = l.playback.Active()
	}
	return st
}

// HealthCheck reports on the peer listener.
type HealthCheck struct {
	// Listener is the listener to report on. Nil means the DMR listener is not
	// enabled in this configuration.
	Listener *Listener
	// DisabledReason explains why Listener is nil, and is shown to the operator.
	DisabledReason string
}

// Name implements health.Checker.
func (HealthCheck) Name() string { return "network" }

// Check implements health.Checker.
func (h HealthCheck) Check(context.Context) health.Result {
	if h.Listener == nil {
		reason := h.DisabledReason
		if reason == "" {
			reason = "the DMR listener is not enabled"
		}
		return health.Unavailable(reason)
	}
	if !h.Listener.running.Load() {
		return health.Failing(
			"the peer listener is not running",
			"check the log for a bind failure, then restart QSP",
		)
	}

	s := h.Listener.Stats()
	res := health.Healthy(fmt.Sprintf("listening on %s", h.Listener.Address()))
	if s.WriteErrors > 0 {
		res = health.Degraded(
			fmt.Sprintf("listening, but %d responses could not be sent", s.WriteErrors),
			"peers may be unreachable; check for a firewall or NAT problem between QSP and them",
		)
	}
	res.Detail = map[string]string{
		"address":          h.Listener.Address(),
		"configured_peers": fmt.Sprintf("%d", s.ConfiguredPeers),
		"datagrams_in":     fmt.Sprintf("%d", s.Received),
		"datagrams_out":    fmt.Sprintf("%d", s.Sent),
		"dropped":          fmt.Sprintf("%d", s.Dropped),
		"frames_accepted":  fmt.Sprintf("%d", s.Frames),
		"frames_forwarded": fmt.Sprintf("%d", s.Forwarded),
		"collisions":       fmt.Sprintf("%d", s.Collisions),
	}
	return res
}

// PeersHealthCheck reports on registered peers, separately from the socket.
type PeersHealthCheck struct {
	Listener *Listener
	Master   *Master
	// DisabledReason explains a nil Listener.
	DisabledReason string
}

// Name implements health.Checker.
func (PeersHealthCheck) Name() string { return "peers" }

// Check implements health.Checker.
//
// A master with no peers is reported healthy, not degraded. An empty club
// network at three in the morning is not a fault, and reporting it as one
// trains operators to ignore the health page.
func (h PeersHealthCheck) Check(context.Context) health.Result {
	if h.Listener == nil || h.Master == nil {
		reason := h.DisabledReason
		if reason == "" {
			reason = "the DMR listener is not enabled"
		}
		return health.Unavailable(reason)
	}

	now := time.Now()
	s := h.Listener.Stats()
	blocked := h.Master.BlockedSources(now)

	detail := map[string]string{
		"configured_peers": fmt.Sprintf("%d", s.ConfiguredPeers),
	}
	if s.ParrotReplays > 0 || s.ParrotActive > 0 {
		detail["parrot_replays"] = fmt.Sprintf("%d", s.ParrotReplays)
		detail["parrot_playing"] = fmt.Sprintf("%d", s.ParrotActive)
	}

	// **A blocked source is degraded, not a fault.** Somebody is being turned
	// away and QSP is working exactly as intended — but it is also the state
	// where a member cannot get on the network and nobody has told the
	// operator. Degraded says "look at this" without claiming anything is
	// broken.
	if blocked > 0 {
		detail["blocked_sources"] = fmt.Sprintf("%d", blocked)
		res := health.Degraded(
			fmt.Sprintf("%d peer(s) connected; %d address(es) refused for repeated failed logins",
				s.ConfiguredPeers, blocked),
			"check the console for which address and why; a member with a wrong password "+
				"looks the same from here as somebody guessing, and both stop on their own")
		res.Detail = detail
		return res
	}

	res := health.Healthy(fmt.Sprintf("%d peer(s) connected", s.ConfiguredPeers))
	res.Detail = detail
	return res
}

// SweepInterval reports how often the listener sweeps idle peers and lost
// calls. It is exported so that the relationship with calls.StreamTimeout can
// be asserted in a test rather than maintained by memory.
func SweepInterval() time.Duration { return sweepInterval }

// storeCall writes a completed call to the record.
//
// **The ring keeps it regardless.** A full or locked database costs the record
// and not the display, and a member transmitting is not the moment to fail
// loudly at somebody who cannot act on it — so this warns and carries on.
//
// One row per completed call, never per frame: a busy club evening is a few
// hundred writes rather than fifty a second, which is what keeps this off the
// path ADR-0002 protects.
func (l *Listener) storeCall(c calls.Call) {
	if l.cfg.CallStore == nil || !l.cfg.CallStore.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := l.cfg.CallStore.Record(ctx, c); err != nil {
		l.log.Warn("cannot record a call in the history",
			logging.PeerID(c.Source), "error", err)
	}
}

// DropNote is one refused datagram, kept so a counter can explain itself.
type DropNote struct {
	// At is when it arrived, in UTC.
	At time.Time `json:"at"`
	// From is the source address.
	From string `json:"from"`
	// Reason is what the master said, verbatim.
	Reason string `json:"reason"`
	// Answered reports whether QSP replied, which distinguishes the protocol
	// working from traffic nobody asked for.
	Answered bool `json:"answered"`
}

// maxDropNotes bounds the explanation.
//
// Twenty is enough to account for a small count — the number an operator
// actually questions — and too few to become a log by accident.
const maxDropNotes = 20

func (l *Listener) noteDrop(from netip.AddrPort, reason string, answered bool) {
	l.dropMu.Lock()
	defer l.dropMu.Unlock()

	l.recentDrops = append(l.recentDrops, DropNote{
		At:       time.Now().UTC(),
		From:     from.String(),
		Reason:   reason,
		Answered: answered,
	})
	if len(l.recentDrops) > maxDropNotes {
		l.recentDrops = l.recentDrops[len(l.recentDrops)-maxDropNotes:]
	}
}

// RecentDrops returns the most recent refused datagrams, newest first.
func (l *Listener) RecentDrops() []DropNote {
	l.dropMu.Lock()
	defer l.dropMu.Unlock()

	out := make([]DropNote, len(l.recentDrops))
	for i, n := range l.recentDrops {
		out[len(l.recentDrops)-1-i] = n
	}
	return out
}

// Attachments returns every talkgroup attachment the master holds.
//
// Exposed through the listener because the console reaches the network through
// it, and because a caller holding the Master could change what it reads.
func (l *Listener) Attachments() []Attachment {
	if l == nil || l.cfg.Master == nil {
		return nil
	}
	return l.cfg.Master.Attachments()
}

// SubscriptionEnabled reports whether per-peer attachment is in force.
//
// The console needs it to tell "this peer receives these talkgroups" from
// "every peer receives everything", which look identical in a list and mean
// opposite things.
func (l *Listener) SubscriptionEnabled() bool {
	if l == nil || l.cfg.Master == nil {
		return false
	}
	return l.cfg.Master.SubscriptionEnabled()
}

// SetIPSCSink wires the Motorola side after construction.
//
// It is set here rather than in ListenerConfig because the IPSC listener is
// built after this one and needs this one to exist first: each is the other's
// destination.
func (l *Listener) SetIPSCSink(send func(origin uint32, frame hbp.Data)) {
	l.cfg.IPSC = send
}
