package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/k9mls/qsp/internal/bindcheck"
	"github.com/k9mls/qsp/internal/config"
)

// listener is one address -check will try to bind, named as the configuration
// names it so an operator can go straight to the field.
type listener struct {
	field   string
	network string
	address string
}

// listeners collects every address this configuration would bind on startup.
//
// **Only the addresses QSP itself binds.** An upstream's Address is the far
// end's and binding it here would mean nothing; a homebrew upstream binds
// nothing at all, because it logs in to somebody else's master and the socket
// is outbound.
func listeners(cfg config.Config) []listener {
	var out []listener

	if addr := strings.TrimSpace(cfg.Server.ListenAddress); addr != "" {
		out = append(out, listener{"server.listen_address", "tcp", addr})
	}
	if cfg.DMR.Enabled {
		if addr := strings.TrimSpace(cfg.DMR.ListenAddress); addr != "" {
			out = append(out, listener{"dmr.listen_address", "udp", addr})
		}
	}
	if cfg.IPSC.Enabled {
		if addr := strings.TrimSpace(cfg.IPSC.ListenAddress); addr != "" {
			out = append(out, listener{"ipsc.listen_address", "udp", addr})
		}
	}
	for _, u := range cfg.DMR.Upstreams {
		if !u.Enabled {
			continue
		}
		// An empty protocol means openbridge, so every document written before
		// outbound peer mode existed keeps meaning what it meant.
		switch u.Protocol {
		case "", config.UpstreamOpenBridge:
		default:
			continue
		}
		if addr := strings.TrimSpace(u.ListenAddress); addr != "" {
			out = append(out, listener{
				fmt.Sprintf("upstream %q listen_address", u.Name), "udp", addr,
			})
		}
	}
	return out
}

// checkBinds attempts every listen address and reports what would fail.
//
// # What counts as a failure, and what does not
//
// **In use is not a failure.** -check is run against a live configuration while
// the service using it is running, so every address in the document is held by
// QSP itself; reporting three failures on a healthy server would teach an
// operator to stop reading the output, and then it is not a gate. EADDRINUSE is
// also positive evidence that the address is one this host has.
//
// The failure is EADDRNOTAVAIL and everything like it: an address this machine
// does not hold. That is the one that took production down, and it is the one
// this can prove.
func checkBinds(out io.Writer, cfg config.Config) error {
	var failed []string
	for _, l := range listeners(cfg) {
		err := bindcheck.Address(l.network, l.address)
		switch {
		case err == nil:
			fmt.Fprintf(out, "  %s %s: can be bound\n", l.field, l.address)
		case errors.Is(err, bindcheck.ErrInUse):
			// Named rather than diagnosed. QSP holding its own port is the
			// expected case and the only other explanation is another program,
			// which -check cannot tell apart and should not guess at.
			fmt.Fprintf(out, "  %s %s: in use — expected while qsp is running\n",
				l.field, l.address)
		default:
			fmt.Fprintf(out, "  %s %s: CANNOT BIND: %v\n", l.field, l.address, err)
			failed = append(failed, l.field)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	// Listed by name. Counting them is how a new failure hides inside a known
	// number, which this project has paid for once already.
	return fmt.Errorf("this host cannot bind: %s — qsp will refuse to start",
		strings.Join(failed, ", "))
}
