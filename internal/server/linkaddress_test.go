package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/peering"
)

// **Three addresses that produce a link reporting itself healthy while carrying
// nothing**, all of which have been written to a live configuration here.
func TestALinkAddressRefusesTheThreeThatLookFine(t *testing.T) {
	for _, tc := range []struct {
		name    string
		address string
		want    error
	}{
		{"a scheme, because every other address has one", "https://qsp.example.com:62031", peering.ErrSchemeInAddress},
		{"the address this server binds", "0.0.0.0:62031", peering.ErrBindAddress},
		{"every interface, spelled the other way", "[::]:62031", peering.ErrBindAddress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := usableFarEnd(tc.address)
			if !errors.Is(err, tc.want) {
				t.Errorf("error is %v, want %v", err, tc.want)
			}
		})
	}

	for _, bad := range []string{"", "qsp.example.com", "62031"} {
		if err := usableFarEnd(bad); err == nil {
			t.Errorf("%q was accepted as a far-end address", bad)
		}
	}

	for _, good := range []string{"qsp.example.com:62031", "192.168.1.247:62031", "[2001:db8::1]:62031"} {
		if err := usableFarEnd(good); err != nil {
			t.Errorf("%q was refused: %v", good, err)
		}
	}
}

// The message has to name the field an operator can act on. "Not host:port" on
// its own sends somebody looking at the wrong box.
func TestARefusedAddressSaysWhichWayItIsWrong(t *testing.T) {
	err := usableFarEnd("192.168.1.247")
	if err == nil {
		t.Fatal("an address with no port was accepted")
	}
	if !strings.Contains(err.Error(), "host:port") {
		t.Errorf("the refusal does not say what an address is: %v", err)
	}
}
