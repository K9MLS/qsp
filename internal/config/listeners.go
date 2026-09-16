package config

import (
	"fmt"
	"net"
	"strings"
)

// Listener is one address this configuration would bind on startup.
//
// **It exists because the same list was being written twice.** cmd/qsp built
// one to probe with, this package needed one to check for collisions, and two
// lists of the same thing drift — which is the shape of most of this project's
// defects. There is one now, and both callers read it.
type Listener struct {
	// Field names the setting as an operator would find it in the document,
	// so a validation error can be acted on by editing the file.
	Field string
	// Label names it as an operator thinks of it. **Both, because they are
	// different jobs**: dmr.upstreams[0].listen_address is what to edit, and
	// the link "Test Server" is what a report should say. Collapsing the two
	// made -check stop naming the link.
	Label string
	// Network is "udp" or "tcp".
	Network string
	// Address is the host:port as written.
	Address string
}

// Listeners returns every address this configuration would bind.
//
// Only what QSP binds itself. An upstream's Address belongs to the far end, and
// a homebrew upstream binds nothing at all — it logs in to somebody else's
// master and the socket is outbound.
func (c Config) Listeners() []Listener {
	var out []Listener

	if addr := strings.TrimSpace(c.Server.ListenAddress); addr != "" {
		out = append(out, Listener{"server.listen_address", "the console", "tcp", addr})
	}
	if c.DMR.Enabled {
		if addr := strings.TrimSpace(c.DMR.ListenAddress); addr != "" {
			out = append(out, Listener{"dmr.listen_address", "the DMR listener", "udp", addr})
		}
	}
	if c.IPSC.Enabled {
		if addr := strings.TrimSpace(c.IPSC.ListenAddress); addr != "" {
			out = append(out, Listener{"ipsc.listen_address", "the IPSC listener", "udp", addr})
		}
	}
	for i, u := range c.DMR.Upstreams {
		if !u.Enabled || u.HomebrewProtocol() {
			continue
		}
		if addr := strings.TrimSpace(u.ListenAddress); addr != "" {
			out = append(out, Listener{
				fmt.Sprintf("dmr.upstreams[%d].listen_address", i),
				fmt.Sprintf("the link %q", u.Name), "udp", addr,
			})
		}
	}
	for i, t := range c.DMR.Transcoders {
		if !c.DMR.Enabled || !t.Enabled {
			continue
		}
		if addr := strings.TrimSpace(t.USRPListen); addr != "" {
			out = append(out, Listener{
				fmt.Sprintf("dmr.transcoders[%d].usrp_listen", i),
				fmt.Sprintf("the transcoder %q", t.Name), "udp", addr,
			})
		}
	}
	return out
}

// listenersCollide reports whether two listeners would contend for one port.
//
// Same protocol and same port collide when the hosts are equal, and also when
// **either host is unspecified**: 0.0.0.0 is every interface on this machine,
// so it includes any specific address on that port. Getting that half wrong
// would let 0.0.0.0:62045 and 192.168.1.27:62045 both validate and then fight
// at bind time, which is the failure this check exists to prevent.
//
// A malformed address is not a collision. Validate reports that separately, and
// guessing here would produce two errors for one mistake.
func listenersCollide(a, b Listener) bool {
	if a.Network != b.Network {
		return false
	}
	hostA, portA, errA := net.SplitHostPort(a.Address)
	hostB, portB, errB := net.SplitHostPort(b.Address)
	if errA != nil || errB != nil || portA != portB {
		return false
	}
	hostA = strings.Trim(strings.TrimSpace(hostA), "[]")
	hostB = strings.Trim(strings.TrimSpace(hostB), "[]")
	if unspecifiedHost(hostA) || unspecifiedHost(hostB) {
		return true
	}
	return strings.EqualFold(hostA, hostB)
}

// unspecifiedHost reports a host meaning "every interface".
func unspecifiedHost(host string) bool {
	if host == "" {
		return true
	}
	addr := net.ParseIP(host)
	return addr != nil && addr.IsUnspecified()
}

// validateListeners refuses a configuration two of whose listeners would want
// the same port.
//
// # The defect this exists after
//
// Upstream *names* were checked for duplicates and their listen addresses were
// not. Accepting two peerings without restarting in between — taking the page's
// own suggested 0.0.0.0:62045 both times, which is the obvious thing to do —
// wrote two links on one port. **Nothing caught it anywhere.**
//
// The accept handler's bind probe could not: an accepted link opens no socket
// until a restart, so the first was not bound when the second was checked.
// `-check` could not: it binds each address and closes it before the next, so
// two identical addresses both report bindable. Startup could, by refusing to
// start, and systemd then crash-looped to its start limit.
//
// It belongs here because Validate is the one gate every path goes through —
// startup, -check, and every save from the console.
func (c Config) validateListeners(v *validator) {
	all := c.Listeners()
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if !listenersCollide(all[i], all[j]) {
				continue
			}
			// Reported against the later of the two, which on the console is
			// the one just added and the one the operator can still change.
			v.add(all[j].Field,
				fmt.Sprintf("%q is already bound by %s, at %s",
					all[j].Address, all[i].Label, all[i].Field),
				"two listeners cannot share a port; give this one a different port, "+
					"and note that 0.0.0.0 means every interface on this machine")
		}
	}
}

// MaxUpstreamNameLength bounds a link name. Long enough for "kb9tyc-cameron",
// short enough that it is a name rather than a description.
const MaxUpstreamNameLength = 64

// ValidUpstreamName reports why a link name cannot be used, or nil.
//
// # Why a name is constrained at all
//
// **It becomes a file path.** Accepting a peering writes the agreed passphrase
// to `filepath.Join(dir, name+".pass")` beside the peer password file, and
// removing the link deletes that path — with the name taken straight from a
// form and checked for nothing but emptiness and uniqueness. A name containing
// a path separator writes and later deletes a file outside the data directory,
// which in a container crosses a volume boundary.
//
// An authenticated administrator is inside SECURITY.md's trust boundary, so
// this is not privilege escalation. It is still not what "name your link"
// should be able to do, and the constraint costs an operator nothing: every
// name anybody would choose already satisfies it.
//
// Exported because the accept handler writes the passphrase file before the
// configuration is saved, so it cannot wait for Validate to catch this.
func ValidUpstreamName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("a link needs a name")
	}
	if len(trimmed) > MaxUpstreamNameLength {
		return fmt.Errorf("a link name is at most %d characters", MaxUpstreamNameLength)
	}
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == ' ', r == '-', r == '_':
		default:
			return fmt.Errorf("a link name may use letters, digits, spaces, "+
				"hyphens and underscores; %q is not one of those", string(r))
		}
	}
	return nil
}
