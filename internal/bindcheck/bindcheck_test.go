package bindcheck

import (
	"errors"
	"net"
	"testing"
)

// TestAnAddressThisHostDoesNotHaveIsRefused is the defect that took production
// down, reduced to a test: a public name resolving somewhere this machine is
// not.
//
// 192.0.2.1 is TEST-NET-1 (RFC 5737), reserved for documentation and never
// assigned to an interface, so it stands in for qsp.hopto.me without needing
// DNS or a router.
func TestAnAddressThisHostDoesNotHaveIsRefused(t *testing.T) {
	err := Address("udp", "192.0.2.1:62045")
	if err == nil {
		t.Fatal("bound an address this host does not have; the accept form's " +
			"listen field would pass a configuration QSP cannot start on")
	}
	if errors.Is(err, ErrInUse) {
		t.Fatalf("reported as in use rather than unassignable: %v", err)
	}
}

// TestEveryInterfaceIsBindable covers the value the page suggests.
func TestEveryInterfaceIsBindable(t *testing.T) {
	if err := Address("udp", "0.0.0.0:0"); err != nil {
		t.Fatalf("0.0.0.0 is where a server listens and was refused: %v", err)
	}
}

// TestAnAddressAlreadyHeldIsNotAFailure is the case that decides whether
// -check can be run at all.
//
// It is run against a live configuration while the service holding those ports
// is running. If in-use came back as a fault, every honest check would report
// three, and an operator would stop reading them.
func TestAnAddressAlreadyHeldIsNotAFailure(t *testing.T) {
	held, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not take an address to hold: %v", err)
	}
	defer held.Close()

	err = Address("udp", held.LocalAddr().String())
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("an address held by another socket reported %v, want ErrInUse", err)
	}
}

// TestTCPIsCheckedToo exists because the console binds TCP and the listeners
// bind UDP, and -check reports on both.
func TestTCPIsCheckedToo(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not take an address to hold: %v", err)
	}
	defer held.Close()

	if err := Address("tcp", held.Addr().String()); !errors.Is(err, ErrInUse) {
		t.Fatalf("a held TCP address reported %v, want ErrInUse", err)
	}
	if err := Address("tcp", "192.0.2.1:8080"); err == nil {
		t.Fatal("bound a TCP address this host does not have")
	}
}

// TestAnEmptyAddressIsNotSilentlyEveryInterface.
//
// net.ListenPacket("udp", "") binds every interface on a random port and
// succeeds, so a missing listen address would come back as a clean pass. That
// is the shape of defect this whole package was written after.
func TestAnEmptyAddressIsNotSilentlyEveryInterface(t *testing.T) {
	if err := Address("udp", "   "); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("an empty listen address reported %v, want ErrNoAddress", err)
	}
}
