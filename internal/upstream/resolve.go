package upstream

import (
	"fmt"
	"net"
	"strconv"
	"time"
)

// Resolver turns a host:port into an address. Nil in a configuration means
// the system resolver; tests supply their own.
type Resolver func(address string) (*net.UDPAddr, error)

func systemResolver(address string) (*net.UDPAddr, error) {
	return net.ResolveUDPAddr("udp", address)
}

// DefaultResolveInterval is how often a link looks its far end up again.
//
// **A name is looked up more than once.** Both transports resolved the far
// end when they started and never again, so a peer on dynamic DNS whose
// address changed was dialled at the old one until QSP restarted — and a name
// that did not resolve at boot stopped QSP starting at all, taking every
// local repeater down with one absent neighbour (found 2026-10-03). A minute
// is well inside the reconnection backoff and costs one cached lookup.
const DefaultResolveInterval = time.Minute

// checkAddress reports whether address is host:port with a usable port. This
// is the part of an address that is a configuration mistake and worth
// refusing to start over; whether the host resolves today is not.
func checkAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if host == "" {
		return fmt.Errorf("no host in %q", address)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port %q is not a number from 1 to 65535", port)
	}
	return nil
}

// isLiteral reports whether address names its host by IP address, which
// never needs looking up again.
func isLiteral(address string) bool {
	host, _, err := net.SplitHostPort(address)
	return err == nil && net.ParseIP(host) != nil
}

func sameAddr(a, b *net.UDPAddr) bool {
	return a != nil && b != nil && a.IP.Equal(b.IP) && a.Port == b.Port
}
