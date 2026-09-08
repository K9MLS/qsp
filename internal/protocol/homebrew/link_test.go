package homebrew_test

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/homebrew"
)

const (
	linkID   = hbp.RepeaterID(3132910)
	linkPass = "far-end-password"
)

var farSalt = [4]byte{0x11, 0x22, 0x33, 0x44}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

type harness struct {
	t *testing.T
	l *homebrew.Link
	c *clock
}

func newHarness(t *testing.T, opts ...func(*homebrew.Config)) *harness {
	t.Helper()
	c := &clock{t: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)}
	cfg := homebrew.Config{
		Name:       "xlx950",
		RepeaterID: linkID,
		Password:   []byte(linkPass),
		Identity:   homebrew.Identity{Callsign: "K9MLS"},
		Now:        c.now,
	}
	for _, o := range opts {
		o(&cfg)
	}
	l, err := homebrew.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &harness{t: t, l: l, c: c}
}

// parse decodes a datagram the link decided to send.
func (h *harness) parse(payload []byte) hbp.Message {
	h.t.Helper()
	msg, err := hbp.Parse(payload)
	if err != nil {
		h.t.Fatalf("the link sent an unparseable datagram: %v", err)
	}
	return msg
}

// connect drives a full successful handshake.
func (h *harness) connect() {
	h.t.Helper()

	out := h.l.Start()
	if len(out.Send) != 1 {
		h.t.Fatalf("Start sent %d datagrams, want 1", len(out.Send))
	}
	if _, ok := h.parse(out.Send[0]).(hbp.Login); !ok {
		h.t.Fatalf("Start sent %s, want RPTL", h.parse(out.Send[0]).Kind())
	}

	out = h.l.Handle(hbp.Ack{Payload: farSalt}.Marshal())
	if len(out.Send) != 1 {
		h.t.Fatalf("the challenge produced %d datagrams, want 1 (%s)", len(out.Send), out.Note)
	}
	key, ok := h.parse(out.Send[0]).(hbp.Key)
	if !ok {
		h.t.Fatalf("answered the challenge with %s, want RPTK", h.parse(out.Send[0]).Kind())
	}
	if key.Digest != hbp.Digest(farSalt, []byte(linkPass)) {
		h.t.Fatal("the digest does not match the salt and password")
	}

	out = h.l.Handle(hbp.Ack{}.Marshal())
	if len(out.Send) != 1 {
		h.t.Fatalf("authentication produced %d datagrams, want 1 (%s)", len(out.Send), out.Note)
	}
	if _, ok := h.parse(out.Send[0]).(hbp.Config); !ok {
		h.t.Fatalf("sent %s after authenticating, want RPTC", h.parse(out.Send[0]).Kind())
	}

	out = h.l.Handle(hbp.Ack{}.Marshal())
	if h.l.State() != homebrew.StateConnected {
		h.t.Fatalf("state is %q after a full handshake, want connected (%s)", h.l.State(), out.Note)
	}
}

// TestTheHandshakeMirrorsTheMasterSide is the whole feature: QSP has spoken the
// master side since phase 1, and this is the same conversation from the other
// end.
func TestTheHandshakeMirrorsTheMasterSide(t *testing.T) {
	h := newHarness(t)
	h.connect()

	if !h.l.EverConnected() {
		t.Error("a completed handshake did not record that the link has worked")
	}
}

func TestIdentityReachesTheFarEnd(t *testing.T) {
	h := newHarness(t, func(c *homebrew.Config) {
		c.Identity = homebrew.Identity{
			Callsign: "K9MLS", Location: "Denton, TX", Description: "QSP",
			ColourCode: 11, Timeslots: 2, Latitude: 33.2148, Longitude: -97.1331,
		}
	})

	h.l.Start()
	h.l.Handle(hbp.Ack{Payload: farSalt}.Marshal())
	out := h.l.Handle(hbp.Ack{}.Marshal())

	cfg, ok := h.parse(out.Send[0]).(hbp.Config)
	if !ok {
		t.Fatalf("want RPTC, got %s", h.parse(out.Send[0]).Kind())
	}
	if cfg.Callsign != "K9MLS" {
		t.Errorf("callsign is %q", cfg.Callsign)
	}
	if !strings.Contains(cfg.Location, "Denton") {
		t.Errorf("location is %q", cfg.Location)
	}
	if cfg.ColorCode != "11" {
		t.Errorf("colour code is %q", cfg.ColorCode)
	}
	if cfg.RepeaterID != linkID {
		t.Errorf("RPTC carries repeater %d, want %d", cfg.RepeaterID, linkID)
	}
	// The far end shows the software to its users; saying what QSP is beats
	// leaving them to guess at an unfamiliar station.
	if cfg.SoftwareID == "" {
		t.Error("no software ID was announced")
	}
}

// TestACallsignIsRequired. A blank one appears on somebody else's dashboard as
// an unidentified station, so it fails at construction rather than on the air.
func TestACallsignIsRequired(t *testing.T) {
	_, err := homebrew.New(homebrew.Config{
		Name: "xlx950", RepeaterID: linkID, Password: []byte(linkPass),
	})
	if err == nil {
		t.Fatal("a link with no callsign was constructed")
	}
	if !strings.Contains(err.Error(), "callsign") {
		t.Errorf("the error should name the callsign: %v", err)
	}
}

func TestConstructionRequiresCredentials(t *testing.T) {
	base := homebrew.Config{Name: "x", RepeaterID: linkID, Password: []byte("p"),
		Identity: homebrew.Identity{Callsign: "K9MLS"}}

	noID := base
	noID.RepeaterID = 0
	if _, err := homebrew.New(noID); err == nil {
		t.Error("a link with no repeater ID was constructed")
	}

	noPass := base
	noPass.Password = nil
	if _, err := homebrew.New(noPass); err == nil {
		t.Error("a link with no password was constructed")
	}
}

// TestARefusalBacksOff. A wrong password fails identically on every retry, so
// hammering the far end helps nobody.
func TestARefusalBacksOff(t *testing.T) {
	h := newHarness(t, func(c *homebrew.Config) {
		c.MinBackoff = 5 * time.Second
		c.MaxBackoff = time.Minute
	})
	h.l.Start()

	out := h.l.Handle(hbp.Nak{RepeaterID: linkID}.Marshal())
	if h.l.State() != homebrew.StateBackoff {
		t.Fatalf("state is %q after a NAK, want backoff", h.l.State())
	}
	if !strings.Contains(out.Note, "password") {
		t.Errorf("the note should suggest what to check: %q", out.Note)
	}

	// Too early.
	h.c.advance(4 * time.Second)
	if got := h.l.Tick(); len(got.Send) != 0 {
		t.Error("the link retried before its backoff elapsed")
	}
	// Due.
	h.c.advance(2 * time.Second)
	if got := h.l.Tick(); len(got.Send) != 1 {
		t.Fatalf("the link did not retry when due: %q", got.Note)
	}
	if h.l.State() != homebrew.StateLoggingIn {
		t.Errorf("state is %q after retrying, want logging-in", h.l.State())
	}
}

// TestBackoffGrowsAndIsBounded. The failures worth backing off from are the
// ones that repeat.
func TestBackoffGrowsAndIsBounded(t *testing.T) {
	h := newHarness(t, func(c *homebrew.Config) {
		c.MinBackoff = time.Second
		c.MaxBackoff = 8 * time.Second
	})
	h.l.Start()

	var delays []time.Duration
	for i := 0; i < 6; i++ {
		h.l.Handle(hbp.Nak{RepeaterID: linkID}.Marshal())
		// Find the delay by advancing until the retry fires.
		var waited time.Duration
		for step := 0; step < 200; step++ {
			h.c.advance(time.Second)
			waited += time.Second
			if out := h.l.Tick(); len(out.Send) > 0 {
				break
			}
		}
		delays = append(delays, waited)
	}

	if delays[0] >= delays[1] || delays[1] >= delays[2] {
		t.Errorf("backoff did not grow: %v", delays)
	}
	for _, d := range delays {
		if d > 9*time.Second {
			t.Errorf("backoff exceeded its bound: %v", delays)
		}
	}
}

// TestABrokenLinkRecoversByItself is why the retry exists. A link that stays
// down after one network blip is worth less than no link, because an operator
// will believe it is working.
func TestABrokenLinkRecoversByItself(t *testing.T) {
	h := newHarness(t, func(c *homebrew.Config) {
		c.Timeout = 30 * time.Second
		c.MinBackoff = 5 * time.Second
	})
	h.connect()

	// The far end goes silent.
	h.c.advance(31 * time.Second)
	if out := h.l.Tick(); h.l.State() != homebrew.StateBackoff {
		t.Fatalf("a silent far end left the link %q: %q", h.l.State(), out.Note)
	}

	// It comes back.
	h.c.advance(6 * time.Second)
	if out := h.l.Tick(); len(out.Send) != 1 {
		t.Fatal("the link did not retry")
	}
	h.l.Handle(hbp.Ack{Payload: farSalt}.Marshal())
	h.l.Handle(hbp.Ack{}.Marshal())
	out := h.l.Handle(hbp.Ack{}.Marshal())

	if h.l.State() != homebrew.StateConnected {
		t.Fatalf("the link did not recover: %q", h.l.State())
	}
	// A reconnection reads differently from a first connection, because the
	// two usually mean different things.
	if !strings.Contains(out.Note, "reconnected") {
		t.Errorf("a recovery should read as a reconnection: %q", out.Note)
	}
}

// TestBackoffResetsOnlyOnACompletedHandshake. Resetting earlier would let a
// link that authenticates and then fails at the configuration step retry at
// the minimum delay forever.
func TestBackoffResetsOnlyOnACompletedHandshake(t *testing.T) {
	h := newHarness(t, func(c *homebrew.Config) {
		c.MinBackoff = time.Second
		c.MaxBackoff = time.Minute
	})
	h.connect()

	// A completed handshake resets it, so the first retry after a later
	// failure is quick.
	h.c.advance(2 * time.Minute)
	h.l.Tick() // times out into backoff
	h.c.advance(2 * time.Second)
	if out := h.l.Tick(); len(out.Send) != 1 {
		t.Error("backoff was not reset by a successful connection")
	}
}

func TestKeepalivesAreSentWhileConnected(t *testing.T) {
	h := newHarness(t, func(c *homebrew.Config) { c.Keepalive = 10 * time.Second })
	h.connect()

	if out := h.l.Tick(); len(out.Send) != 0 {
		t.Error("a keepalive was sent immediately after connecting")
	}
	h.c.advance(11 * time.Second)
	out := h.l.Tick()
	if len(out.Send) != 1 {
		t.Fatalf("no keepalive after the interval elapsed")
	}
	if _, ok := h.parse(out.Send[0]).(hbp.Ping); !ok {
		t.Errorf("sent %s, want RPTPING", h.parse(out.Send[0]).Kind())
	}
}

// TestAnyAnswerCountsAsAlive. A master answering every ping with a NAK would
// otherwise look silent and be dropped for the wrong reason.
func TestAnyAnswerCountsAsAlive(t *testing.T) {
	h := newHarness(t, func(c *homebrew.Config) { c.Timeout = 30 * time.Second })
	h.connect()

	for i := 0; i < 5; i++ {
		h.c.advance(20 * time.Second)
		h.l.Handle(hbp.Pong{RepeaterID: linkID}.Marshal())
		if out := h.l.Tick(); h.l.State() != homebrew.StateConnected {
			t.Fatalf("a link answering keepalives was dropped: %q", out.Note)
		}
	}
}

// TestFramesOnlyFlowWhenConnected. A frame queued during a reconnection would
// arrive after the transmission it belonged to had ended, which is worse than
// not arriving.
func TestFramesOnlyFlowWhenConnected(t *testing.T) {
	h := newHarness(t)
	frame := hbp.Data{SourceID: 3121001, TargetID: 9, Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup}

	if h.l.Send(frame) != nil {
		t.Error("a frame was sent before the link connected")
	}
	h.connect()
	payload := h.l.Send(frame)
	if payload == nil {
		t.Fatal("a connected link refused to send")
	}
	sent, ok := h.parse(payload).(hbp.Data)
	if !ok {
		t.Fatalf("sent %s, want DMRD", h.parse(payload).Kind())
	}
	// The far end registered this link's ID, not the originating peer's.
	if sent.RepeaterID != linkID {
		t.Errorf("the frame names repeater %d, want the link's %d", sent.RepeaterID, linkID)
	}
	if sent.TargetID != 9 || sent.SourceID != 3121001 {
		t.Error("the frame's own addressing was altered")
	}
}

// TestFramesBeforeTheHandshakeAreIgnored. The far end should not be sending
// yet, and the link is not established enough to route what arrives.
func TestFramesBeforeTheHandshakeAreIgnored(t *testing.T) {
	h := newHarness(t)
	h.l.Start()

	out := h.l.Handle(hbp.Data{SourceID: 1, TargetID: 9, Timeslot: hbp.Timeslot2}.Marshal())
	if out.Data != nil {
		t.Error("a frame arriving mid-handshake was accepted for routing")
	}
	if out.Note == "" {
		t.Error("the frame was ignored without an explanation")
	}
}

func TestFramesAreDeliveredWhenConnected(t *testing.T) {
	h := newHarness(t)
	h.connect()

	out := h.l.Handle(hbp.Data{SourceID: 3121002, TargetID: 91, Timeslot: hbp.Timeslot1}.Marshal())
	if out.Data == nil {
		t.Fatalf("a frame from a connected far end was not delivered: %q", out.Note)
	}
	if out.Data.TargetID != 91 {
		t.Errorf("the frame was altered: target %d", out.Data.TargetID)
	}
}

// TestOutOfOrderAcksDoNotAdvanceTheHandshake is what stops a spoofed
// acknowledgement pushing the link into a state it has not earned.
func TestOutOfOrderAcksDoNotAdvanceTheHandshake(t *testing.T) {
	h := newHarness(t)

	// An ack before anything was sent.
	out := h.l.Handle(hbp.Ack{}.Marshal())
	if h.l.State() != homebrew.StateIdle {
		t.Errorf("an unsolicited ack moved the link to %q", h.l.State())
	}
	if out.Note == "" {
		t.Error("the unexpected ack was dropped without an explanation")
	}

	h.connect()
	// And once connected, further acks change nothing.
	before := h.l.State()
	h.l.Handle(hbp.Ack{}.Marshal())
	if h.l.State() != before {
		t.Errorf("an ack while connected moved the link to %q", h.l.State())
	}
}

func TestAStalledHandshakeGivesUp(t *testing.T) {
	h := newHarness(t, func(c *homebrew.Config) { c.Timeout = 30 * time.Second })
	h.l.Start()

	// The far end never answers the login.
	h.c.advance(31 * time.Second)
	out := h.l.Tick()
	if h.l.State() != homebrew.StateBackoff {
		t.Fatalf("a stalled handshake left the link %q", h.l.State())
	}
	if !strings.Contains(out.Note, "logging-in") {
		t.Errorf("the note should say where it stalled: %q", out.Note)
	}
}

func TestMasterCloseDropsTheLink(t *testing.T) {
	h := newHarness(t)
	h.connect()

	out := h.l.Handle(hbp.MasterClose{RepeaterID: linkID}.Marshal())
	if h.l.State() != homebrew.StateBackoff {
		t.Errorf("MSTCL left the link %q", h.l.State())
	}
	if !strings.Contains(out.Note, "closed") {
		t.Errorf("the note should say the far end closed it: %q", out.Note)
	}
}

func TestCloseTellsTheFarEnd(t *testing.T) {
	h := newHarness(t)
	h.connect()

	out := h.l.Close()
	if len(out.Send) != 1 {
		t.Fatal("closing sent nothing; the far end would wait out its own timeout")
	}
	if _, ok := h.parse(out.Send[0]).(hbp.RepeaterClose); !ok {
		t.Errorf("sent %s, want RPTCL", h.parse(out.Send[0]).Kind())
	}
	if h.l.State() != homebrew.StateIdle {
		t.Errorf("state is %q after closing, want idle", h.l.State())
	}
	// Closing an idle link sends nothing rather than a stray datagram.
	if got := h.l.Close(); len(got.Send) != 0 {
		t.Error("closing an idle link sent something")
	}
}

// TestGarbageIsNotFatal. The far end is not trusted, and a malformed datagram
// must not take the link down or panic.
func TestGarbageIsNotFatal(t *testing.T) {
	h := newHarness(t)
	h.connect()

	for _, junk := range [][]byte{
		nil, {}, []byte("x"), []byte("NOPE"), []byte("RPTACK"),
		make([]byte, 500),
	} {
		out := h.l.Handle(junk)
		if out.Data != nil {
			t.Errorf("garbage produced a routable frame: %q", junk)
		}
	}
	if h.l.State() != homebrew.StateConnected {
		t.Errorf("garbage dropped the link: %q", h.l.State())
	}
}

func FuzzHandle(f *testing.F) {
	f.Add(hbp.Ack{}.Marshal())
	f.Add(hbp.Nak{RepeaterID: linkID}.Marshal())
	f.Add(hbp.Pong{RepeaterID: linkID}.Marshal())
	f.Add([]byte("RPTL"))

	f.Fuzz(func(t *testing.T, datagram []byte) {
		c := &clock{t: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)}
		l, err := homebrew.New(homebrew.Config{
			Name: "fuzz", RepeaterID: linkID, Password: []byte(linkPass),
			Identity: homebrew.Identity{Callsign: "K9MLS"}, Now: c.now,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		l.Start()
		// Must not panic, whatever arrives, in whatever state.
		l.Handle(datagram)
		c.advance(time.Minute)
		l.Tick()
		l.Handle(datagram)
	})
}

// TestTheSlotSurvivesTheWire is the difference between the two link kinds,
// pinned where it can be seen (ADR-0051).
//
// `openbridge.Encode` sets `data.Timeslot = hbp.Timeslot1` on every frame,
// deliberately and correctly: OpenBridge passes all traffic on TS1 because
// BrandMeister needs a way to take many networks onto one slot. Two instances
// of QSP have no such need, and using that pipe between them threw the slot
// away — a Motorola repeater keyed on the slot nobody was listening to while
// the codeplug had TG 2 on TS 2, and no audio crossed in either direction for
// a day.
//
// The homebrew peer path replaces the repeater ID and nothing else. This test
// is what stops somebody adding a coercion here later for symmetry.
func TestTheSlotSurvivesTheWire(t *testing.T) {
	for _, slot := range []hbp.Timeslot{hbp.Timeslot1, hbp.Timeslot2} {
		h := newHarness(t)
		h.connect()

		payload := h.l.Send(hbp.Data{
			RepeaterID: 3132910, SourceID: 3132910, TargetID: 2,
			Timeslot: slot, CallType: hbp.CallGroup,
			FrameType: hbp.FrameTypeVoiceSync, StreamID: 0x1234,
		})
		if payload == nil {
			t.Fatalf("TS%d: a connected link refused to send", slot)
		}
		msg, ok := h.parse(payload).(hbp.Data)
		if !ok {
			t.Fatalf("TS%d: sent %s, want DMRD", slot, h.parse(payload).Kind())
		}
		if msg.Timeslot != slot {
			t.Errorf("a frame on TS%d went onto the wire as TS%d", slot, msg.Timeslot)
		}
		if msg.TargetID != 2 {
			t.Errorf("TS%d: talkgroup 2 went onto the wire as %d", slot, msg.TargetID)
		}
		// The one field the link does change, and why: the far end registered
		// this ID and has never heard of the peer the frame came from.
		if msg.RepeaterID != linkID {
			t.Errorf("TS%d: the link announced %d, want its own %d", slot, msg.RepeaterID, linkID)
		}
	}
}
