package peers_test

import (
	"testing"

	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/routing"
)

// Master must satisfy routing.SubscriberLookup, or cmd/qsp will not compile —
// and cmd/qsp cannot be built in the development container, so the check has to
// live somewhere that can.
func TestMasterSatisfiesSubscriberLookup(t *testing.T) {
	var _ routing.SubscriberLookup = (*peers.Master)(nil)
}

// Master must also satisfy routing.Subscriptions, for the same reason.
func TestMasterSatisfiesSubscriptions(t *testing.T) {
	var _ routing.Subscriptions = (*peers.Master)(nil)
}
