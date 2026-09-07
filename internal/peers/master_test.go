package peers_test

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

const (
	testID       = hbp.RepeaterID(3132910)
	testPassword = "qsp-test-password"
)

var (
	addrA = netip.MustParseAddrPort("192.0.2.10:54663")
	addrB = netip.MustParseAddrPort("198.51.100.7:41000")
	salt  = [4]byte{0x99, 0x47, 0xb4, 0x30}
)

// clock is a manually advanced clock so that timeouts are exact rather than
// approximate.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

type harness struct {
	t *testing.T
	m *peers.Master
	c *clock
}

func newHarness(t *testing.T, opts ...func(*peers.MasterConfig)) *harness {
	t.Helper()
	c := &clock{t: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	cfg := peers.MasterConfig{
		Password: func(id hbp.RepeaterID) ([]byte, bool) {
			if id == testID {
				return []byte(testPassword), true
			}
			return nil, false
		},
		Now:  c.now,
		Salt: func() ([4]byte, error) { return salt, nil },
	}
	for _, o := range opts {
		o(&cfg)
	}
	m, err := peers.NewMaster(logging.Discard(), cfg)
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}
	return &harness{t: t, m: m, c: c}
}

func (h *harness) send(msg hbp.Message, from netip.AddrPort) peers.Outcome {
	h.t.Helper()
	return h.m.Handle(msg.Marshal(), from)
}

// login drives a full successful handshake and returns the outcomes.
func (h *harness) login(from netip.AddrPort) {
	h.t.Helper()

	out := h.send(hbp.Login{RepeaterID: testID}, from)
	if len(out.Responses) != 1 {
		h.t.Fatalf("login produced %d responses, want 1 (dropped: %q)", len(out.Responses), out.Dropped)
	}
	ack, err := hbp.Parse(out.Responses[0].Payload)
	if err != nil {
		h.t.Fatalf("challenge is unparseable: %v", err)
	}
	issued := ack.(hbp.Ack).Salt()

	digest := hbp.Digest(issued, []byte(testPassword))
	out = h.send(hbp.Key{RepeaterID: testID, Digest: digest}, from)
	if len(out.Responses) != 1 {
		h.t.Fatalf("authentication produced %d responses, want 1 (dropped: %q)", len(out.Responses), out.Dropped)
	}

	out = h.send(hbp.Config{RepeaterID: testID, Callsign: "K9MLS", ColorCode: "11"}, from)
	if len(out.Responses) != 1 {
		h.t.Fatalf("configuration produced %d responses, want 1 (dropped: %q)", len(out.Responses), out.Dropped)
	}
	if len(out.Events) != 1 || out.Events[0].Kind != peers.EventConnected {
		h.t.Fatalf("registration did not emit a connected event: %+v", out.Events)
	}
}

func TestFullHandshakeMatchesTheCapturedSequence(t *testing.T) {
	h := newHarness(t)

	// RPTL -> RPTACK carrying the salt.
	out := h.send(hbp.Login{RepeaterID: testID}, addrA)
	if out.Dropped != "" {
		t.Fatalf("login dropped: %s", out.Dropped)
	}
	msg, err := hbp.Parse(out.Responses[0].Payload)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ack, ok := msg.(hbp.Ack)
	if !ok {
		t.Fatalf("login answered with %s, want RPTACK", msg.Kind())
	}
	if ack.Salt() != salt {
		t.Errorf("challenge salt = %x, want %x", ack.Salt(), salt)
	}

	// RPTK -> RPTACK echoing the repeater ID.
	out = h.send(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(salt, []byte(testPassword))}, addrA)
	if out.Dropped != "" {
		t.Fatalf("authentication dropped: %s", out.Dropped)
	}
	msg, _ = hbp.Parse(out.Responses[0].Payload)
	if got := msg.(hbp.Ack).RepeaterID(); got != testID {
		t.Errorf("ack echoed repeater ID %d, want %d", got, testID)
	}

	// RPTC -> RPTACK, and the peer becomes operational.
	out = h.send(hbp.Config{RepeaterID: testID, Callsign: "K9MLS"}, addrA)
	if out.Dropped != "" {
		t.Fatalf("configuration dropped: %s", out.Dropped)
	}
	p, ok := h.m.Lookup(testID)
	if !ok {
		t.Fatal("peer is not registered after a complete handshake")
	}
	if p.State != peers.StateConfigured {
		t.Errorf("state = %s, want configured", p.State)
	}
	if !p.State.CanPassTraffic() {
		t.Error("a fully registered peer cannot pass traffic")
	}
	if p.Callsign() != "K9MLS" {
		t.Errorf("callsign = %q, want K9MLS", p.Callsign())
	}
}

func TestWrongPasswordIsRejectedAndLeavesNoRegistration(t *testing.T) {
	h := newHarness(t)
	h.send(hbp.Login{RepeaterID: testID}, addrA)

	out := h.send(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(salt, []byte("wrong"))}, addrA)
	if !strings.Contains(out.Dropped, "authentication failed") {
		t.Errorf("drop reason = %q", out.Dropped)
	}
	if h.m.Count() != 0 {
		t.Error("a failed authentication left a registration behind, holding a slot")
	}
}

func TestUnknownRepeaterIDIsRejectedWithNak(t *testing.T) {
	h := newHarness(t)
	out := h.send(hbp.Login{RepeaterID: 9999999}, addrA)

	if len(out.Responses) != 1 {
		t.Fatalf("got %d responses, want 1 (an MSTNAK)", len(out.Responses))
	}
	msg, err := hbp.Parse(out.Responses[0].Payload)
	if err != nil {
		t.Fatalf("the rejection is unparseable: %v", err)
	}
	nak, ok := msg.(hbp.Nak)
	if !ok {
		t.Fatalf("refused login answered with %s, want MSTNAK", msg.Kind())
	}
	if nak.RepeaterID != 9999999 {
		t.Errorf("MSTNAK carries repeater ID %d, want the one that was refused", nak.RepeaterID)
	}
	// A rejection must never be mistaken for a challenge.
	if _, isAck := msg.(hbp.Ack); isAck {
		t.Error("the refusal was an RPTACK")
	}
	if !strings.Contains(out.Dropped, "no password is configured") {
		t.Errorf("drop reason = %q", out.Dropped)
	}
	if h.m.Count() != 0 {
		t.Error("a refused login created a registration")
	}
}

func TestWrongPasswordIsRejectedWithNak(t *testing.T) {
	h := newHarness(t)
	h.send(hbp.Login{RepeaterID: testID}, addrA)

	out := h.send(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(salt, []byte("wrong"))}, addrA)
	if len(out.Responses) != 1 {
		t.Fatalf("got %d responses, want an MSTNAK", len(out.Responses))
	}
	msg, err := hbp.Parse(out.Responses[0].Payload)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := msg.(hbp.Nak); !ok {
		t.Errorf("failed authentication answered with %s, want MSTNAK", msg.Kind())
	}
}

// TestCleanDisconnectRemovesThePeerImmediately.
//
// Without RPTCL a hotspot shutting down cleanly is indistinguishable from one
// that lost power, for a whole timeout.
func TestCleanDisconnectRemovesThePeerImmediately(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	out := h.send(hbp.RepeaterClose{RepeaterID: testID}, addrA)
	if h.m.Count() != 0 {
		t.Error("the peer is still registered after closing cleanly")
	}
	if len(out.Events) != 1 || out.Events[0].Kind != peers.EventDisconnected {
		t.Fatalf("a clean close emitted %+v, want one disconnected event", out.Events)
	}
	if !strings.Contains(out.Events[0].Reason, "closed the connection") {
		t.Errorf("reason = %q, want it to distinguish a clean close from a timeout",
			out.Events[0].Reason)
	}
	// A close is not acknowledged.
	if len(out.Responses) != 0 {
		t.Errorf("a close produced %d responses, want 0", len(out.Responses))
	}
}

// TestCloseFromAnotherAddressIsRefused.
//
// Otherwise anyone who knows a repeater ID could disconnect it with a single
// nine-byte datagram.
func TestCloseFromAnotherAddressIsRefused(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	out := h.send(hbp.RepeaterClose{RepeaterID: testID}, addrB)
	if h.m.Count() != 1 {
		t.Fatal("a spoofed close disconnected a working peer")
	}
	if !strings.Contains(out.Dropped, "registered from") {
		t.Errorf("drop reason = %q", out.Dropped)
	}
}

func TestCloseFromAnUnregisteredPeerIsIgnored(t *testing.T) {
	h := newHarness(t)
	out := h.send(hbp.RepeaterClose{RepeaterID: testID}, addrA)
	if out.Dropped == "" {
		t.Error("a close from an unregistered peer was accepted silently")
	}
	if len(out.Events) != 0 {
		t.Error("a close from an unregistered peer emitted a disconnected event")
	}
}

func TestHandshakeCannotBeSkipped(t *testing.T) {
	// Every out-of-order step must be refused. This is the property that stops
	// an unregistered station injecting traffic, and it is about Data and
	// Dropped rather than about whether the master says anything back.
	//
	// A keepalive is answered with MSTNAK, which carries no traffic and confers
	// nothing: it tells a stale peer to log in again. See handlePing.
	cases := map[string]struct {
		msg       hbp.Message
		responses int
		why       string
	}{
		"config without login": {hbp.Config{RepeaterID: testID, Callsign: "K9MLS"}, 0, ""},
		"key without login":    {hbp.Key{RepeaterID: testID}, 0, ""},
		"ping without login": {hbp.Ping{RepeaterID: testID}, 1,
			"a keepalive from an unregistered peer is answered with MSTNAK so it logs in again"},
		"data without login": {hbp.Data{RepeaterID: testID, SourceID: uint32(testID), TargetID: 3100}, 1,
			"the first frame of a stale transmission is answered with MSTNAK, once per five seconds, " +
				"so a peer does not wait for its own keepalive to find out"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			out := h.send(tc.msg, addrA)
			if len(out.Responses) != tc.responses {
				t.Errorf("produced %d responses, want %d: %s", len(out.Responses), tc.responses, tc.why)
			}
			if out.Data != nil {
				t.Error("a frame was accepted from an unregistered station")
			}
			if out.Dropped == "" {
				t.Error("the datagram was discarded without an explanation")
			}
			// Whatever is said back, no registration may result from it.
			if h.m.Count() != 0 {
				t.Error("an out-of-order message created a registration")
			}
		})
	}
}

func TestConfigCannotBeSentBeforeAuthenticating(t *testing.T) {
	h := newHarness(t)
	h.send(hbp.Login{RepeaterID: testID}, addrA)

	out := h.send(hbp.Config{RepeaterID: testID, Callsign: "K9MLS"}, addrA)
	if len(out.Responses) != 0 {
		t.Error("configuration was accepted from a peer that had not authenticated")
	}
	if !strings.Contains(out.Dropped, "not authenticated") {
		t.Errorf("drop reason = %q", out.Dropped)
	}
}

func TestAuthenticatedPeerCannotPassTrafficUntilConfigured(t *testing.T) {
	h := newHarness(t)
	h.send(hbp.Login{RepeaterID: testID}, addrA)
	h.send(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(salt, []byte(testPassword))}, addrA)

	out := h.send(hbp.Data{RepeaterID: testID, SourceID: uint32(testID), TargetID: 3100}, addrA)
	if out.Data != nil {
		t.Error("a frame was accepted before the peer announced its configuration")
	}
	if !strings.Contains(out.Dropped, "complete registration") {
		t.Errorf("drop reason = %q", out.Dropped)
	}
}

func TestRegisteredPeerPassesTraffic(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	frame := hbp.Data{
		RepeaterID: testID, SourceID: uint32(testID), TargetID: 3100,
		Timeslot: hbp.Timeslot2, StreamID: 0xdeadbeef, Trailing: []byte{0, 0},
	}
	out := h.send(frame, addrA)
	if out.Data == nil {
		t.Fatalf("frame from a registered peer was dropped: %s", out.Dropped)
	}
	if out.From != testID {
		t.Errorf("frame attributed to %d, want %d", out.From, testID)
	}
	if out.Data.TargetID != 3100 || out.Data.StreamID != 0xdeadbeef {
		t.Errorf("frame fields were altered: %+v", out.Data)
	}
	if len(out.Responses) != 0 {
		t.Error("a data frame produced a response; masters do not acknowledge them")
	}
}

func TestKeepaliveIsAnswered(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	out := h.send(hbp.Ping{RepeaterID: testID}, addrA)
	if len(out.Responses) != 1 {
		t.Fatalf("keepalive produced %d responses, want 1 (%s)", len(out.Responses), out.Dropped)
	}
	msg, err := hbp.Parse(out.Responses[0].Payload)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pong, ok := msg.(hbp.Pong)
	if !ok {
		t.Fatalf("keepalive answered with %s, want MSTPONG", msg.Kind())
	}
	if pong.RepeaterID != testID {
		t.Errorf("pong carries repeater ID %d, want %d", pong.RepeaterID, testID)
	}
}

func TestGarbageIsDroppedWithAnExplanation(t *testing.T) {
	h := newHarness(t)
	for _, in := range [][]byte{
		nil, {}, []byte("XXXX"), []byte("RPTL"), make([]byte, 1500),
	} {
		out := h.m.Handle(in, addrA)
		if len(out.Responses) != 0 {
			t.Errorf("garbage input %q produced a response", in)
		}
		if out.Dropped == "" {
			t.Errorf("garbage input %q was discarded without an explanation", in)
		}
	}
	if h.m.Count() != 0 {
		t.Error("garbage created a registration")
	}
}
