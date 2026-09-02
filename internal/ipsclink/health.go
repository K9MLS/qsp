package ipsclink

import (
	"context"
	"fmt"
	"time"

	"github.com/k9mls/qsp/internal/health"
)

// HealthCheck reports the IPSC listener.
//
// Constitution §3: an absent subsystem reports its absence, and a disabled one
// names the setting that would turn it on. A listener that is off is not a
// listener that is broken, and the report says which.
type HealthCheck struct {
	// Listener is nil when IPSC is disabled or unconfigured.
	Listener *Listener
	// DisabledReason names the setting that would enable it.
	DisabledReason string
}

// Name identifies the check.
func (HealthCheck) Name() string { return "ipsc" }

// Check reports the listener's state.
func (h HealthCheck) Check(context.Context) health.Result {
	if h.Listener == nil {
		return health.Unavailable(h.DisabledReason)
	}
	if !h.Listener.Running() {
		return health.Degraded("the IPSC listener is configured but not serving",
			"check the log for a bind failure on the configured listen address")
	}

	peers := h.Listener.Peers()
	ignored, unparsed := h.Listener.Counters()
	refused, refusing := h.Listener.LastRefused(time.Now())

	// **Unrecognised datagrams are expected and are not a fault.** Eight
	// message types have been captured and IPSC certainly has more, so a
	// running link produces some. Reporting them as degraded would train an
	// operator to ignore the health page.
	summary := fmt.Sprintf("%d peer(s) registered on %s, %d voice frame(s)",
		len(peers), h.Listener.Address(), totalFrames(peers))
	if unparsed > 0 {
		summary += fmt.Sprintf(", %d datagram(s) of types this build does not know", unparsed)
	}
	if ignored > 0 {
		summary += fmt.Sprintf(", %d refused by the allow list", ignored)
	}

	// **The condition is a peer being refused now, not one having been refused
	// ever.** A lifetime total never falls, so a subsystem that once turned
	// something away reads degraded until the process restarts, and a status
	// that cannot recover is a status an operator stops reading. What matters
	// is whether something is knocking at this moment — and which, because on
	// 2026-09-02 the count said 2144 and named nobody, and the answer was a
	// member's repeater retrying every ten seconds for hours.
	if refusing {
		return health.Degraded(
			summary+fmt.Sprintf("; radio ID %d is being refused right now", refused),
			fmt.Sprintf("add %d under ipsc.allowed_peers if it should connect, "+
				"or leave it if it should not", refused))
	}
	return health.Healthy(summary)
}

func totalFrames(peers []Peer) uint64 {
	var n uint64
	for _, p := range peers {
		n += p.VoiceFrames
	}
	return n
}
