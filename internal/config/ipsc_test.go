package config

import (
	"errors"
	"strings"
	"testing"
)

// TestAnEnabledIPSCListenerRequiresAColourCode is the defect this file exists
// for.
//
// The field was a `uint8` validated only for range. Zero is a legal DMR colour
// code, so a configuration that never mentioned it validated, started, and
// built every burst with colour code 0. A receiver rejects a burst whose colour
// code is not its own and says nothing about it, so the whole failure presented
// as no audio — with a journal reporting transmissions relayed successfully.
//
// Absent and zero must therefore be different things.
func TestAnEnabledIPSCListenerRequiresAColourCode(t *testing.T) {
	c := Default()
	c.IPSC.Enabled = true
	c.IPSC.ListenAddress = "0.0.0.0:50000"
	c.IPSC.MasterID = 3132911
	c.IPSC.ColourCode = nil

	err := c.Validate()
	if err == nil {
		t.Fatal("a configuration with no colour code was accepted; a burst built with the " +
			"wrong colour code is silence, not an error")
	}
	if !strings.Contains(err.Error(), "ipsc.colour_code") {
		t.Errorf("the failure does not name the field an operator has to fix: %v", err)
	}
}

// TestColourCodeZeroIsDeliberate is the other half, and the reason a pointer
// was needed rather than a sentinel.
//
// Zero is a legal colour code. An operator who means it must be able to say so,
// which is exactly what a non-nil zero expresses and what a plain uint8 could
// not.
func TestColourCodeZeroIsDeliberate(t *testing.T) {
	c := Default()
	c.IPSC.Enabled = true
	c.IPSC.ListenAddress = "0.0.0.0:50000"
	c.IPSC.MasterID = 3132911
	zero := uint8(0)
	c.IPSC.ColourCode = &zero

	if err := c.Validate(); err != nil {
		t.Fatalf("colour code 0 was refused, but it is a legal DMR colour code: %v", err)
	}
}

// TestColourCodeOutOfRangeIsStillRefused keeps the original check.
func TestColourCodeOutOfRangeIsStillRefused(t *testing.T) {
	c := Default()
	c.IPSC.Enabled = true
	c.IPSC.ListenAddress = "0.0.0.0:50000"
	c.IPSC.MasterID = 3132911
	bad := uint8(16)
	c.IPSC.ColourCode = &bad

	err := c.Validate()
	if err == nil {
		t.Fatal("colour code 16 was accepted; DMR allows 0 to 15")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
}

// TestADisabledIPSCListenerNeedsNothing keeps the requirement where it belongs.
//
// A club running no Motorola repeater should not have to name a colour code for
// a listener that never starts.
func TestADisabledIPSCListenerNeedsNothing(t *testing.T) {
	c := Default()
	c.IPSC.Enabled = false
	c.IPSC.ColourCode = nil

	if err := c.Validate(); err != nil {
		t.Fatalf("a disabled IPSC listener demanded a colour code: %v", err)
	}
}
