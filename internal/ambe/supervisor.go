package ambe

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/k9mls/qsp/internal/health"
)

// A supervisor for the configured vocoder channels.
//
// # Why a supervisor rather than opening a client at startup
//
// **A dongle is a thing that gets unplugged.** The operator's is passed
// through ESXi to a virtual machine, its recovery path is removing its power
// for ten seconds, and AMBEserver has no unit and is started by hand. So the
// vocoder is absent far more often than any other destination QSP routes to,
// and a server that refused to start without one would be a server that
// refused to start.
//
// It is also not allowed to look healthy while it is absent. Constitution §3:
// an absent subsystem reports its absence, neither hidden from the report nor
// reported healthy. So the supervisor keeps trying, and the health report says
// exactly which of the three states each channel is in.
//
// # It carries nothing yet, and says so
//
// ADR-0063 put the talkgroup-to-channel mapping in the routing table and
// stopped there. Nothing delivers frames to a vocoder yet, and the
// per-repeater permission that governs the other direction does not exist. A
// channel that has handshaked with its chip is therefore **ready and not
// carrying**, which is degraded rather than healthy — because a green line
// against a subsystem that cannot pass audio is the stub that claims success.
type Supervisor struct {
	log      *slog.Logger
	channels []*channel
}

// Vocoder is one configured vocoder channel.
//
// Named Vocoder rather than Channel because Channel in this package is
// already the channel-packet field table, and a configuration concept sharing
// a name with a protocol one is a name that has to be disambiguated at every
// use.
type Vocoder struct {
	// Name is what a bridge endpoint names.
	Name string
	// Address is the AMBEserver, host:port.
	Address string
	// Rate is the built-in rate index to set on the chip.
	Rate int
}

// channel is one supervised vocoder and its current state.
type channel struct {
	cfg Vocoder

	mu       sync.Mutex
	client   *Client
	lastErr  error
	attempts int
	openedAt time.Time
}

// RetryInterval is how often a supervisor retries a channel it cannot open.
//
// **Slow on purpose.** The thing at the other end is usually absent because
// somebody has unplugged it or has not started AMBEserver, and neither is
// fixed by asking again quickly. Thirty seconds keeps the log readable and
// still has the channel up within half a minute of the operator plugging it
// back in — which is the same reasoning as the IPSC warning rate limit, where
// a retry per datagram wrote thousands of identical lines.
const RetryInterval = 30 * time.Second

// NewSupervisor builds a supervisor over the configured channels. It opens
// nothing; call Run.
func NewSupervisor(log *slog.Logger, channels []Vocoder) *Supervisor {
	s := &Supervisor{log: log}
	for _, c := range channels {
		s.channels = append(s.channels, &channel{cfg: c})
	}
	return s
}

// Run keeps every channel open until ctx is cancelled, then closes them.
//
// It returns only when ctx is done. A channel that cannot be opened is retried
// rather than abandoned, and the failure is reported through the health check
// rather than by stopping the server.
func (s *Supervisor) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, ch := range s.channels {
		wg.Add(1)
		go func(ch *channel) {
			defer wg.Done()
			s.keepOpen(ctx, ch)
		}(ch)
	}
	wg.Wait()
}

// keepOpen holds one channel open, retrying while ctx lives.
func (s *Supervisor) keepOpen(ctx context.Context, ch *channel) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	if !timer.Stop() {
		<-timer.C
	}

	for {
		if err := s.openOnce(ctx, ch); err != nil {
			// Logged at the first failure and then not again until it
			// changes, because a vocoder that is absent stays absent and a
			// line every thirty seconds is a log nobody reads.
			ch.mu.Lock()
			first := ch.attempts == 1
			ch.mu.Unlock()
			if first && s.log != nil {
				s.log.Warn("vocoder unreachable",
					"transcoder", ch.cfg.Name,
					"address", ch.cfg.Address,
					"error", err,
					"retry_interval", RetryInterval)
			}
		}

		timer.Reset(RetryInterval)
		select {
		case <-ctx.Done():
			ch.close()
			return
		case <-timer.C:
		}
	}
}

// openOnce opens a channel if it is not already open.
func (s *Supervisor) openOnce(ctx context.Context, ch *channel) error {
	ch.mu.Lock()
	already := ch.client != nil
	ch.mu.Unlock()
	if already {
		return nil
	}

	c, err := Open(ctx, s.log, ch.cfg.Address, Options{Rate: ch.cfg.Rate})

	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.attempts++
	if err != nil {
		ch.lastErr = err
		return err
	}
	ch.client, ch.lastErr, ch.openedAt = c, nil, time.Now()
	if s.log != nil {
		s.log.Info("vocoder ready and carrying nothing",
			"transcoder", ch.cfg.Name,
			"address", ch.cfg.Address,
			"product", c.Product(),
			"version", c.Version())
	}
	return nil
}

// close releases a channel's client.
func (ch *channel) close() {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.client != nil {
		_ = ch.client.Close()
		ch.client = nil
	}
}

// Names lists the configured channels, in configuration order.
func (s *Supervisor) Names() []string {
	out := make([]string, 0, len(s.channels))
	for _, ch := range s.channels {
		out = append(out, ch.cfg.Name)
	}
	return out
}

// ClientFor returns the open client for a channel, or nil.
//
// internal/vocoderlink calls it at the start of every call. Returning nil
// rather than an error is deliberate: a caller with a frame to transcode and
// no vocoder has nowhere to put it, and that is a routing decision rather than
// an exception.
func (s *Supervisor) ClientFor(name string) *Client {
	for _, ch := range s.channels {
		if ch.cfg.Name != name {
			continue
		}
		ch.mu.Lock()
		defer ch.mu.Unlock()
		return ch.client
	}
	return nil
}

// CheckFor returns the health check for one channel.
func (s *Supervisor) CheckFor(name string) health.Checker {
	for _, ch := range s.channels {
		if ch.cfg.Name == name {
			return channelCheck{ch: ch}
		}
	}
	return missingChannel(name)
}

// channelCheck reports one channel's state.
type channelCheck struct{ ch *channel }

func (c channelCheck) Name() string { return "transcoder:" + c.ch.cfg.Name }

// Check reports which of three states the channel is in.
//
// **None of them is healthy, and that is the point.** A vocoder QSP can talk
// to still carries no audio, because ADR-0063 built the mapping and nothing
// delivers to it yet. Reporting green would be the stub that claims success;
// reporting failing would say the hardware is broken when it is not. Degraded
// with a summary naming what is missing is the honest one.
func (c channelCheck) Check(context.Context) health.Result {
	c.ch.mu.Lock()
	client, lastErr, attempts := c.ch.client, c.ch.lastErr, c.ch.attempts
	c.ch.mu.Unlock()

	detail := map[string]string{"address": c.ch.cfg.Address}

	if client == nil {
		summary := fmt.Sprintf("cannot reach the vocoder at %s", c.ch.cfg.Address)
		if lastErr != nil {
			summary = fmt.Sprintf("cannot reach the vocoder at %s: %v",
				c.ch.cfg.Address, lastErr)
		}
		if attempts == 0 {
			summary = fmt.Sprintf("has not yet tried to reach %s", c.ch.cfg.Address)
		}
		detail["attempts"] = fmt.Sprintf("%d", attempts)
		return health.Result{
			Status:  health.StatusFailing,
			Summary: summary,
			Fix: "start AMBEserver on the host holding the dongle — its debug flag is " +
				"-x, and -v only prints a version — and check the address. If it " +
				"stops answering, unplug the dongle physically for ten seconds; a " +
				"software reset does not clear a wedged chip",
			Detail: detail,
		}
	}

	encoded, decoded, refused, failed := client.Counters()
	detail["product"] = client.Product()
	detail["version"] = client.Version()
	detail["encoded"] = fmt.Sprintf("%d", encoded)
	detail["decoded"] = fmt.Sprintf("%d", decoded)
	detail["refused"] = fmt.Sprintf("%d", refused)
	detail["failed"] = fmt.Sprintf("%d", failed)
	if h := client.Holder(); h != nil {
		detail["carrying"] = h.String()
	}

	// **Healthy once the chip has worked both ways.** Decoding proves DMR
	// reaches it and encoding proves audio leaves it; either alone is half a
	// transcoder, and the summary says which half.
	summary := fmt.Sprintf("%s at %s is ready and carrying nothing", client.Product(), c.ch.cfg.Address)
	if encoded > 0 || decoded > 0 {
		summary = fmt.Sprintf("%s at %s has decoded %d and encoded %d frame(s)",
			client.Product(), c.ch.cfg.Address, decoded, encoded)
	}
	if encoded > 0 && decoded > 0 {
		res := health.Healthy(summary)
		res.Detail = detail
		return res
	}
	res := health.Degraded(summary,
		"a vocoder is working when it has carried audio both ways; see transcoder-audio:"+
			c.ch.cfg.Name+" for which direction has not")
	res.Detail = detail
	return res
}

// missingChannel reports a channel that was named but not configured.
type missingChannel string

func (m missingChannel) Name() string { return "transcoder:" + string(m) }

func (m missingChannel) Check(context.Context) health.Result {
	return health.Unavailable(fmt.Sprintf("no transcoder named %q is configured", string(m)))
}
