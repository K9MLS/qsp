package peers_test

import (
	"context"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// denySubscriber builds a list that permits everything except one radio.
func denySubscriber(t *testing.T, banned uint32) access.Lists {
	t.Helper()
	l, err := access.Parse("dmr.access.subscribers", access.Subscriber, access.ModeDeny,
		[]string{"3155999"})
	if err != nil {
		t.Fatalf("access.Parse: %v", err)
	}
	_ = banned
	return access.Lists{Subscriber: l}
}

func masterWithAccess(t *testing.T, lists access.Lists) *peers.Master {
	t.Helper()
	m, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password:    func(hbp.RepeaterID) ([]byte, bool) { return []byte("x"), true },
		PeerTimeout: time.Minute,
		Access:      lists,
	})
	if err != nil {
		t.Fatalf("peers.NewMaster: %v", err)
	}
	return m
}

// TestABannedRadioIsBannedOnBothProtocols is the hole this closes.
//
// A subscriber list bans a **radio**, not a repeater. Before the IPSC path
// asked, the same operator was refused on a hotspot and carried by a Motorola
// repeater — so which door they walked through decided the answer, and a ban
// could be walked around by keying a different radio.
func TestABannedRadioIsBannedOnBothProtocols(t *testing.T) {
	const banned, ordinary = 3155999, 3132910
	m := masterWithAccess(t, denySubscriber(t, banned))

	if m.SubscriberAllowed(banned) {
		t.Error("the banned radio is permitted; the list is not being read")
	}
	if !m.SubscriberAllowed(ordinary) {
		t.Error("an ordinary radio was refused; the list is denying too much")
	}
}

// TestTheMastersAccessListsReload covers two of four lists that were saved and
// did nothing.
//
// The console edits all four and posts the whole configuration back. Talkgroup
// lists reached the routing core on reload; **registration and subscriber lists
// were read when the master was constructed and never again**, and NeedsRestart
// named neither. An operator banning a radio got a successful save, no restart
// warning, and a ban that was not in force.
func TestTheMastersAccessListsReload(t *testing.T) {
	const radio = 3155999

	// Start permissive: an empty list permits everything.
	m := masterWithAccess(t, access.Lists{})
	if !m.SubscriberAllowed(radio) {
		t.Fatal("an empty subscriber list refused a radio; it should permit everything")
	}

	m.SetAccess(denySubscriber(t, radio))
	if m.SubscriberAllowed(radio) {
		t.Error("the radio is still permitted after the list changed; " +
			"a ban saved from the console would not be in force until a restart")
	}

	// And back again, because "remove every restriction" has to be savable too.
	m.SetAccess(access.Lists{})
	if !m.SubscriberAllowed(radio) {
		t.Error("clearing the list did not lift the ban")
	}
}

// TestABannedRadioOnARepeaterReachesNobody drives the listener rather than the
// accessor, and it exists because removing the check from DeliverFromIPSC broke
// nothing any other test could see.
//
// **The first version of this test passed with the check removed.** It sent a
// permitted transmission and then a banned one down the same listener, and the
// second was refused for contention — the first stream had not ended — so the
// silence it asserted proved nothing. Each case now gets its own listener, so
// the only difference between them is the ban.
func TestABannedRadioOnARepeaterReachesNobody(t *testing.T) {
	const bannedRadio = 3155999

	// The control: the same frame, no ban, must arrive. Without this the test
	// below passes on any listener that delivers nothing for any reason.
	t.Run("permitted", func(t *testing.T) {
		l, _ := startWithIPSCAccess(t)
		hotspot := register(t, l.Address(), testID, "K9MLS")
		l.DeliverFromIPSC(motorola, ipscFrameFrom(bannedRadio))
		if _, ok := hotspot.recv().(hbp.Data); !ok {
			t.Fatal("the frame did not reach the hotspot with no ban in force")
		}
	})

	t.Run("banned", func(t *testing.T) {
		l, master := startWithIPSCAccess(t)
		master.SetAccess(denySubscriber(t, bannedRadio))
		hotspot := register(t, l.Address(), testID, "K9MLS")
		l.DeliverFromIPSC(motorola, ipscFrameFrom(bannedRadio))
		hotspot.silence(300 * time.Millisecond)
	})
}

// ipscFrameFrom is one burst as it arrives from a Motorola repeater.
func ipscFrameFrom(source uint32) hbp.Data {
	return hbp.Data{
		RepeaterID: motorola, SourceID: source, TargetID: 2,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0xC0FFEE01,
	}
}

// startWithIPSCAccess is startWithIPSC with the master handed back, so a test
// can change the access lists on a running listener.
func startWithIPSCAccess(t *testing.T) (*peers.Listener, *peers.Master) {
	t.Helper()

	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}
	table, err := routing.NewTable([]routing.Bridge{{
		Name: "motorola-to-hotspots", Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: motorola, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			{Peer: testID, Talkgroup: 2, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	core, err := routing.NewCore(routing.CoreOptions{
		Table: table, Peers: readyFromMaster{m: master},
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Routing: core,
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, master
}
