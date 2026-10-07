package peers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/events"
)

// TestASaveDoesNotPublishWhoSavedIt. The event stream is read without signing
// in, and a configuration save used to put the administrator's username on
// it: half of a sign-in, sent to anybody with the Overview open.
//
// To see it fail: add `"author": r.Author,` back to the event applyPending
// publishes.
func TestASaveDoesNotPublishWhoSavedIt(t *testing.T) {
	for _, tc := range []struct{ name, author, summary string }{
		{"a callsign", "K9MLS", "added a bridge"},
		{"a name that is not a callsign", "pete-the-admin", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := events.NewBus(nil, events.Options{})
			t.Cleanup(bus.Close)
			sub, _ := bus.Subscribe()
			defer sub.Close()

			l, buf := journal()
			l.cfg.Bus = bus
			l.Apply(&Reload{Author: tc.author, Summary: tc.summary})
			l.applyPending()

			select {
			case ev := <-sub.C():
				if ev.Type != events.TypeRouteChanged {
					t.Fatalf("published %q, want %q", ev.Type, events.TypeRouteChanged)
				}
				sent, err := json.Marshal(ev)
				if err != nil {
					t.Fatalf("the event does not encode: %v", err)
				}
				if strings.Contains(string(sent), tc.author) {
					t.Errorf("the event names who saved: %s", sent)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("applying a configuration published nothing")
			}
			// It is still attributable, where an operator reads it.
			if !strings.Contains(buf.String(), tc.author) {
				t.Errorf("the log line does not name who saved: %s", buf.String())
			}
		})
	}
}
