package server

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// **A configuration cannot have the lookup on with no contact address**, so the
// page has two states rather than three. An earlier version of this reported a
// third and asserted it against a struct built by hand — a state no server can
// hold, checked by a test that could not fail.
//
// Proved here against the validator rather than against a struct, because that
// is what makes the claim true.
func TestALookupWithNoContactIsPreventedRatherThanReported(t *testing.T) {
	cfg := config.Default()
	cfg.DMR.Callsigns = config.Callsigns{Enabled: true, Contact: ""}

	if err := cfg.Validate(); err == nil {
		t.Fatal("a configuration with the lookup on and no contact was accepted; the " +
			"administration page would then need a state for it")
	} else if !strings.Contains(err.Error(), "contact") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
}

func TestTheCallsignBlockSaysWhetherRadiosAreBeingNamed(t *testing.T) {
	// Off is not the same as broken, and the page must not conflate them.
	off := callsignState(config.Callsigns{Enabled: false})
	if off.Usable {
		t.Error("a lookup that is off was reported as working")
	}
	if !strings.Contains(off.Why, "off") {
		t.Errorf("an off lookup reads as a fault: %q", off.Why)
	}

	on := callsignState(config.Callsigns{Enabled: true, Contact: "k9mls@example.com"})
	if !on.Usable {
		t.Errorf("a configured lookup was reported as unusable: %q", on.Why)
	}
	if on.Why != "" {
		t.Errorf("a working lookup explains itself anyway: %q", on.Why)
	}
}

// **The block that earns the page.** A server several saved changes behind what
// it is running has to say so in one place — before this it was a sentence
// beside whichever link happened to be saved last, and nothing added them up.
func TestAgreementReportsEverySettingWaitingOnARestart(t *testing.T) {
	store := newStubConfig()
	store.pending = []string{"dmr.listen_address", "dmr.upstreams"}

	got := agreement(store)
	if got.Agrees {
		t.Fatal("a server two changes behind its configuration reported agreement")
	}
	if len(got.Pending) != 2 {
		t.Errorf("the page names %d pending settings, want 2", len(got.Pending))
	}
	if !got.Writable {
		t.Error("a writable instance was reported as read only")
	}

	store.pending = nil
	if quiet := agreement(store); !quiet.Agrees {
		t.Error("a server matching its configuration did not report agreement")
	}
}

// An instance started without -config runs on defaults and cannot be
// reconfigured. **That is a working state**, and the page says which it is
// rather than showing a form that cannot save.
func TestAReadOnlyInstanceSaysSoRatherThanOfferingAForm(t *testing.T) {
	store := newStubConfig()
	store.readOnly = config.ErrNotWritable

	got := agreement(store)
	if got.Writable {
		t.Fatal("a read-only instance was reported as writable")
	}
	if got.NotWritable == "" {
		t.Error("the page does not say why it cannot be configured")
	}
}
