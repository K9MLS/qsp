package p25link

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/k9mls/qsp/internal/protocol/p25"
)

// maxDatagram bounds a read.
//
// The largest frame in any capture is 22 bytes. This is generous rather than
// tight because a datagram longer than a frame is refused by the parser and
// counted, and a read buffer too small would truncate one into something that
// looks like a different frame — which is worse than refusing it.
const maxDatagram = 1500

// Start serves until the context is cancelled.
func (l *Listener) Start(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp", l.cfg.ListenAddress)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	l.conn = conn
	l.running.Store(true)
	defer func() {
		l.running.Store(false)
		_ = conn.Close()
	}()

	l.log.Info("p25 listener started",
		"address", conn.LocalAddr().String(),
		"callsign", l.cfg.Callsign,
		"allowed", len(*l.allowed.Load()),
		"poll_interval", PollInterval.String())

	// Closing the socket is what unblocks the read below; a deadline would
	// work too and would mean waking up for nothing several times a second.
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	buf := make([]byte, maxDatagram)
	sweep := time.NewTicker(PollInterval)
	defer sweep.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-sweep.C:
			l.expire(now)
		default:
		}

		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			l.log.Warn("p25 read failed", "error", err.Error())
			continue
		}
		l.handle(buf[:n], from)
	}
}

// handle processes one datagram.
func (l *Listener) handle(raw []byte, from *net.UDPAddr) {
	frame, err := p25.Parse(raw)
	if err != nil {
		// **Counted rather than logged per datagram.** Three captures are not
		// the whole protocol, so this is expected to be non-zero, and a log
		// line per unknown frame would bury everything else during whatever
		// sends them.
		l.unparsed.Add(1)
		return
	}

	if frame.Kind == p25.KindPoll {
		l.poll(raw, from)
		return
	}
	l.voice(frame, raw, from)
}

// poll answers a gateway's keepalive.
//
// **The poll is the registration.** `p25-register.pcap` shows a gateway
// announcing itself and the far end returning the identical datagram — 96 out,
// 96 back, byte for byte, every 5.01 seconds. There is no login, so there is
// nothing else to answer.
//
// The reply is the received bytes rather than a poll QSP builds. A gateway that
// pads its callsign differently gets its own padding back, which is the same
// rule the rest of this package follows: QSP does not rewrite what it carries.
func (l *Listener) poll(raw []byte, from *net.UDPAddr) {
	poll, err := p25.ParsePoll(raw)
	if err != nil {
		l.unparsed.Add(1)
		return
	}

	callsign := normalise(poll.Callsign)
	if callsign == "" {
		l.unparsed.Add(1)
		return
	}

	if allowed := *l.allowed.Load(); len(allowed) > 0 && !allowed[callsign] {
		l.refused.Add(1)
		who := poll.Callsign
		l.refusedCallsign.Store(&who)
		// Not answered. A refused gateway that received a reply would believe
		// it was registered and sit there sending voice nobody carries.
		return
	}

	now := l.now()
	l.mu.Lock()
	g := l.gateways[callsign]
	if g == nil {
		g = &Gateway{Callsign: poll.Callsign, FirstSeen: now}
		l.gateways[callsign] = g
		l.log.Info("p25 gateway registered", "callsign", poll.Callsign,
			"address", from.String())
	}
	g.Address = from
	g.LastPoll = now
	g.Received++
	l.publish()
	l.mu.Unlock()

	if _, err := l.conn.WriteToUDP(raw, from); err != nil {
		l.log.Warn("cannot answer a p25 poll", "callsign", poll.Callsign,
			"error", err.Error())
	}
}

// voice records a frame and relays it to every other registered gateway.
//
// **Relayed verbatim, to everybody except the sender.** Reading the talkgroup
// is the only inspection: it decides what the console reports and, once the
// routing core carries P25, where a call goes. The bytes themselves are never
// touched.
func (l *Listener) voice(frame p25.Frame, raw []byte, from *net.UDPAddr) {
	if !frame.Voice() && !frame.EndsTransmission() {
		l.unparsed.Add(1)
		return
	}

	now := l.now()
	l.mu.Lock()

	// The sender, found by address rather than by callsign: a voice frame
	// carries no callsign, so the only thing tying it to a gateway is where it
	// came from.
	var sender *Gateway
	for _, g := range l.gateways {
		if g.Address != nil && g.Address.IP.Equal(from.IP) && g.Address.Port == from.Port {
			sender = g
			break
		}
	}
	if sender == nil {
		// **Voice from a gateway that has not polled is not carried.** It may
		// be a gateway whose poll was refused, or one that has never sent one;
		// either way QSP does not know who it is, and relaying it would put
		// audio from an unidentified source onto somebody's repeater.
		l.mu.Unlock()
		l.refused.Add(1)
		return
	}
	sender.Received++
	sender.LastPoll = now

	if tg, high, err := frame.Talkgroup(); err == nil && high == 0 {
		sender.Talkgroup = tg
	}
	if src, err := frame.SourceID(); err == nil {
		sender.SourceID = src
	}

	targets := make([]*Gateway, 0, len(l.gateways))
	for _, g := range l.gateways {
		if g != sender && g.Address != nil {
			targets = append(targets, g)
		}
	}
	l.publish()
	l.mu.Unlock()

	for _, g := range targets {
		if _, err := l.conn.WriteToUDP(raw, g.Address); err != nil {
			l.log.Warn("cannot relay a p25 frame", "to", g.Callsign, "error", err.Error())
			continue
		}
		l.mu.Lock()
		g.Sent++
		l.mu.Unlock()
	}
}

// expire forgets gateways that have stopped polling.
func (l *Listener) expire(now time.Time) {
	cutoff := PollInterval * MissedPollsBeforeGone

	l.mu.Lock()
	defer l.mu.Unlock()

	for key, g := range l.gateways {
		if now.Sub(g.LastPoll) <= cutoff {
			continue
		}
		l.log.Info("p25 gateway went quiet", "callsign", g.Callsign,
			"silent_for", now.Sub(g.LastPoll).Round(time.Second).String())
		delete(l.gateways, key)
	}
	l.publish()
}

// ExpireAt is expire, for the scheduler and for tests.
func (l *Listener) ExpireAt(now time.Time) { l.expire(now) }

// publish refreshes the snapshot. Caller holds mu.
func (l *Listener) publish() {
	out := make([]Gateway, 0, len(l.gateways))
	for _, g := range l.gateways {
		out = append(out, *g)
	}
	l.snapshot.Store(&out)
}
