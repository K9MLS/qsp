package upstream

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// stubLink is a Connection with a status the test dictates.
type stubLink struct {
	name   string
	status Status
}

func (s *stubLink) Name() string                { return s.name }
func (s *stubLink) Start(context.Context) error { return nil }
func (s *stubLink) Close() error                { return nil }
func (s *stubLink) Send(hbp.Data) error         { return nil }
func (s *stubLink) Status() Status              { return s.status }

// TestALinkSaysWhenThisInstanceIsTheReason.
//
// **Found by running two instances, not by the suite.** A pair peered over
// OpenBridge with dmr.forwarding off logged "link open", bound its socket, and
// then reported degraded with advice to check the far end's address and whether
// UDP was reaching it — while the routing table, built only when forwarding is
// on, did not exist. Nothing could ever have reached that link.
//
// Three statements, each true, together pointing at somebody else's network.
func TestALinkSaysWhenThisInstanceIsTheReason(t *testing.T) {
	set := NewSet(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := set.Add(&stubLink{name: "pair", status: Status{
		Open:    true,
		Summary: "no traffic has ever arrived on this link",
		Advice:  "confirm the far end has this server's current public address",
	}}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	set.SetRelaying(false)
	res := set.CheckFor("pair").Check(context.Background())
	if !strings.Contains(res.Summary, "forwarding is off") {
		t.Errorf("a link nothing can reach reported %q, which sends an operator to "+
			"debug the far end", res.Summary)
	}
	if strings.Contains(res.Summary, "public address") {
		t.Error("the far end is still being blamed")
	}

	// With forwarding on, the link's own advice is the right advice again.
	set.SetRelaying(true)
	res = set.CheckFor("pair").Check(context.Background())
	if !strings.Contains(res.Summary, "no traffic has ever arrived") {
		t.Errorf("a relaying instance lost the link's own report: %q", res.Summary)
	}
}
