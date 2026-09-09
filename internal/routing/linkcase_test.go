package routing_test

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/routing"
)

// **A link whose name is not already lowercase was unreachable.**
//
// `qspLinks` stored names lowercased, and a repeat target was built from that
// key — while the upstream registry looks a link up by the name in the
// configuration. So `route` addressed `qsp test server`, the registry held
// `QSP Test Server`, and every frame was refused with *no link named ... is
// configured*.
//
// It survived because every link before it happened to be named in lowercase,
// and because the direction that works never looks a name up: audio arriving
// from a link is delivered to peers, and only audio going *out* is addressed by
// name. One direction worked, and the link reported itself healthy throughout.
//
// Found on 2026-09-09 by a Motorola repeater transmitting into a link that
// carried in the other direction — running the system, not reading it.
func TestALinkIsAddressedByItsConfiguredName(t *testing.T) {
	for _, name := range []string{
		"QSP Test Server",
		"Production",
		"KD9EJA-01",
		"test-server",
	} {
		t.Run(name, func(t *testing.T) {
			core, err := routing.NewCore(routing.CoreOptions{QSPLinks: []string{name}, Peers: peersReady()})
			if err != nil {
				t.Fatalf("NewCore: %v", err)
			}

			got := routing.QSPLinkNamesForTest(core)
			if len(got) != 1 {
				t.Fatalf("the core holds %d links, want 1", len(got))
			}
			if got[0] != name {
				t.Errorf("a frame is addressed to %q, want %q — the upstream registry "+
					"looks a link up by the name in the configuration", got[0], name)
			}
			if strings.ToLower(got[0]) == got[0] && got[0] != name {
				t.Error("the name was lowercased, which is the defect this rejects")
			}
		})
	}
}
