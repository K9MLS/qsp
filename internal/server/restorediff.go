package server

import (
	"fmt"
	"slices"
	"strings"

	"github.com/k9mls/qsp/internal/config"
)

// Where a backup came from, as far as this server can tell.
//
// **Said, not implied.** On 2026-10-18 the full backup of one server was
// restored onto another. The confirmation said "if that server is still
// running, two servers will claim one identity" — true of every restore, so
// it read as boilerplate — and nothing said the file came from a different
// machine, or that the two links the restored server had would be gone after
// the restart. They were, and two days later it looked like a link that had
// dropped on its own.
const (
	// originThisServer: the backup carries this server's own identifier.
	originThisServer = "this_server"
	// originAnotherServer: the backup carries a different identifier.
	originAnotherServer = "another_server"
	// originUnknown: one side has no identifier, so nothing can be compared.
	// **Not reported as a match.** A backup from before identifiers existed
	// could be from anywhere, and saying "this server" would be the false
	// reassurance this exists to remove.
	originUnknown = "unknown"
)

// restoreChanges is what a restore would change, by name, worked out before
// it is confirmed.
//
// **Names rather than counts**, because "removes 2 links" is a number an
// operator reads past, and "removes links: production, BCARA" is the sentence
// that would have stopped the restore that prompted this.
type restoreChanges struct {
	// Origin is originThisServer, originAnotherServer or originUnknown.
	Origin string `json:"origin"`
	// OriginNote says the same in a sentence, naming both servers.
	OriginNote string `json:"origin_note"`

	LinksRemoved   []string `json:"links_removed,omitempty"`
	LinksAdded     []string `json:"links_added,omitempty"`
	BridgesRemoved []string `json:"bridges_removed,omitempty"`
	BridgesAdded   []string `json:"bridges_added,omitempty"`
	// StationsRemoved and StationsAdded are the IDs allowed to register.
	StationsRemoved []string `json:"stations_removed,omitempty"`
	StationsAdded   []string `json:"stations_added,omitempty"`
}

// compareRestore works out what replacing running with incoming would change.
//
// incomingID is the identifier the backup carries. The shareable export has it
// beside the configuration as well as inside it, so the caller passes whichever
// the file has; an empty one is unknown, never a match.
func compareRestore(running, incoming config.Config, incomingID string) restoreChanges {
	out := restoreChanges{}

	here := strings.TrimSpace(running.Server.Identifier)
	there := strings.TrimSpace(incomingID)
	if there == "" {
		there = strings.TrimSpace(incoming.Server.Identifier)
	}
	hereName, thereName := serverName(running), serverName(incoming)

	switch {
	case here == "" || there == "":
		out.Origin = originUnknown
		out.OriginNote = "This backup does not say which server made it, so QSP " +
			"cannot tell whether it is this one."
	case here == there:
		out.Origin = originThisServer
		out.OriginNote = "This backup was made by this server."
	default:
		out.Origin = originAnotherServer
		if thereName == hereName {
			out.OriginNote = fmt.Sprintf("This backup was made by another server, also "+
				"called %s, not by this one. Restoring it replaces this server's "+
				"settings with that server's.", thereName)
		} else {
			out.OriginNote = fmt.Sprintf("This backup was made by %s, not by this "+
				"server (%s). Restoring it replaces this server's settings with %s's.",
				thereName, hereName, thereName)
		}
	}

	out.LinksRemoved, out.LinksAdded = difference(linkNames(running), linkNames(incoming))
	out.BridgesRemoved, out.BridgesAdded = difference(bridgeNames(running), bridgeNames(incoming))
	out.StationsRemoved, out.StationsAdded = difference(stationIDs(running), stationIDs(incoming))
	return out
}

// identityNote is the restore's identity paragraph for where the backup came
// from.
//
// **The general warning is kept for every case but one.** When the backup is
// this server's own, there is no other machine to collide with, and saying
// there might be is what made the warning read as boilerplate.
func identityNote(origin, general string) string {
	if origin == originThisServer {
		return "The backup carries this server's own identifier, so restoring it " +
			"keeps this server's identity."
	}
	return general
}

// restoredFrom adds to a restore's history summary the server the backup
// came from, when that was not this one, so the History page says what
// happened without the backup file to hand. The summary is otherwise left as
// it was.
func restoredFrom(summary string, c restoreChanges, incoming config.Config) string {
	if c.Origin == originAnotherServer {
		return summary + " made by " + serverName(incoming)
	}
	return summary
}

// serverName is what an operator calls a server: its network name, else the
// start of its identifier.
func serverName(cfg config.Config) string {
	if n := strings.TrimSpace(cfg.DMR.Join.NetworkName); n != "" {
		return n
	}
	if id := strings.TrimSpace(cfg.Server.Identifier); id != "" {
		return "the server " + config.ShortIdentifier(id)
	}
	return "an unnamed server"
}

func linkNames(cfg config.Config) []string {
	out := make([]string, 0, len(cfg.DMR.Upstreams))
	for _, u := range cfg.DMR.Upstreams {
		out = append(out, strings.TrimSpace(u.Name))
	}
	return out
}

func bridgeNames(cfg config.Config) []string {
	out := make([]string, 0, len(cfg.DMR.Bridges))
	for _, b := range cfg.DMR.Bridges {
		out = append(out, strings.TrimSpace(b.Name))
	}
	return out
}

func stationIDs(cfg config.Config) []string {
	if cfg.DMR.Access == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.DMR.Access.Registration.IDs))
	for _, id := range cfg.DMR.Access.Registration.IDs {
		out = append(out, strings.TrimSpace(id))
	}
	return out
}

// difference returns what is in before and not after, and the reverse, each
// sorted and without blanks or repeats.
func difference(before, after []string) (removed, added []string) {
	b, a := nameSet(before), nameSet(after)
	for k := range b {
		if !a[k] {
			removed = append(removed, k)
		}
	}
	for k := range a {
		if !b[k] {
			added = append(added, k)
		}
	}
	slices.Sort(removed)
	slices.Sort(added)
	return removed, added
}

func nameSet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, s := range items {
		if s != "" {
			out[s] = true
		}
	}
	return out
}
