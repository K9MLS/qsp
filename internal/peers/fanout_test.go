package peers_test

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// Synthetic peers, to test what one hotspot cannot.
//
// Every hardware run so far has used a single peer with forwarding off, so
// audio has never crossed between two stations — the product's core function.
// README.md claimed it did. These tests are what makes that claim true, or
// would have caught it being false.
//
// A syntheticPeer is not a mock. It performs the six-step login using the same
// hbp package the master validated against a WPSD hotspot on 2026-08-23, and it
// transmits the DMRD frames captured from a real radio on 2026-08-25. If the
// handshake or the frame layout changes, these break — which is the point.

// syntheticPeer speaks the peer half of the Homebrew Protocol.
type syntheticPeer struct {
	id       hbp.RepeaterID
	callsign string
	addr     netip.AddrPort
	password []byte
}

func newSyntheticPeer(id hbp.RepeaterID, n int, password []byte) *syntheticPeer {
	return &syntheticPeer{
		id:       id,
		callsign: fmt.Sprintf("N0CALL%d", n),
		// Distinct source addresses: the master keys sessions on them, and
		// sharing one would hide rebinding bugs.
		addr:     netip.MustParseAddrPort(fmt.Sprintf("10.0.%d.%d:62031", n/250, n%250+1)),
		password: password,
	}
}

// register drives RPTL → RPTACK(salt) → RPTK → RPTACK → RPTC → RPTACK.
//
// It asserts at each step rather than at the end, so a failure names the step
// that broke instead of reporting that registration did not happen.
func (p *syntheticPeer) register(t *testing.T, m *peers.Master) {
	t.Helper()

	out := m.Handle(hbp.Login{RepeaterID: p.id}.Marshal(), p.addr)
	if len(out.Responses) != 1 {
		t.Fatalf("peer %d: RPTL produced %d responses, want 1 (%s)", p.id, len(out.Responses), out.Dropped)
	}
	msg, err := hbp.Parse(out.Responses[0].Payload)
	if err != nil {
		t.Fatalf("peer %d: cannot parse the salt: %v", p.id, err)
	}
	ack, ok := msg.(hbp.Ack)
	if !ok {
		t.Fatalf("peer %d: RPTL answered with %T, want hbp.Ack carrying a salt", p.id, msg)
	}

	out = m.Handle(hbp.Key{
		RepeaterID: p.id,
		Digest:     hbp.Digest(ack.Payload, p.password),
	}.Marshal(), p.addr)
	if len(out.Responses) != 1 {
		t.Fatalf("peer %d: RPTK rejected: %s", p.id, out.Dropped)
	}

	out = m.Handle(hbp.Config{
		RepeaterID:  p.id,
		Callsign:    p.callsign,
		ColorCode:   "01",
		Slots:       "4",
		Description: "synthetic",
		SoftwareID:  "qsp-test",
	}.Marshal(), p.addr)
	if len(out.Responses) != 1 {
		t.Fatalf("peer %d: RPTC rejected: %s", p.id, out.Dropped)
	}
}

// transmit replays captured frames as though this peer had sent them.
//
// Source and repeater IDs are rewritten to this peer; everything else is left
// exactly as it came off the wire, including the trailing bytes MMDVMHost
// appends.
func (p *syntheticPeer) transmit(t *testing.T, m *peers.Master, frames []hbp.Data, tg uint32, slot hbp.Timeslot) []peers.Outcome {
	t.Helper()

	outcomes := make([]peers.Outcome, 0, len(frames))
	for _, f := range frames {
		f.SourceID = uint32(p.id)
		f.RepeaterID = p.id
		f.TargetID = tg
		f.Timeslot = slot

		out := m.Handle(f.Marshal(), p.addr)
		if out.Data == nil {
			t.Fatalf("peer %d: frame %d was not accepted: %s", p.id, f.Sequence, out.Dropped)
		}
		outcomes = append(outcomes, out)
	}
	return outcomes
}

// liveFrames returns one complete transmission captured from a real radio.
//
// Two subtleties, both learned by getting this wrong first.
//
// The capture was taken with `tcpdump -i any`, so it holds the master link and
// the MMDVMHost/DMRGateway loopback leg side by side. Every frame appears
// twice. Grouping by stream ID and taking one stream removes the duplicates as
// a side effect of a more meaningful selection.
//
// And it holds five separate transmissions. Replaying them back to back would
// be five people keying up with no gap, which the routing core correctly
// refuses under ADR-0014 contention — the destination is still reserved by the
// previous stream. A relay test should exercise relaying, not contention, so it
// uses a single stream. Contention has its own tests.
func liveFrames(t *testing.T) []hbp.Data {
	t.Helper()

	payloads, err := readCaptureRaw("../../testdata/hbp/hbp-voice-live.pcap")
	if err != nil {
		t.Fatalf("reading the live capture: %v", err)
	}

	streams := make(map[hbp.StreamID][]hbp.Data)
	var order []hbp.StreamID
	for _, raw := range payloads {
		msg, err := hbp.Parse(raw)
		if err != nil {
			continue // control traffic and the gateway dialect
		}
		d, ok := msg.(hbp.Data)
		if !ok {
			continue
		}
		if _, seen := streams[d.StreamID]; !seen {
			order = append(order, d.StreamID)
		}
		streams[d.StreamID] = append(streams[d.StreamID], d)
	}
	if len(order) == 0 {
		t.Fatal("no voice frames in the capture; the fixture or the reader is wrong")
	}

	// The longest stream, so the test covers the most speech.
	best := order[0]
	for _, id := range order {
		if len(streams[id]) > len(streams[best]) {
			best = id
		}
	}

	// Both legs of the capture carry identical frames. Keep one of each
	// sequence number.
	seen := make(map[uint8]bool)
	var frames []hbp.Data
	for _, d := range streams[best] {
		if seen[d.Sequence] {
			continue
		}
		seen[d.Sequence] = true
		frames = append(frames, d)
	}

	t.Logf("using stream %#x: %d frames of %d captured across %d transmissions",
		best, len(frames), len(payloads), len(order))
	return frames
}

// readyPeers adapts peers.Master to routing.PeerLookup.
type readyPeers struct{ m *peers.Master }

func (r readyPeers) Ready(id hbp.RepeaterID) bool {
	p, ok := r.m.Lookup(id)
	return ok && p.State == peers.StateConfigured
}

func (r readyPeers) ReadyPeers() []hbp.RepeaterID {
	var out []hbp.RepeaterID
	for _, p := range r.m.Peers() {
		if p.State == peers.StateConfigured {
			out = append(out, p.ID)
		}
	}
	return out
}

func testMaster(t *testing.T, password []byte, maxPeers int) *peers.Master {
	t.Helper()
	m, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return password, true },
		MaxPeers: maxPeers,
	})
	if err != nil {
		t.Fatalf("peers.NewMaster: %v", err)
	}
	return m
}

// -----------------------------------------------------------------------------

// TestAudioCrossesBetweenTwoPeers is the assertion README.md was making without
// evidence: that QSP relays audio between bridged talkgroups.
//
// Peer A transmits on TG 9; peer B is bridged to it on TG 91. Every frame must
// arrive at B, and each must carry B's repeater ID rather than A's — a relaying
// master re-stamps the frame as its own, and the wrong ID there is a bug the
// receiving hotspot would silently act on.
func TestAudioCrossesBetweenTwoPeers(t *testing.T) {
	const password = "test-password"
	m := testMaster(t, []byte(password), 8)

	a := newSyntheticPeer(3132910, 1, []byte(password))
	b := newSyntheticPeer(3132911, 2, []byte(password))
	a.register(t, m)
	b.register(t, m)

	if m.ConfiguredCount() != 2 {
		t.Fatalf("%d peers configured, want 2", m.ConfiguredCount())
	}

	table, err := routing.NewTable([]routing.Bridge{{
		Name:    "club",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: a.id, Talkgroup: 9, Timeslot: hbp.Timeslot2},
			{Peer: b.id, Talkgroup: 91, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}

	core, err := routing.NewCore(routing.CoreOptions{Table: table, Peers: readyPeers{m}})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	frames := liveFrames(t)
	now := time.Now()

	delivered := 0
	for _, out := range a.transmit(t, m, frames, 9, hbp.Timeslot2) {
		result := core.Route(out.From, *out.Data, now)
		now = now.Add(60 * time.Millisecond)

		for _, d := range result.Deliveries {
			if d.Peer != b.id {
				t.Fatalf("frame delivered to peer %d, want %d", d.Peer, b.id)
			}
			if d.Frame.RepeaterID != b.id {
				t.Errorf("delivered frame carries repeater ID %d, want %d rewritten for the destination",
					d.Frame.RepeaterID, b.id)
			}
			if d.Frame.TargetID != 91 {
				t.Errorf("delivered frame targets TG %d, want 91", d.Frame.TargetID)
			}
			if d.Frame.SourceID != uint32(a.id) {
				t.Errorf("delivered frame's source is %d, want %d — the originating radio must survive the relay",
					d.Frame.SourceID, a.id)
			}
			delivered++
		}
	}

	if delivered != len(frames) {
		t.Errorf("%d of %d frames crossed the bridge", delivered, len(frames))
	}
	t.Logf("relayed %d live frames from peer %d to peer %d", delivered, a.id, b.id)
}

// TestFanOutToManyPeers measures what one transmission costs at club scale.
//
// BLUEPRINT-v1 §4 estimates roughly 1,700 datagrams per second outbound when a
// hundred hotspots share a talkgroup. That was arithmetic, not measurement.
// This records the real number.
//
// Deliberately no timing threshold: a CI runner's speed is not QSP's, and a
// flaky performance gate is worse than none. It fails only on incorrect
// delivery, and logs the cost.
func TestFanOutToManyPeers(t *testing.T) {
	const (
		password  = "test-password"
		peerCount = 100
	)

	m := testMaster(t, []byte(password), peerCount+8)

	all := make([]*syntheticPeer, 0, peerCount)
	endpoints := make([]routing.Endpoint, 0, peerCount)
	for i := range peerCount {
		p := newSyntheticPeer(hbp.RepeaterID(3130000+i), i, []byte(password))
		p.register(t, m)
		all = append(all, p)
		endpoints = append(endpoints, routing.Endpoint{
			Peer: p.id, Talkgroup: 9, Timeslot: hbp.Timeslot2,
		})
	}

	if got := m.ConfiguredCount(); got != peerCount {
		t.Fatalf("%d peers registered, want %d", got, peerCount)
	}

	table, err := routing.NewTable([]routing.Bridge{{
		Name: "club", Enabled: true, Endpoints: endpoints,
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	core, err := routing.NewCore(routing.CoreOptions{Table: table, Peers: readyPeers{m}})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	frames := liveFrames(t)
	if len(frames) > 200 {
		frames = frames[:200] // about twelve seconds of speech
	}

	now := time.Now()
	start := time.Now()
	deliveries := 0

	for _, out := range all[0].transmit(t, m, frames, 9, hbp.Timeslot2) {
		result := core.Route(out.From, *out.Data, now)
		now = now.Add(60 * time.Millisecond)
		deliveries += len(result.Deliveries)
	}

	elapsed := time.Since(start)
	perFrame := float64(deliveries) / float64(len(frames))

	// One transmitting peer, so every other peer on the bridge should receive.
	if want := float64(peerCount - 1); perFrame != want {
		t.Errorf("each frame reached %.1f peers, want %.0f", perFrame, want)
	}

	t.Logf("%d peers: %d frames produced %d deliveries in %v (%.0f deliveries/sec of speech)",
		peerCount, len(frames), deliveries, elapsed,
		float64(deliveries)/(float64(len(frames))*0.06))
}

// TestRegistryHoldsAtClubScale checks the peer registry itself, without routing.
func TestRegistryHoldsAtClubScale(t *testing.T) {
	const password = "test-password"
	m := testMaster(t, []byte(password), 128)

	for i := range 100 {
		newSyntheticPeer(hbp.RepeaterID(3130000+i), i, []byte(password)).register(t, m)
	}

	if got := m.Count(); got != 100 {
		t.Errorf("registry holds %d peers, want 100", got)
	}
	if got := len(m.Peers()); got != 100 {
		t.Errorf("Peers() returned %d, want 100", got)
	}

	// Every peer must be individually findable: an admin looking for one
	// member among a hundred is BLUEPRINT-v1's Phase 2c gate.
	for i := range 100 {
		id := hbp.RepeaterID(3130000 + i)
		if _, ok := m.Lookup(id); !ok {
			t.Fatalf("peer %d is registered but not findable", id)
		}
	}
}

// TestMaxPeersIsEnforced guards the bound an operator sets.
//
// A club sizing a Pi for a hundred hotspots needs the limit to hold, and to be
// refused honestly rather than silently.
func TestMaxPeersIsEnforced(t *testing.T) {
	const password = "test-password"
	m := testMaster(t, []byte(password), 4)

	for i := range 4 {
		newSyntheticPeer(hbp.RepeaterID(3130000+i), i, []byte(password)).register(t, m)
	}

	extra := newSyntheticPeer(3139999, 200, []byte(password))
	out := m.Handle(hbp.Login{RepeaterID: extra.id}.Marshal(), extra.addr)
	if out.Dropped == "" {
		t.Error("a peer beyond max_peers was accepted without explanation")
	}
	if m.Count() > 4 {
		t.Errorf("registry holds %d peers, want at most 4", m.Count())
	}
	t.Logf("refused with: %s", out.Dropped)
}
