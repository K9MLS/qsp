package ipsclink_test

import (
	"net"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ipsclink"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// TestARepeaterAddedFromTheConsoleIsAnsweredWithoutARestart is the point of the
// allow list being replaceable.
//
// `ipsc.allowed_peers` used to be read once when the listener was built. The
// console saves the whole configuration, so an operator could add a repeater,
// see the save succeed, get no restart warning, and watch the repeater go on
// being ignored.
func TestARepeaterAddedFromTheConsoleIsAnsweredWithoutARestart(t *testing.T) {
	const known, newcomer = 315544, 999999

	l, conn := start(t, ipsclink.Config{AllowedPeers: []uint32{known}})

	// Not on the list: ignored, and counted so the operator can see it.
	send(t, conn, ipsc.KindRegisterRequest, newcomer, registerBody())
	expectSilence(t, conn)
	if ignored, _ := l.Counters(); ignored == 0 {
		t.Error("a refused datagram was not counted; an operator cannot see it")
	}

	l.SetAllowedPeers([]uint32{known, newcomer})

	send(t, conn, ipsc.KindRegisterRequest, newcomer, registerBody())
	if reply := expectReply(t, conn, ipsc.KindRegisterReply); reply.Kind != ipsc.KindRegisterReply {
		t.Fatalf("the newcomer got %#02x, want a registration reply", byte(reply.Kind))
	}
}

// TestARepeaterRemovedFromTheConsoleStopsBeingAnswered is the other direction,
// and it is the one that matters for access control: revoking has to take
// effect, or the list is a suggestion.
func TestARepeaterRemovedFromTheConsoleStopsBeingAnswered(t *testing.T) {
	const banished = 315544

	l, conn := start(t, ipsclink.Config{AllowedPeers: []uint32{banished}})

	send(t, conn, ipsc.KindRegisterRequest, banished, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)

	l.SetAllowedPeers([]uint32{999999})

	send(t, conn, ipsc.KindRegisterRequest, banished, registerBody())
	expectSilence(t, conn)
}

// TestAnEmptyAllowListStillAnswersEverybody guards a setting that reads like an
// absence.
//
// An empty list admits every repeater. That is the documented behaviour and the
// default a new instance runs with, so clearing the list from the console must
// be savable and must take effect — refusing to apply an empty one would make
// "let every repeater in" impossible to express.
func TestAnEmptyAllowListStillAnswersEverybody(t *testing.T) {
	l, conn := start(t, ipsclink.Config{AllowedPeers: []uint32{315544}})

	l.SetAllowedPeers(nil)

	send(t, conn, ipsc.KindRegisterRequest, 999999, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)
}

// expectSilence asserts that nothing comes back within a short window, which is
// how IPSC says no.
func expectSilence(t *testing.T, conn *net.UDPConn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := conn.Read(make([]byte, 2048)); err == nil {
		t.Fatalf("expected silence, got a %d byte reply", n)
	}
}
