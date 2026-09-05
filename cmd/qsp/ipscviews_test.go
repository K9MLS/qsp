package main

import (
	"os"
	"strings"
	"testing"
)

// TestTheIPSCAdapterMarksALookedUpCallsign covers the one line the server's own
// tests cannot reach.
//
// internal/server tests the payload with fixtures, so it proves the field
// survives the handler and says nothing about whether the adapter ever sets it.
// Removing the assignment broke nothing there.
//
// **Reading the source is blunt and it is the right instrument here.** The
// alternative is constructing a listener, registering a peer over a socket and
// standing up a callsign registry to observe one boolean — a test that would
// pass if the adapter were deleted and replaced by a stub.
func TestTheIPSCAdapterMarksALookedUpCallsign(t *testing.T) {
	b, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("reading app.go: %v", err)
	}
	src := string(b)

	i := strings.Index(src, "func (p ipscPeerViews) PeerViews(")
	if i < 0 {
		t.Fatal("the IPSC peer adapter is gone; this test needs rewriting")
	}
	j := strings.Index(src[i:], "\nfunc ")
	if j < 0 {
		j = len(src) - i
	}
	body := src[i : i+j]

	if !strings.Contains(body, "server.CallsignFromRegistry") {
		t.Error("the IPSC adapter sets a callsign without marking it as looked up; " +
			"a registry guess would be shown exactly like a callsign the peer announced")
	}
	// **An operator's own label is a third claim and must outrank the
	// registry.** A repeater on a private radio ID is not in the registry and
	// never will be, so a lookup that wins would leave the peers carrying this
	// network showing bare numbers however carefully they were named.
	if !strings.Contains(body, "server.CallsignFromOperator") {
		t.Error("the IPSC adapter ignores the callsign an administrator gave a repeater")
	}
	if strings.Index(body, "CallsignFromOperator") > strings.Index(body, "CallsignFromRegistry") {
		t.Error("the registry lookup is tried before the operator's own label")
	}
	if !strings.Contains(body, "resolve(peer.RadioID") {
		t.Error("the IPSC adapter no longer resolves a callsign; repeaters would " +
			"show as bare radio IDs")
	}
}
