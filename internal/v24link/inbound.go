package v24link

import (
	"context"
	"time"

	"github.com/k9mls/qsp/internal/protocol/p25"
)

// A call from a P25 gateway, sent to the repeaters to transmit.
//
// **This is the direction nobody has captured.** Everything QSP knows about
// voice on this link it learned by listening to a repeater; what a repeater
// accepts is inferred from what it sends. So a gateway's call is sent the way
// a repeater sends its own: the start marker, the voice records, and the end
// marker twice, each in the bytes captured, from the address captured.
//
// **The voice record needs no building.** A gateway's frame is the repeater's
// voice record already — the gateway protocol is those records in datagrams —
// so it is given the two bytes that make it an information frame and nothing
// else is touched. The IMBE inside is never decoded (ADR-0034).

// uiAddress is the address the repeater's own information frames came from.
const uiAddress = 0x07

// The header a repeater sent before each of its own calls, as captured: both
// halves, whole. **It says talkgroup 1.** The second half encodes the
// talkgroup with error correction QSP does not compute, and all three calls
// captured were on talkgroup 1, so this is that header and no other. It is
// sent only when Config.SendHeader asks, for a repeater that will not
// transmit a call that arrives without one.
var (
	capturedHeader1 = []byte{uiAddress, controlUI,
		0x60, 0x02, 0x02, 0x0C, 0x0B, 0x1B, 0x38, 0x1A, 0x4D, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x06}
	capturedHeader2 = []byte{uiAddress, controlUI,
		0x61, 0x00, 0x01, 0x17, 0x14, 0x2A, 0x10, 0x33, 0x31, 0x00,
		0x39, 0x2A, 0x22, 0x04, 0x23, 0x12, 0x11, 0x0A, 0x00, 0x03, 0x0C, 0x02}
)

// FromGateway sends one voice frame from a gateway's call to every linked
// repeater, and reports how many it reached. A repeater hearing the call's
// first frame is sent the start marker before it, whenever in the call that
// is, so a repeater whose link opens part-way still gets a beginning.
//
// Anything that is not a voice frame is refused.
func (l *Listener) FromGateway(frame []byte) int {
	f, err := p25.Parse(frame)
	if err != nil || !f.Voice() {
		return 0
	}
	payload := make([]byte, 0, len(frame)+2)
	payload = append(payload, uiAddress, controlUI)
	payload = append(payload, frame...)

	l.inbound.Lock()
	defer l.inbound.Unlock()
	l.inboundLast = l.now()

	reached := 0
	for _, st := range l.linked() {
		st.mu.Lock()
		opened, send := st.receiving, st.send
		st.receiving = true
		st.mu.Unlock()

		if !opened {
			if send(startMarker) != nil {
				continue
			}
			if l.cfg.SendHeader {
				_ = send(capturedHeader1)
				_ = send(capturedHeader2)
			}
		}
		if send(payload) != nil {
			continue // a repeater that cannot be written to is closed by its own tunnel
		}
		reached++
		st.mu.Lock()
		st.view.Sent++
		st.mu.Unlock()
	}
	l.sent.Add(uint64(reached))
	return reached
}

// EndFromGateway closes the gateway's call at every repeater that was sent
// any of it, with the end marker twice as a repeater sends it. It reports how
// many repeaters that was.
func (l *Listener) EndFromGateway() int {
	l.inbound.Lock()
	defer l.inbound.Unlock()
	return l.endInbound()
}

// endInbound is EndFromGateway with l.inbound already held.
func (l *Listener) endInbound() int {
	l.inboundLast = time.Time{}
	ended := 0
	for _, st := range l.linked() {
		st.mu.Lock()
		opened, send := st.receiving, st.send
		st.receiving = false
		st.mu.Unlock()
		if !opened {
			continue
		}
		_ = send(endMarker)
		_ = send(endMarker)
		ended++
	}
	return ended
}

// watchInbound ends a gateway's call that stopped without a terminator.
//
// **A repeater sent a start and no end stays keyed** until its own timer
// gives up, which on a transmitter is a carrier with nothing on it. A lost
// datagram is all it takes, so the end is sent here when voice has stopped
// arriving for CallTimeout.
func (l *Listener) watchInbound(ctx context.Context) {
	defer l.done.Done()
	tick := time.NewTicker(CallTimeout / 4)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		l.inbound.Lock()
		if !l.inboundLast.IsZero() && l.now().Sub(l.inboundLast) > CallTimeout {
			if n := l.endInbound(); n > 0 {
				l.log.Info("a gateway's call stopped without ending and was closed at the repeaters")
			}
		}
		l.inbound.Unlock()
	}
}

// linked is every repeater whose link is open and can be written to.
func (l *Listener) linked() []*station {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*station
	for _, st := range l.stations {
		st.mu.Lock()
		ok := st.view.Up && st.send != nil
		st.mu.Unlock()
		if ok {
			out = append(out, st)
		}
	}
	return out
}
