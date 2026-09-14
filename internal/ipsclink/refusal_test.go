package ipsclink

import (
	"testing"
	"time"
)

// TestARefusedPeerIsLoggedOncePerWindow is the flood an operator read past on
// 2026-09-14.
//
// A repeater not on the allow list keeps polling, and every poll was a
// warning. Production carried thousands of identical
// `ignoring peer not on the allow list` lines from one sender, every ten
// seconds, for hours across 2–3 September. The operator went looking for five
// refused datagrams on a different listener and had to read past all of them
// to find the two lines that mattered.
//
// `internal/peers` learned this for routing drops and P25 counts refusals
// rather than logging each one. This listener had neither.
func TestARefusedPeerIsLoggedOncePerWindow(t *testing.T) {
	l := &Listener{}
	at := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	if !l.noteRefusal(3155412, 0x90, at) {
		t.Fatal("the first refusal was not logged; a refusal an operator has "+
			"never seen must always be reported", 0)
	}

	// A repeater polls every ten seconds and QSP sees several message types
	// between polls. None of these is news.
	for i := 1; i < 60; i++ {
		if l.noteRefusal(3155412, 0x90, at.Add(time.Duration(i)*100*time.Millisecond)) {
			t.Fatalf("refusal %d in six seconds was logged again; one line "+
				"stands for the window", i)
		}
	}
}

// TestADifferentMessageTypeIsItsOwnFact keeps the window from merging two
// things an operator would want told apart.
//
// The 2–3 September flood alternated 0x90 and 0xf0 from one sender. Those are
// different statements about what the repeater is trying to do, so each says
// so once rather than the first swallowing the other.
func TestADifferentMessageTypeIsItsOwnFact(t *testing.T) {
	l := &Listener{}
	at := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	if !l.noteRefusal(3155412, 0x90, at) {
		t.Fatal("the first refusal was not logged")
	}
	if !l.noteRefusal(3155412, 0xf0, at.Add(time.Second)) {
		t.Error("a different message type was swallowed by the window for the " +
			"first; they are different facts")
	}
	if !l.noteRefusal(999998, 0x90, at.Add(time.Second)) {
		t.Error("a different sender was swallowed by the window for the first; " +
			"a second unauthorised repeater must be reported")
	}
}

// TestTheWindowExpires is the other half: silence must not be permanent.
//
// An operator who fixes an allow list and keys up again wants to be told
// promptly that it is still refused. A refusal logged once and never again
// would be worse than the flood, because it would look fixed.
func TestTheWindowExpires(t *testing.T) {
	l := &Listener{}
	at := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	if !l.noteRefusal(3155412, 0x90, at) {
		t.Fatal("the first refusal was not logged")
	}
	if l.noteRefusal(3155412, 0x90, at.Add(refusalWindow-time.Millisecond)) {
		t.Error("a refusal just inside the window was logged")
	}
	if !l.noteRefusal(3155412, 0x90, at.Add(refusalWindow)) {
		t.Error("a refusal after the window was not logged; a peer that is " +
			"still refused must keep saying so")
	}
}

// TestTheRefusalMapIsBounded is the memory a stranger would otherwise control.
//
// The key names a sender and a message type, and both come off the wire. A
// host inventing sender IDs would grow this map without a bound.
func TestTheRefusalMapIsBounded(t *testing.T) {
	l := &Listener{}
	at := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	for i := range uint32(maxRefusals * 4) {
		l.noteRefusal(i, 0x90, at)
	}

	l.refusalsMu.Lock()
	n := len(l.refusals)
	l.refusalsMu.Unlock()

	if n > maxRefusals {
		t.Errorf("the refusal map holds %d entries with a cap of %d; the key "+
			"comes off the wire and a stranger must not be able to grow it",
			n, maxRefusals)
	}
}
