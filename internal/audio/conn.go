package audio

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync/atomic"
	"time"
)

// Conn is a USRP socket with exactly one far side.
//
// # Why one far side, and why the filter is the whole of its security
//
// USRP has no authentication, no session and no sequence a stranger cannot
// guess. Anything that can reach the port can key a transmitter. The one thing
// this end can check is where a datagram came from, so **a datagram from any
// address other than the configured peer is discarded and counted** — and the
// count is exported, because a port receiving traffic it refuses is something
// an operator should see rather than something that was quietly safe.
//
// The listen address is not required to be a specific interface. A container
// cannot bind the host's address, and refusing 0.0.0.0 would make the Docker
// install impossible while protecting nothing the source filter does not
// already protect.
type Conn struct {
	pc   *net.UDPConn
	peer netip.AddrPort

	sent, received, foreign, malformed atomic.Uint64
}

// ErrUnspecifiedPeer is a peer address that names no machine.
var ErrUnspecifiedPeer = errors.New("audio: the USRP peer must name one machine, not every interface")

// Listen opens a USRP socket on listen that exchanges datagrams with peer.
//
// Both are host:port with a literal IP. A hostname is refused rather than
// resolved, because the source filter compares addresses and a name that
// resolves differently tomorrow would silently start refusing the far side.
func Listen(listen, peer string) (*Conn, error) {
	laddr, err := netip.ParseAddrPort(listen)
	if err != nil {
		return nil, fmt.Errorf("audio: USRP listen address %q: %w", listen, err)
	}
	paddr, err := netip.ParseAddrPort(peer)
	if err != nil {
		return nil, fmt.Errorf("audio: USRP peer address %q: %w", peer, err)
	}
	if paddr.Addr().IsUnspecified() || paddr.Port() == 0 {
		return nil, fmt.Errorf("%w: %q", ErrUnspecifiedPeer, peer)
	}
	pc, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(laddr))
	if err != nil {
		return nil, fmt.Errorf("audio: cannot listen for USRP on %s: %w", listen, err)
	}
	// Unmapped, because a dual-stack socket reports an IPv4 sender as
	// ::ffff:a.b.c.d and the comparison in Receive must not refuse the peer
	// for the way the kernel spelled it.
	return &Conn{pc: pc, peer: netip.AddrPortFrom(paddr.Addr().Unmap(), paddr.Port())}, nil
}

// LocalAddr is the address actually bound, which differs from the configured
// one when the configuration asked for port 0 in a test.
func (c *Conn) LocalAddr() netip.AddrPort {
	return c.pc.LocalAddr().(*net.UDPAddr).AddrPort()
}

// Send encodes and sends one frame to the peer.
func (c *Conn) Send(f Frame) error {
	b, err := Encode(f)
	if err != nil {
		return fmt.Errorf("audio: encoding a USRP frame: %w", err)
	}
	if _, err := c.pc.WriteToUDPAddrPort(b, c.peer); err != nil {
		return fmt.Errorf("audio: sending USRP to %s: %w", c.peer, err)
	}
	c.sent.Add(1)
	return nil
}

// Receive returns the next well-formed frame from the peer.
//
// Datagrams from anywhere else, and datagrams that are not USRP voice frames,
// are counted and skipped rather than returned: the caller has nothing it
// could do with either except drop it, and doing that here means it cannot
// forget to. It returns when ctx is done or the socket is closed.
func (c *Conn) Receive(ctx context.Context) (Frame, error) {
	buf := make([]byte, FrameBytes+64)
	for {
		if err := ctx.Err(); err != nil {
			return Frame{}, err
		}
		// A short deadline so cancellation is noticed without closing the
		// socket underneath a caller that may still want to send a release.
		if err := c.pc.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			return Frame{}, fmt.Errorf("audio: setting a USRP read deadline: %w", err)
		}
		n, from, err := c.pc.ReadFromUDPAddrPort(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return Frame{}, fmt.Errorf("audio: reading USRP: %w", err)
		}
		if netip.AddrPortFrom(from.Addr().Unmap(), from.Port()) != c.peer {
			c.foreign.Add(1)
			continue
		}
		f, ok := Decode(buf[:n])
		if !ok {
			c.malformed.Add(1)
			continue
		}
		c.received.Add(1)
		return f, nil
	}
}

// Counters reports what the socket has done: frames sent to the peer, frames
// received from it, datagrams refused for coming from somewhere else, and
// datagrams from the peer that were not USRP voice frames.
func (c *Conn) Counters() (sent, received, foreign, malformed uint64) {
	return c.sent.Load(), c.received.Load(), c.foreign.Load(), c.malformed.Load()
}

// Close closes the socket.
func (c *Conn) Close() error {
	if err := c.pc.Close(); err != nil {
		return fmt.Errorf("audio: closing the USRP socket: %w", err)
	}
	return nil
}
