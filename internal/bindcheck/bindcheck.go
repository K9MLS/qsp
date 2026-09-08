// Package bindcheck answers one question about an address: can this host bind
// it?
//
// # Why this is not in internal/config
//
// Configuration validation in this project deliberately never touches the
// network. `reachableBeyondHost` in internal/config refuses even to resolve a
// hostname, because whether a document is valid must not depend on whether the
// network is up. That line is worth keeping sharp, so the probe that does touch
// the network lives on its own and is called only where an operator has asked
// for it: `-check`, and the moment a peering writes a listen address.
//
// # What a successful bind proves, and what it does not
//
// It proves the address is one this host holds and the port is free. **It
// proves nothing about reachability from outside.** A port forward, a firewall
// rule and a NAT translation are all beyond anything a process on this machine
// can observe, and a check that implied otherwise would be the same false
// assurance in a new place.
package bindcheck

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// ErrNoAddress is an empty string offered as somewhere to listen.
var ErrNoAddress = errors.New("no address to bind")

// ErrInUse reports that this host can bind the address and something already
// holds it.
//
// **This is not a failure, and telling it apart from one is the reason this
// package classifies at all.** `-check` is run against a configuration while
// the service using that configuration is running, so every address in the
// document is held by QSP itself. Reporting that as "cannot bind" would be the
// defect this package exists to fix, inverted — and a gate that cries wolf is
// ignored, at which point it is not a gate.
//
// EADDRINUSE is also positive evidence: something bound the address, so the
// address is one this host has. The failure that took production down was
// EADDRNOTAVAIL — *cannot assign requested address* — which is a different
// errno and a different sentence.
var ErrInUse = errors.New("something is already listening there")

// Address binds address on network and closes it again.
//
// network is "udp" or "tcp". A nil return means this host can hold the address
// and the port was free at the moment of asking.
func Address(network, address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return ErrNoAddress
	}
	switch network {
	case "udp":
		conn, err := net.ListenPacket("udp", address)
		if err != nil {
			return classify(err)
		}
		return conn.Close()
	case "tcp":
		ln, err := net.Listen("tcp", address)
		if err != nil {
			return classify(err)
		}
		return ln.Close()
	default:
		return fmt.Errorf("bindcheck: unknown network %q", network)
	}
}

// classify separates the one error that is not a fault from every error that
// is. Everything else is returned as the operating system phrased it, because
// "cannot assign requested address" is already the clearest available statement
// of what went wrong.
func classify(err error) error {
	if errors.Is(err, syscall.EADDRINUSE) {
		return ErrInUse
	}
	return err
}
