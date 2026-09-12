package p25link

import (
	"context"
	"fmt"
	"strconv"

	"github.com/k9mls/qsp/internal/health"
)

// HealthCheck reports the P25 listener's state.
//
// **It replaces an entry that said P25 was unbuilt.** That was true until
// 2026-09-11 and became a lie the moment the listener existed — which is the
// class of staleness §8a records four instances of in one day, so it is
// replaced in the same patch that builds the thing.
type HealthCheck struct {
	// Listener is nil when P25 is disabled or unconfigured.
	Listener *Listener
	// DisabledReason names the setting that would enable it.
	DisabledReason string
}

// Name identifies the check.
func (HealthCheck) Name() string { return "p25" }

// Check reports the listener's state.
func (h HealthCheck) Check(context.Context) health.Result {
	if h.Listener == nil {
		return health.Unavailable(h.DisabledReason)
	}
	if !h.Listener.Running() {
		return health.Degraded("the P25 listener is configured but not serving",
			"check the log for a bind failure on the configured listen address")
	}

	gateways := h.Listener.Gateways()
	refused, refusedWho := h.Listener.Refused()
	unparsed := h.Listener.Unparsed()

	// **Polls are not frames.** Summing one field called Received reported
	// the five-second registration poll as received audio, so a reflector
	// nobody had spoken through counted twelve frames a minute for as long
	// as it was up. Measured, not inferred: see Gateway.Polls.
	var polls, frames uint64
	for _, g := range gateways {
		polls += g.Polls
		frames += g.Frames
	}

	summary := fmt.Sprintf("%d gateway(s) registered, %d voice frame(s) received",
		len(gateways), frames)

	res := health.Healthy(summary)
	res.Detail = map[string]string{
		"gateways": strconv.Itoa(len(gateways)),
		"frames":   strconv.FormatUint(frames, 10),
		"polls":    strconv.FormatUint(polls, 10),
	}

	// **Unrecognised datagrams are expected and are not a fault.** Three
	// captures are not the whole protocol, so a running listener produces
	// some. Reporting them as degraded would train an operator to ignore the
	// health page, which is the failure the IPSC check already names.
	if unparsed > 0 {
		res.Detail["unparsed"] = strconv.FormatUint(unparsed, 10)
	}

	// A refusal is worth naming rather than counting. The IPSC listener
	// learned that on 2026-09-02: 2144 refusals, none named, and finding out
	// which repeater took a journal search.
	if refused > 0 {
		res.Detail["refused"] = strconv.FormatUint(refused, 10)
		if refusedWho != "" {
			res.Detail["refused_last"] = refusedWho
		}
	}

	// **No gateways is not a fault either.** A reflector nobody has linked to
	// is a working reflector waiting, and an operator who has just enabled it
	// should not be sent looking for a problem. Same reasoning as the bridge
	// check.
	if len(gateways) == 0 {
		res = health.Healthy("no gateways have linked yet")
		res.Detail = map[string]string{"gateways": "0"}
	}
	return res
}
