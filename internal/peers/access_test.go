package peers_test

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// withAccess installs a set of lists on a test master.
func withAccess(l access.Lists) func(*peers.MasterConfig) {
	return func(cfg *peers.MasterConfig) { cfg.Access = l }
}

func mustParse(t *testing.T, kind access.Kind, mode access.Mode, ids ...string) access.List {
	t.Helper()
	l, err := access.Parse("dmr.access.test", kind, mode, ids)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return l
}

func voice(source uint32, stream hbp.StreamID, seq uint8) hbp.Data {
	return hbp.Data{
		Sequence:   seq,
		SourceID:   source,
		TargetID:   9,
		RepeaterID: testID,
		Timeslot:   hbp.Timeslot2,
		CallType:   hbp.CallGroup,
		FrameType:  hbp.FrameTypeVoice,
		StreamID:   stream,
	}
}

// TestNoAccessListsChangeNothing is the compatibility guarantee at the master.
// An instance with no access block must behave exactly as it did before.
func TestNoAccessListsChangeNothing(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	out := h.send(voice(3121001, 0x1111, 0), addrA)
	if out.Dropped != "" {
		t.Fatalf("a frame was dropped with no access lists configured: %s", out.Dropped)
	}
	if out.Data == nil {
		t.Fatal("no frame was accepted with no access lists configured")
	}
}

func TestRegistrationListRefusesAnUnlistedRepeater(t *testing.T) {
	// A permit list naming somebody else.
	h := newHarness(t, withAccess(access.Lists{
		Registration: mustParse(t, access.Registration, access.ModePermit, "312999"),
	}))

	out := h.send(hbp.Login{RepeaterID: testID}, addrA)
	if out.Dropped == "" {
		t.Fatal("a repeater not on the registration list was allowed to log in")
	}
	if !strings.Contains(out.Dropped, "dmr.access.registration") {
		t.Errorf("the refusal should name the list that refused: %q", out.Dropped)
	}
	// A refused peer still gets MSTNAK, so its operator sees a rejection in
	// their own log rather than silence.
	if len(out.Responses) != 1 {
		t.Fatalf("a refused login produced %d responses, want a NAK", len(out.Responses))
	}
	if msg, err := hbp.Parse(out.Responses[0].Payload); err != nil {
		t.Fatalf("the NAK is unparseable: %v", err)
	} else if _, ok := msg.(hbp.Nak); !ok {
		t.Errorf("a refused login was answered with %s, want MSTNAK", msg.Kind())
	}
	if h.m.Count() != 0 {
		t.Error("a refused login left a peer in the registry")
	}
}

func TestRegistrationListAdmitsAListedRepeater(t *testing.T) {
	h := newHarness(t, withAccess(access.Lists{
		Registration: mustParse(t, access.Registration, access.ModePermit, "3132910"),
	}))
	h.login(addrA)
	if h.m.Count() != 1 {
		t.Error("a permitted repeater did not register")
	}
}

// TestRegistrationIsCheckedBeforeThePassword matters for diagnosis. "Wrong
// password" and "not permitted here" are very different messages to an operator
// whose hotspot will not connect, and the list must not be discoverable by
// guessing credentials.
func TestRegistrationIsCheckedBeforeThePassword(t *testing.T) {
	// An ID with no password configured *and* refused by the list. If the
	// password were checked first, the message would name the password.
	h := newHarness(t, withAccess(access.Lists{
		Registration: mustParse(t, access.Registration, access.ModeDeny, "999999"),
	}))

	out := h.send(hbp.Login{RepeaterID: 999999}, addrA)
	if out.Dropped == "" {
		t.Fatal("a denied repeater was allowed to log in")
	}
	if !strings.Contains(out.Dropped, "dmr.access.registration") {
		t.Errorf("the access list should refuse before the password lookup: %q", out.Dropped)
	}
	if strings.Contains(out.Dropped, "password") {
		t.Errorf("the refusal names the password, so the list was checked second: %q", out.Dropped)
	}
}

func TestSubscriberListRefusesAnUnlistedRadio(t *testing.T) {
	h := newHarness(t, withAccess(access.Lists{
		Subscriber: mustParse(t, access.Subscriber, access.ModeDeny, "3121077"),
	}))
	h.login(addrA)

	out := h.send(voice(3121077, 0x2222, 0), addrA)
	if out.Data != nil {
		t.Fatal("a frame from a denied subscriber was accepted for routing")
	}
	if out.Dropped == "" {
		t.Fatal("a denied subscriber's frame was dropped silently")
	}
	if !strings.Contains(out.Dropped, "dmr.access.subscribers") {
		t.Errorf("the refusal should name the list that refused: %q", out.Dropped)
	}

	// A different radio on the same peer is unaffected.
	out = h.send(voice(3121001, 0x3333, 0), addrA)
	if out.Data == nil {
		t.Fatalf("a permitted subscriber was refused: %s", out.Dropped)
	}
}

// TestARefusedSubscriberDoesNotDisconnectItsPeer is the reason the subscriber
// check sits where it does. On DMR a hotspot is shared infrastructure, and the
// offending party is a radio.
func TestARefusedSubscriberDoesNotDisconnectItsPeer(t *testing.T) {
	h := newHarness(t, withAccess(access.Lists{
		Subscriber: mustParse(t, access.Subscriber, access.ModeDeny, "3121077"),
	}))
	h.login(addrA)

	// A refused transmission still counts as hearing from the peer, or a
	// hotspot carrying a banned radio would eventually time out for it.
	h.c.advance(50 * time.Second)
	if out := h.send(voice(3121077, 0x4444, 0), addrA); out.Data != nil {
		t.Fatal("the denied frame was carried")
	}
	h.c.advance(50 * time.Second)

	if events := h.m.Expire(); len(events) != 0 {
		t.Errorf("the peer was expired despite being heard from: %+v", events)
	}
	if h.m.Count() != 1 {
		t.Error("a refused subscriber cost its peer its registration")
	}
}

// TestARefusedStreamIsAnnouncedOnce is ADR-0020's logging rule. Five hundred
// identical lines would rotate an operator's journal past the evidence they
// needed.
func TestARefusedStreamIsAnnouncedOnce(t *testing.T) {
	h := newHarness(t, withAccess(access.Lists{
		Subscriber: mustParse(t, access.Subscriber, access.ModeDeny, "3121077"),
	}))
	h.login(addrA)

	// The opening frame explains itself.
	first := h.send(voice(3121077, 0x5555, 0), addrA)
	if strings.Contains(first.Dropped, "frame ") {
		t.Errorf("the first refusal of a stream should be the full explanation: %q", first.Dropped)
	}

	// The rest are counted rather than re-explained — but never silent: a
	// caller counting drops still sees every one.
	for seq := uint8(1); seq < 6; seq++ {
		out := h.send(voice(3121077, 0x5555, seq), addrA)
		if out.Dropped == "" {
			t.Fatalf("frame %d of a refused stream was dropped silently", seq)
		}
		if out.Data != nil {
			t.Fatalf("frame %d of a refused stream was carried", seq)
		}
		if !strings.Contains(out.Dropped, "frame ") {
			t.Errorf("frame %d should be counted rather than re-explained: %q", seq, out.Dropped)
		}
	}

	// A new transmission from the same radio explains itself again.
	next := h.send(voice(3121077, 0x6666, 0), addrA)
	if strings.Contains(next.Dropped, "frame ") {
		t.Errorf("a new refused stream should explain itself again: %q", next.Dropped)
	}
}

// TestRefusalsOnDifferentTimeslotsAreDistinct guards the identity of a refused
// stream. A peer can be refused on one slot while talking on the other.
func TestRefusalsOnDifferentTimeslotsAreDistinct(t *testing.T) {
	h := newHarness(t, withAccess(access.Lists{
		Subscriber: mustParse(t, access.Subscriber, access.ModeDeny, "3121077"),
	}))
	h.login(addrA)

	one := voice(3121077, 0x7777, 0)
	one.Timeslot = hbp.Timeslot1
	two := voice(3121077, 0x7777, 0)
	two.Timeslot = hbp.Timeslot2

	if out := h.send(one, addrA); strings.Contains(out.Dropped, "frame ") {
		t.Errorf("the first refusal on TS1 should explain itself: %q", out.Dropped)
	}
	// Same source and stream ID, different slot: a separate transmission.
	if out := h.send(two, addrA); strings.Contains(out.Dropped, "frame ") {
		t.Errorf("a refusal on TS2 should not be counted against TS1: %q", out.Dropped)
	}
}

// TestAPermittedStreamClearsTheRefusal stops the counter leaking across
// transmissions, which would make a later refused stream look like a
// continuation and log nothing at all.
func TestAPermittedStreamClearsTheRefusal(t *testing.T) {
	h := newHarness(t, withAccess(access.Lists{
		Subscriber: mustParse(t, access.Subscriber, access.ModeDeny, "3121077"),
	}))
	h.login(addrA)

	h.send(voice(3121077, 0x8888, 0), addrA)
	if out := h.send(voice(3121001, 0x8888, 1), addrA); out.Data == nil {
		t.Fatalf("a permitted subscriber was refused: %s", out.Dropped)
	}
	// The refused radio returns on the same stream ID. Because a permitted
	// frame intervened, this is a fresh refusal and must explain itself.
	if out := h.send(voice(3121077, 0x8888, 2), addrA); strings.Contains(out.Dropped, "frame ") {
		t.Errorf("a refusal after a permitted frame should explain itself: %q", out.Dropped)
	}
}

// TestTalkgroupListsAreNotConsultedHere records the division of labour. A
// talkgroup is a routing question, answered where destinations are known.
func TestTalkgroupListsAreNotConsultedHere(t *testing.T) {
	h := newHarness(t, withAccess(access.Lists{
		Talkgroup2: mustParse(t, access.Talkgroup, access.ModePermit, "3100"),
	}))
	h.login(addrA)

	// TG 9 on TS2 is not on that permit list, and the master carries it
	// regardless: refusing here would drop the frame before any destination
	// was known, including destinations the list would have allowed.
	if out := h.send(voice(3121001, 0x9999, 0), addrA); out.Data == nil {
		t.Fatalf("the master refused on a talkgroup list: %s", out.Dropped)
	}
}

// TestAStalePeerIsToldToLogInAgain is the restart case, observed live on
// 2026-08-28: the master restarted, the peer kept sending keepalives to a
// registry that no longer held it, and every one was dropped in silence. The
// network was dead for the minute it took the peer's own timeout to fire.
func TestAStalePeerIsToldToLogInAgain(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	// A new master, as if the process had restarted. The peer knows nothing
	// about it and carries on as before.
	restarted := newHarness(t)

	out := restarted.send(hbp.Ping{RepeaterID: testID}, addrA)
	if len(out.Responses) != 1 {
		t.Fatalf("a stale keepalive produced %d responses; it must be answered so the peer re-logs in",
			len(out.Responses))
	}
	msg, err := hbp.Parse(out.Responses[0].Payload)
	if err != nil {
		t.Fatalf("the answer is unparseable: %v", err)
	}
	nak, ok := msg.(hbp.Nak)
	if !ok {
		t.Fatalf("a stale keepalive was answered with %s, want MSTNAK", msg.Kind())
	}
	if nak.RepeaterID != testID {
		t.Errorf("the NAK names repeater %d, want %d", nak.RepeaterID, testID)
	}
	if out.Dropped == "" {
		t.Error("the keepalive was discarded without an explanation")
	}
	if restarted.m.Count() != 0 {
		t.Error("answering a stale keepalive created a registration")
	}

	// And the peer can then log in again, which is the whole point.
	restarted.login(addrA)
	if restarted.m.Count() != 1 {
		t.Error("the peer could not re-register after being told to")
	}
}

// TestAStaleTransmissionIsAnsweredOnce is the measured half of the same
// decision, and it moved.
//
// It used to assert that stale voice frames were never answered at all,
// because voice arrives every 60 ms and answering each would put hundreds of
// datagrams on the wire. That is right about answering each and wrong about
// answering at all, and **the difference was a second of somebody's audio**:
// after the 23:27 restart on 2026-09-06 a station keyed up and eighteen
// consecutive frames were dropped in silence over 1.02 seconds, until the
// hotspot's own keepalive arrived and was answered.
//
// One answer per transmission gives the peer the same instruction the keepalive
// would have, seconds earlier, for one datagram instead of hundreds.
func TestAStaleTransmissionIsAnsweredOnce(t *testing.T) {
	h := newHarness(t)

	var answered int
	for seq := uint8(0); seq < 20; seq++ {
		out := h.send(voice(3121001, 0xEEEE, seq), addrA)
		answered += len(out.Responses)
		if out.Dropped == "" {
			t.Error("a stale voice frame was discarded without an explanation")
		}
		if out.Data != nil {
			t.Fatal("a frame was accepted from an unregistered station")
		}
		h.c.advance(60 * time.Millisecond)
	}
	if answered != 1 {
		t.Errorf("twenty frames of one transmission produced %d answers, want 1", answered)
	}
	if h.m.Count() != 0 {
		t.Error("answering a stale frame created a registration")
	}
}

// TestAStaleTransmissionIsAnsweredAgainLater checks the other edge of the
// suppression: a peer that ignored the first MSTNAK, or one that keys up again
// much later, must not be met with permanent silence.
func TestAStaleTransmissionIsAnsweredAgainLater(t *testing.T) {
	h := newHarness(t)

	if out := h.send(voice(3121001, 0xEEEE, 0), addrA); len(out.Responses) != 1 {
		t.Fatalf("the first stale frame produced %d responses, want 1", len(out.Responses))
	}
	if out := h.send(voice(3121001, 0xEEEE, 1), addrA); len(out.Responses) != 0 {
		t.Fatalf("the second frame was answered too; the suppression is not working")
	}

	h.c.advance(6 * time.Second)
	if out := h.send(voice(3121001, 0xEEEE, 2), addrA); len(out.Responses) != 1 {
		t.Errorf("a frame after the interval produced %d responses, want 1", len(out.Responses))
	}
}

// TestOneStaleStationDoesNotSilenceAnother checks the suppression is per
// repeater ID rather than global. Two hotspots come back from a restart at the
// same moment, and each needs telling.
//
// The frames are built here rather than with voice(), which hardcodes
// RepeaterID and takes a *source radio* as its argument. Writing this test
// against what that helper looked like it did produced a failure that blamed
// the code — the same mistake as reading a constant off a hex dump by eye, one
// layer up.
func TestOneStaleStationDoesNotSilenceAnother(t *testing.T) {
	h := newHarness(t)

	stale := func(repeater hbp.RepeaterID) hbp.Data {
		return hbp.Data{
			RepeaterID: repeater,
			SourceID:   uint32(repeater),
			TargetID:   9,
			Timeslot:   hbp.Timeslot2,
			CallType:   hbp.CallGroup,
			FrameType:  hbp.FrameTypeVoice,
			StreamID:   hbp.StreamID(repeater),
		}
	}

	if out := h.send(stale(3121001), addrA); len(out.Responses) != 1 {
		t.Fatalf("the first station produced %d responses, want 1", len(out.Responses))
	}
	if out := h.send(stale(3121002), addrB); len(out.Responses) != 1 {
		t.Errorf("a second station was silenced by the first station's answer")
	}
}
