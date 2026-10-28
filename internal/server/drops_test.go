package server

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/peers"
)

// TestDropReasonsAreNotPublished closes a hole the address redaction beside it
// did not cover.
//
// `PeerView.Address` has been blanked for unauthenticated callers since
// ADR-0043, with a comment arguing that an address is a member's home internet
// connection plus the fact they are online right now. **The drop reasons carry
// the same addresses in prose**, three hundred lines further down the same
// function, and were published to anybody who asked.
//
// It went unnoticed until the console began drawing those reasons on the page,
// which is the second time this project has found a leak by rendering it.
func TestDropReasonsAreNotPublished(t *testing.T) {
	const home = "192.168.1.155:42602"
	drops := []peers.DropNote{
		{
			At:       time.Now().UTC(),
			From:     home,
			Reason:   "keepalive from repeater ID 3132910 at " + home + ", which is not registered",
			Answered: true,
		},
	}

	srv := peersServer(t,
		fixedPeers{traffic: Traffic{Dropped: 1, Answered: 1, RecentDrops: drops}},
		fixedPeers{},
	)

	public := peersBody(t, srv)
	if len(public.Traffic.RecentDrops) != 0 {
		t.Errorf("%d drop notes were published to a caller who is not signed in",
			len(public.Traffic.RecentDrops))
	}
	// The counters themselves stay. They name nobody, and withholding them
	// would take away the only thing a public view can honestly report about
	// traffic being turned away.
	if public.Traffic.Dropped != 1 {
		t.Errorf("the dropped counter was withheld too; it names nobody")
	}

	// The complementary half: an operator diagnosing a peer that will not
	// connect is signed in, and taking the reason away from them would leave
	// the counter unexplainable again.
	private := peersBodyAs(t, srv, true)
	if len(private.Traffic.RecentDrops) != 1 {
		t.Fatalf("a signed-in operator got %d drop notes, want 1",
			len(private.Traffic.RecentDrops))
	}
	if !strings.Contains(private.Traffic.RecentDrops[0].Reason, home) {
		t.Error("the reason reached a signed-in operator with its address removed")
	}
}

// TestCollisionNotesAreForASignedInOperator: the sentences that explain
// COLLISIONS are withheld from a public view as the drop reasons are, and
// reach an operator whole.
//
// To see it fail: remove `body.Traffic.RecentCollisions = nil`, and the public
// case fails; remove the field from cmd/qsp's Traffic and nothing is shown
// to anybody (the browser checks catch that half).
func TestCollisionNotesAreForASignedInOperator(t *testing.T) {
	notes := []peers.CollisionNote{{
		At: time.Now().UTC(), Refusals: 50, KeyOver: true,
		Summary: `peer 3121380 keyed up while transcoder "zello" was carrying TG 2`,
	}}
	srv := peersServer(t,
		fixedPeers{traffic: Traffic{Collisions: 50, RecentCollisions: notes}},
		fixedPeers{},
	)
	for _, tc := range []struct {
		name     string
		signedIn bool
		want     int
	}{
		{"public", false, 0},
		{"signed in", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := peersBodyAs(t, srv, tc.signedIn)
			if got := len(body.Traffic.RecentCollisions); got != tc.want {
				t.Errorf("%d collision notes, want %d", got, tc.want)
			}
			if body.Traffic.Collisions != 50 {
				t.Errorf("the counter reads %d, want 50: it names nobody and stays",
					body.Traffic.Collisions)
			}
		})
	}
}
