package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
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

// linkedConfig is a configuration with one link, turned off so that it needs
// no password file to be valid.
func linkedConfig() config.Config {
	cfg := config.Default()
	cfg.DMR.Upstreams = []config.Upstream{{
		Name: "cameron", Protocol: "openbridge",
		Address: "kb9tyc.example.com:62045", ListenAddress: "0.0.0.0:62045",
		NetworkID: 3127045,
	}}
	return cfg
}

// An edited address is reported as needing a restart, and one that could not
// be saved is not left in the running configuration.
//
// **The handler edited the configuration it was comparing against.** `cfg :=
// before` shared the upstreams, so NeedsRestart saw the new address on both
// sides and said nothing — for a link, which is built once at startup — and a
// refused save left the address in what the server went on running.
//
// To see it fail: in handleLinkAddress, put `cfg := before.Clone()` back to
// `cfg := before`.
func TestAnEditedLinkAddressIsNotWrittenIntoTheRunningConfiguration(t *testing.T) {
	const was, now = "kb9tyc.example.com:62045", "kb9tyc.example.com:62031"

	for _, tc := range []struct {
		name    string
		saveErr error
		status  int
		// after is the address the running configuration must hold.
		after string
	}{
		{"saved", nil, http.StatusOK, now},
		{"refused", errors.New("the file is read-only"), http.StatusBadRequest, was},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cm := newStubConfig()
			cm.current = linkedConfig()
			cm.saveErr = tc.saveErr
			srv, a := newConfigServer(t, cm, nil)

			rec := authed(t, srv, a, http.MethodPut, "/api/links/cameron/address",
				`{"address":"`+now+`"}`)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			if got := cm.current.DMR.Upstreams[0].Address; got != tc.after {
				t.Errorf("the running configuration holds %q, want %q", got, tc.after)
			}
			if tc.saveErr != nil {
				return
			}
			var body addressResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if len(body.NeedsRestart) == 0 {
				t.Error("a changed link address was reported as needing no restart")
			}
		})
	}
}
