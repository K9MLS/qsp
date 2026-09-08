package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/k9mls/qsp/internal/bindcheck"
	"github.com/k9mls/qsp/internal/config"
)

// The list of addresses lived here as well as in internal/config, and two
// lists of the same thing drift. config.Listeners() is the one now: the
// collision rule in Validate reads it, and so does this.
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
	for _, l := range cfg.Listeners() {
		err := bindcheck.Address(l.Network, l.Address)
		switch {
		case err == nil:
			fmt.Fprintf(out, "  %s (%s) %s: can be bound\n", l.Label, l.Field, l.Address)
		case errors.Is(err, bindcheck.ErrInUse):
			// Named rather than diagnosed. QSP holding its own port is the
			// expected case and the only other explanation is another program,
			// which -check cannot tell apart and should not guess at.
			fmt.Fprintf(out, "  %s (%s) %s: in use — expected while qsp is running\n",
				l.Label, l.Field, l.Address)
		default:
			fmt.Fprintf(out, "  %s (%s) %s: CANNOT BIND: %v\n", l.Label, l.Field, l.Address, err)
			failed = append(failed, l.Label+" ("+l.Field+")")
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
