package v24link

import (
	"context"
	"fmt"
	"strconv"

	"github.com/k9mls/qsp/internal/health"
)

// HealthCheck reports the Motorola P25 repeater link.
type HealthCheck struct {
	// Listener is nil when the link is off.
	Listener *Listener
	// DisabledReason is what to say then.
	DisabledReason string
}

// Name is the subsystem's name in the health report.
func (HealthCheck) Name() string { return "p25-repeaters" }

// Check reports how far each repeater's link has come.
//
// **A router connected with no link open is the one state worth amber.** No
// router at all is a listener waiting, which is not a fault. A tunnel open and
// the repeater still asking is a repeater that cannot hear QSP's answers or
// will not take them, and that is what an operator needs pointing at.
func (h HealthCheck) Check(context.Context) health.Result {
	if h.Listener == nil {
		return health.Unavailable(h.DisabledReason)
	}
	if !h.Listener.Running() {
		return health.Degraded("the Motorola P25 repeater link is configured but not serving",
			"check the log for a bind failure on the configured listen address")
	}

	repeaters := h.Listener.Repeaters()
	up := 0
	for _, r := range repeaters {
		if r.Up {
			up++
		}
	}
	detail := map[string]string{
		"routers":      strconv.Itoa(len(repeaters)),
		"links_up":     strconv.Itoa(up),
		"voice_frames": strconv.FormatUint(h.Listener.VoiceFrames(), 10),
		"calls":        strconv.FormatUint(h.Listener.Calls(), 10),
	}
	if refused := h.Listener.Refused(); refused > 0 {
		detail["refused"] = strconv.FormatUint(refused, 10)
	}

	var res health.Result
	switch {
	case len(repeaters) == 0:
		res = health.Healthy("no router has connected yet")
	case up < len(repeaters):
		res = health.Degraded(
			fmt.Sprintf("%d of %d repeater link(s) open", up, len(repeaters)),
			"a router is connected and its repeater's link is not open: read the log for "+
				"how far the link got, and check the repeater's wireline settings and serial cable")
	default:
		res = health.Healthy(fmt.Sprintf("%d repeater link(s) open, %d voice frame(s) heard",
			up, h.Listener.VoiceFrames()))
	}
	res.Detail = detail
	return res
}
