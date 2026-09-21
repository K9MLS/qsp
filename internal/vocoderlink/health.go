package vocoderlink

import (
	"context"
	"fmt"

	"github.com/k9mls/qsp/internal/health"
)

// CheckName is the health check's name for a channel.
func CheckName(name string) string { return "transcoder-audio:" + name }

// Check reports what the channel has carried, in each direction.
//
// **Healthy only once both directions have carried a call.** A channel that has
// passed a hundred calls one way is half a Zello link, and a green line would
// say otherwise; so until both have happened this names the direction that has
// not, which is also the next thing an operator should test.
func (c *Channel) Check(context.Context) health.Result {
	c.mu.Lock()
	problem, holder := c.problem, c.holder
	c.mu.Unlock()

	toUSRP, fromUSRP := c.calls.Load(), c.txCalls.Load()
	detail := map[string]string{
		"calls_to_usrp":       fmt.Sprintf("%d", toUSRP),
		"frames_to_usrp":      fmt.Sprintf("%d", c.frames.Load()),
		"dropped_queue":       fmt.Sprintf("%d", c.dropped.Load()),
		"not_voice":           fmt.Sprintf("%d", c.notVoice.Load()),
		"refused":             fmt.Sprintf("%d", c.refused.Load()),
		"failed":              fmt.Sprintf("%d", c.failed.Load()),
		"abandoned":           fmt.Sprintf("%d", c.abandoned.Load()),
		"frames_from_usrp":    fmt.Sprintf("%d", c.fromRadio.Load()),
		"calls_from_usrp":     fmt.Sprintf("%d", fromUSRP),
		"bursts_to_routing":   fmt.Sprintf("%d", c.txBursts.Load()),
		"dropped_from_usrp":   fmt.Sprintf("%d", c.txDropped.Load()),
		"refused_from_usrp":   fmt.Sprintf("%d", c.txRefused.Load()),
		"abandoned_from_usrp": fmt.Sprintf("%d", c.txAbandoned.Load()),
		// Slots toward the network that audio from USRP missed, each filled
		// with a silent burst: a short dropout a listener hears, rather than
		// the stutter a repeater makes filling it by repeating audio.
		"late_to_dmr":         fmt.Sprintf("%d", c.txLate.Load()),
		"encoded_needing_fec": fmt.Sprintf("%d", c.txBadFEC.Load()),
	}
	if conn, ok := c.radio.(interface {
		Counters() (sent, received, foreign, malformed uint64)
	}); ok {
		_, _, foreign, malformed := conn.Counters()
		detail["refused_foreign_source"] = fmt.Sprintf("%d", foreign)
		detail["malformed_usrp"] = fmt.Sprintf("%d", malformed)
	}
	if holder != "" {
		detail["carrying"] = holder
	}
	if problem != "" {
		detail["last_problem"] = problem
	}

	// **Since when, in the sentence.** The counts start at zero with QSP, so
	// right after a restart a transcoder that works reads exactly like one
	// that does not; twice on 2026-09-16 that looked like a regression.
	since := "since QSP started at " + c.started.UTC().Format("15:04 UTC")
	summary := fmt.Sprintf("%d call(s) from DMR to USRP, %d from USRP to DMR %s", toUSRP, fromUSRP, since)
	var fix string
	switch {
	case c.txBadFEC.Load() > 0:
		// Said before anything else: this is the one that means audio will
		// never be heard, whatever the other numbers show.
		fix = "frames the vocoder encoded need FEC correction, so their layout is not " +
			"DMR's and radios will not play them; check the chip's rate"
	case toUSRP > 0 && fromUSRP > 0:
		res := health.Healthy(summary)
		res.Detail = detail
		return res
	case toUSRP == 0 && fromUSRP == 0:
		fix = "nothing has crossed " + since + "; key up on a bridged talkgroup, and talk from the " +
			"far side of the USRP socket — after a restart that is expected, not a fault"
	case toUSRP == 0:
		fix = "no DMR call has reached the vocoder " + since + "; key up once on a talkgroup bridged to it " +
			"to confirm this direction — after a restart that is expected, not a fault"
	default:
		fix = "no audio has arrived from the USRP side " + since + "; talk from Zello once to confirm " +
			"this direction — after a restart that is expected, not a fault"
	}
	if problem != "" {
		fix = "last problem: " + problem + ". " + fix
	}
	res := health.Degraded(summary, fix)
	res.Detail = detail
	return res
}

// Checker adapts a channel to the health registry.
func (c *Channel) Checker() health.Checker {
	return health.CheckerFunc{CheckName: CheckName(c.name), Fn: c.Check}
}
