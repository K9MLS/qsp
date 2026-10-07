package p25link_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/p25link"
	"github.com/k9mls/qsp/internal/protocol/p25"
)

// answered sends a poll and reports whether it came back.
func answered(t *testing.T, c *net.UDPConn, callsign string) bool {
	t.Helper()
	if _, err := c.Write(p25.NewPoll(callsign).Marshal()); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err := c.Read(make([]byte, 64))
	return err == nil
}

// TestOnlySoManyGatewaysAreRegistered. A registration is one datagram naming
// any callsign, and with no allow list every one made a gateway that was then
// sent every voice frame: twenty thousand of them, in the hunt of
// 2026-10-07, from one machine.
//
// To see it fail: remove the `len(l.gateways) >= l.maxGateways()` test from
// poll.
func TestOnlySoManyGatewaysAreRegistered(t *testing.T) {
	tests := []struct {
		name  string
		limit int // as configured; zero is the default
		send  int
		want  int
	}{
		{"a limit of three", 3, 10, 3},
		{"the default", 0, p25link.DefaultMaxGateways + 40, p25link.DefaultMaxGateways},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, addr, stop := serve(t, p25link.Config{MaxGateways: tc.limit})
			defer stop()
			c := dial(t, addr)
			// Read what comes back, or the answers fill the socket and the
			// kernel starts dropping what this is trying to send.
			go func() {
				buf := make([]byte, 128)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			}()
			for i := range tc.send {
				if _, err := c.Write(p25.NewPoll(fmt.Sprintf("F%05d", i)).Marshal()); err != nil {
					t.Fatalf("write: %v", err)
				}
				if i%16 == 15 {
					time.Sleep(time.Millisecond) // a datagram socket drops a burst
				}
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				refused, _ := l.Refused()
				if len(l.Gateways())+int(refused) >= tc.send || time.Now().After(deadline) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if got := len(l.Gateways()); got != tc.want {
				t.Fatalf("%d gateways registered, want %d", got, tc.want)
			}
			// At least one: a datagram may still be lost on a busy machine,
			// and the count of those is not what this is about.
			if refused, who := l.Refused(); refused == 0 || who == "" {
				t.Errorf("%d refused, the last of them %q; want some, and a callsign", refused, who)
			}
		})
	}
}

// TestAFullRegistryStillServesTheGatewaysInIt, and has room again when one of
// them goes quiet.
//
// To see it fail: refuse a poll whenever the registry is full, without
// asking whether the callsign is already in it.
func TestAFullRegistryStillServesTheGatewaysInIt(t *testing.T) {
	l, addr, stop := serve(t, p25link.Config{MaxGateways: 2})
	defer stop()
	one, two, three := dial(t, addr), dial(t, addr), dial(t, addr)

	if !answered(t, one, "W9AAA") || !answered(t, two, "W9BBB") {
		t.Fatal("the first two gateways were not registered")
	}
	if answered(t, three, "W9CCC") {
		t.Fatal("a third gateway was answered past a limit of two")
	}
	// The two that are in go on being answered, however full it is.
	for range 3 {
		if !answered(t, one, "W9AAA") || !answered(t, two, "W9BBB") {
			t.Fatal("a registered gateway's poll went unanswered because the registry was full")
		}
	}

	// Both go quiet for longer than a gateway may, and are forgotten.
	l.ExpireAt(time.Now().Add(p25link.PollInterval*p25link.MissedPollsBeforeGone + time.Minute))
	if got := len(l.Gateways()); got != 0 {
		t.Fatalf("%d gateways remain after going quiet", got)
	}
	if !answered(t, three, "W9CCC") {
		t.Error("with a place free again, a new gateway was still refused")
	}
}
