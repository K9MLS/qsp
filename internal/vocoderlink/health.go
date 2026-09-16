package vocoderlink

import (
	"context"
	"fmt"

	"github.com/k9mls/qsp/internal/health"
)

// CheckName is the health check's name for a channel.
func CheckName(name string) string { return "transcoder-audio:" + name }

// Check reports what the channel has carried.
//
// **Degraded whatever it has carried**, until audio from the USRP side is
// transcoded back to DMR. A channel that has passed a hundred calls one way is
// half a Zello link, and a green line would say otherwise.
func (c *Channel) Check(context.Context) health.Result {
	c.mu.Lock()
	problem, holder := c.problem, c.holder
	c.mu.Unlock()

	detail := map[string]string{
		"calls":            fmt.Sprintf("%d", c.calls.Load()),
		"frames_to_usrp":   fmt.Sprintf("%d", c.frames.Load()),
		"dropped_queue":    fmt.Sprintf("%d", c.dropped.Load()),
		"not_voice":        fmt.Sprintf("%d", c.notVoice.Load()),
		"refused":          fmt.Sprintf("%d", c.refused.Load()),
		"failed":           fmt.Sprintf("%d", c.failed.Load()),
		"abandoned":        fmt.Sprintf("%d", c.abandoned.Load()),
		"frames_from_usrp": fmt.Sprintf("%d", c.fromRadio.Load()),
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

	summary := fmt.Sprintf("has carried %d call(s) from DMR to USRP; audio from USRP is "+
		"received and discarded", c.calls.Load())
	fix := "DMR to USRP is built; USRP back to DMR arrives in the next patch, so a " +
		"Zello user cannot yet be heard on the network"
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
