package peers_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// Layer 3, per-peer talkgroup attachment. See ADR-0023.

func withSubscription(cfg peers.SubscriptionConfig) func(*peers.MasterConfig) {
	return func(c *peers.MasterConfig) { c.Subscription = cfg }
}

// TestSubscriptionOffDeliversEverything is the compatibility guarantee. A club
// that configures nothing must not notice this feature exists.
func TestSubscriptionOffDeliversEverything(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	for _, tg := range []uint32{9, 91, 3148} {
		if !h.m.Attached(testID, tg, hbp.Timeslot2) {
			t.Errorf("with subscription off, TG %d was not attached", tg)
		}
	}
	if h.m.AttachmentCount() != 0 {
		t.Errorf("subscription is off but %d attachments were recorded", h.m.AttachmentCount())
	}
}

// TestTransmittingAttachesTheTalkgroup is the mechanism that makes this usable
// without an administrator.
func TestTransmittingAttachesTheTalkgroup(t *testing.T) {
	h := newHarness(t, withSubscription(peers.SubscriptionConfig{Enabled: true}))
	h.login(addrA)

	if h.m.Attached(testID, 3148, hbp.Timeslot2) {
		t.Fatal("a talkgroup nobody has used was attached")
	}

	h.send(voiceOn(3121001, 3148, hbp.Timeslot2, 0x1111), addrA)

	if !h.m.Attached(testID, 3148, hbp.Timeslot2) {
		t.Error("transmitting on a talkgroup did not attach it")
	}
	// The other slot is a different path and is not attached by this.
	if h.m.Attached(testID, 3148, hbp.Timeslot1) {
		t.Error("attaching on TS2 also attached TS1")
	}
}

// TestTheAttachingFrameIsStillDelivered is the mistake ADR-0016 records making
// with PTT triggers: attach after routing and the first syllable is clipped.
func TestTheAttachingFrameIsStillDelivered(t *testing.T) {
	h := newHarness(t, withSubscription(peers.SubscriptionConfig{Enabled: true}))
	h.login(addrA)

	out := h.send(voiceOn(3121001, 3148, hbp.Timeslot2, 0x2222), addrA)
	if out.Data == nil {
		t.Fatalf("the frame that created the attachment was not accepted: %s", out.Dropped)
	}
	if !h.m.Attached(testID, 3148, hbp.Timeslot2) {
		t.Error("the attachment was not in place by the time the frame was returned for routing")
	}
}

func TestADynamicAttachmentLapses(t *testing.T) {
	h := newHarness(t, withSubscription(peers.SubscriptionConfig{
		Enabled: true, Timeout: 10 * time.Minute,
	}))
	h.login(addrA)
	h.send(voiceOn(3121001, 3148, hbp.Timeslot2, 0x3333), addrA)

	h.c.advance(9 * time.Minute)
	if !h.m.Attached(testID, 3148, hbp.Timeslot2) {
		t.Error("an attachment lapsed before its timeout")
	}
	h.c.advance(2 * time.Minute)
	if h.m.Attached(testID, 3148, hbp.Timeslot2) {
		t.Error("an attachment outlived its timeout")
	}
}

// TestUsingATalkgroupRefreshesIt keeps a conversation alive through pauses.
func TestUsingATalkgroupRefreshesIt(t *testing.T) {
	h := newHarness(t, withSubscription(peers.SubscriptionConfig{
		Enabled: true, Timeout: 10 * time.Minute,
	}))
	h.login(addrA)

	for i := 0; i < 5; i++ {
		h.send(voiceOn(3121001, 3148, hbp.Timeslot2, hbp.StreamID(i)), addrA)
		h.c.advance(9 * time.Minute)
		h.send(hbp.Ping{RepeaterID: testID}, addrA)
	}
	if !h.m.Attached(testID, 3148, hbp.Timeslot2) {
		t.Error("a talkgroup in regular use lapsed")
	}
}

func TestExpireDropsLapsedAttachments(t *testing.T) {
	h := newHarness(t, withSubscription(peers.SubscriptionConfig{
		Enabled: true, Timeout: 5 * time.Minute,
	}))
	h.login(addrA)
	h.send(voiceOn(3121001, 3148, hbp.Timeslot2, 0x4444), addrA)

	if h.m.AttachmentCount() != 1 {
		t.Fatalf("got %d attachments, want 1", h.m.AttachmentCount())
	}
	h.c.advance(6 * time.Minute)
	h.m.Expire()
	if h.m.AttachmentCount() != 0 {
		t.Errorf("Expire left %d lapsed attachments behind", h.m.AttachmentCount())
	}
}

// TestAStaticAttachmentNeverLapses covers what dynamics cannot serve: a
// calling channel has to be there before anybody speaks.
func TestAStaticAttachmentNeverLapses(t *testing.T) {
	h := newHarness(t, withSubscription(peers.SubscriptionConfig{
		Enabled: true,
		Timeout: time.Minute,
		Static: []peers.Attachment{
			{Peer: testID, Talkgroup: 9, Timeslot: hbp.Timeslot2},
		},
	}))
	h.login(addrA)

	if !h.m.Attached(testID, 9, hbp.Timeslot2) {
		t.Fatal("a static attachment was not in place before anybody transmitted")
	}
	h.c.advance(2 * time.Hour)
	h.m.Expire()
	if !h.m.Attached(testID, 9, hbp.Timeslot2) {
		t.Error("a static attachment lapsed")
	}
	if h.m.AttachmentCount() != 1 {
		t.Errorf("Expire removed a static attachment; %d remain", h.m.AttachmentCount())
	}
}

// TestTransmittingOnAStaticAttachmentDoesNotDemoteIt guards a subtle one: the
// refresh path must not turn a configured attachment into one that can lapse.
func TestTransmittingOnAStaticAttachmentDoesNotDemoteIt(t *testing.T) {
	h := newHarness(t, withSubscription(peers.SubscriptionConfig{
		Enabled: true,
		Timeout: time.Minute,
		Static: []peers.Attachment{
			{Peer: testID, Talkgroup: 9, Timeslot: hbp.Timeslot2},
		},
	}))
	h.login(addrA)
	h.send(voiceOn(3121001, 9, hbp.Timeslot2, 0x5555), addrA)

	h.c.advance(2 * time.Hour)
	h.m.Expire()
	if !h.m.Attached(testID, 9, hbp.Timeslot2) {
		t.Error("transmitting on a static attachment demoted it to a dynamic one")
	}
	for _, a := range h.m.Attachments() {
		if a.Talkgroup == 9 && !a.Static {
			t.Error("the attachment is no longer marked static")
		}
	}
}

// TestPrivateCallsDoNotAttach. A private call is addressed to a radio and says
// nothing about which talkgroups its peer wants to hear.
func TestPrivateCallsDoNotAttach(t *testing.T) {
	h := newHarness(t, withSubscription(peers.SubscriptionConfig{Enabled: true}))
	h.login(addrA)

	frame := voiceOn(3121001, 3121002, hbp.Timeslot2, 0x6666)
	frame.CallType = hbp.CallPrivate
	h.send(frame, addrA)

	if h.m.AttachmentCount() != 0 {
		t.Errorf("a private call created %d attachments", h.m.AttachmentCount())
	}
}

func TestAttachmentsAreOrderedAndCopied(t *testing.T) {
	h := newHarness(t, withSubscription(peers.SubscriptionConfig{Enabled: true}))
	h.login(addrA)
	for _, tg := range []uint32{3148, 9, 91} {
		h.send(voiceOn(3121001, tg, hbp.Timeslot2, hbp.StreamID(tg)), addrA)
	}

	got := h.m.Attachments()
	if len(got) != 3 {
		t.Fatalf("got %d attachments, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Talkgroup >= got[i].Talkgroup {
			t.Errorf("attachments are not ordered: %v", got)
		}
	}
	got[0].Talkgroup = 999999
	if h.m.Attachments()[0].Talkgroup == 999999 {
		t.Error("Attachments returned a view into master state")
	}
}

// TestDropAttachmentsLeavesStaticAlone.
//
// **A member pressing disconnect says what they want to stop hearing.** A
// static attachment is an administrator's statement about what a peer must
// always carry, and a PTT does not overrule it — otherwise somebody drops
// themselves off the club calling channel and cannot work out why they have
// gone deaf.
//
// Waiting out the timeout is not a control, it is a delay. Landing on a
// talkgroup, finding it empty and moving on is what exploring a network looks
// like, and it wants to happen now.
func TestDropAttachmentsLeavesStaticAlone(t *testing.T) {
	h := newHarness(t, withSubscription(peers.SubscriptionConfig{
		Enabled: true,
		Static: []peers.Attachment{
			{Peer: testID, Talkgroup: 2, Timeslot: hbp.Timeslot2, Static: true},
		},
	}))
	h.login(addrA)

	h.send(voiceOn(3121001, 3148, hbp.Timeslot2, 0x4444), addrA)
	h.send(voiceOn(3121001, 91, hbp.Timeslot2, 0x4445), addrA)
	if !h.m.Attached(testID, 3148, hbp.Timeslot2) {
		t.Fatal("transmitting did not attach a talkgroup")
	}

	if n := h.m.DropAttachments(testID); n != 2 {
		t.Errorf("dropped %d attachments, want the two dynamic ones", n)
	}
	if !h.m.Attached(testID, 2, hbp.Timeslot2) {
		t.Error("a static attachment was dropped, so a member has gone deaf to the " +
			"talkgroup an administrator pinned")
	}
	if h.m.Attached(testID, 3148, hbp.Timeslot2) {
		t.Error("a dynamic attachment survived a disconnect")
	}
}
