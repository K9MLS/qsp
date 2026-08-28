package config

import (
	"bytes"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/access"
)

// enabledDMR is a configuration with the listener on and nothing else set, so
// that each test states only what it is about.
func enabledDMR() Config {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.PasswordFile = "peer.pass"
	return c
}

// TestDefaultHasNoAccessBlock records that the absence is the default. The
// listener is off by default, so nothing is exposed; the absence only becomes a
// problem when an operator turns it on somewhere reachable.
func TestDefaultHasNoAccessBlock(t *testing.T) {
	if Default().DMR.Access != nil {
		t.Error("the default configuration should not write an access block")
	}
	if err := Default().Validate(); err != nil {
		t.Errorf("the default configuration must validate: %v", err)
	}
}

// TestReachableListenerWithoutAccessIsRefused is ADR-0020's startup check. The
// case it guards is the moment UDP 62031 is forwarded at the router.
func TestReachableListenerWithoutAccessIsRefused(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:62031", "192.168.1.247:62031", ":62031", "[::]:62031"} {
		t.Run(listen, func(t *testing.T) {
			c := enabledDMR()
			c.DMR.ListenAddress = listen

			err := c.Validate()
			if err == nil {
				t.Fatalf("a listener on %s with no access block was accepted", listen)
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("want *ValidationError, got %T", err)
			}
			var found bool
			for _, fe := range ve.Errors {
				if fe.Field == "dmr.access" {
					found = true
					// The fix must show how to permit everything on purpose,
					// or the only apparent way past the error is to give up on
					// binding a reachable address.
					if !strings.Contains(fe.Fix, `"mode": "deny"`) {
						t.Errorf("the fix does not show the deliberate permit-everything form: %q", fe.Fix)
					}
				}
			}
			if !found {
				t.Errorf("no dmr.access error; got fields %v", ve.Fields())
			}
		})
	}
}

// TestLoopbackListenerWithoutAccessIsAccepted keeps the check proportionate. A
// master bound to loopback is reachable only by something already on the host,
// which is the developing and tunnelled case.
func TestLoopbackListenerWithoutAccessIsAccepted(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:62031", "[::1]:62031"} {
		c := enabledDMR()
		c.DMR.ListenAddress = listen
		if err := c.Validate(); err != nil {
			t.Errorf("a loopback listener on %s was rejected: %v", listen, err)
		}
	}
}

// TestDisabledListenerWithoutAccessIsAccepted matters because the listener is
// off by default. A configuration being prepared must not be unable to validate
// on its way to being finished.
func TestDisabledListenerWithoutAccessIsAccepted(t *testing.T) {
	c := Default()
	c.DMR.ListenAddress = "0.0.0.0:62031"
	if err := c.Validate(); err != nil {
		t.Errorf("a disabled listener with no access block was rejected: %v", err)
	}
}

// TestEmptyAccessBlockIsTheEscapeHatch is the whole point of the pointer. An
// empty block and no block behave identically; only one of them is a statement
// of intent, and the check is about silence rather than behaviour.
func TestEmptyAccessBlockIsTheEscapeHatch(t *testing.T) {
	c := enabledDMR()
	c.DMR.ListenAddress = "0.0.0.0:62031"
	c.DMR.Access = &Access{}

	if err := c.Validate(); err != nil {
		t.Fatalf("an explicit empty access block was rejected: %v", err)
	}
	lists, err := c.AccessLists()
	if err != nil {
		t.Fatalf("AccessLists: %v", err)
	}
	if !lists.Permissive() {
		t.Error("an empty access block should permit everything")
	}
}

func TestDenyNobodyIsTheDocumentedEscapeHatch(t *testing.T) {
	// The form the error message tells the operator to write must actually
	// work, or the guidance is worse than none.
	c := enabledDMR()
	c.DMR.ListenAddress = "0.0.0.0:62031"
	c.DMR.Access = &Access{Registration: ACL{Mode: "deny", IDs: []string{}}}

	if err := c.Validate(); err != nil {
		t.Fatalf("the form the error message suggests was rejected: %v", err)
	}
}

func TestAccessListsFromConfiguration(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{
		Registration: ACL{Mode: "permit", IDs: []string{"312100", "312100101"}},
		Subscribers:  ACL{Mode: "deny", IDs: []string{"3121077"}},
		Talkgroups: Talkgroups{
			Timeslot1: ACL{Mode: "permit", IDs: []string{"3100-3199"}},
			Timeslot2: ACL{Mode: "permit", IDs: []string{"9"}},
		},
	}

	if err := c.Validate(); err != nil {
		t.Fatalf("a well-formed access block was rejected: %v", err)
	}
	lists, err := c.AccessLists()
	if err != nil {
		t.Fatalf("AccessLists: %v", err)
	}

	if !lists.Registration.Allows(312100) || lists.Registration.Allows(312101) {
		t.Error("the registration list did not come through")
	}
	// A nine-digit hotspot ID must survive; the repeater ID travels in 32 bits.
	if !lists.Registration.Allows(312100101) {
		t.Error("a nine-digit hotspot ID was refused by the registration list")
	}
	if lists.Subscriber.Allows(3121077) || !lists.Subscriber.Allows(3121001) {
		t.Error("the subscriber deny list did not come through")
	}
	if !lists.Talkgroups(1).Allows(3150) || lists.Talkgroups(1).Allows(9) {
		t.Error("the timeslot 1 talkgroup list did not come through")
	}
	if !lists.Talkgroups(2).Allows(9) || lists.Talkgroups(2).Allows(3150) {
		t.Error("the timeslot 2 talkgroup list did not come through")
	}
	if lists.Permissive() {
		t.Error("a configured set reported itself permissive")
	}
}

func TestNilAccessYieldsPermissiveLists(t *testing.T) {
	lists, err := Default().AccessLists()
	if err != nil {
		t.Fatalf("AccessLists on a nil block: %v", err)
	}
	if !lists.Permissive() {
		t.Error("a nil access block must yield permissive lists, or an upgrade disconnects a club")
	}
}

func TestAccessValidationNamesTheExactField(t *testing.T) {
	for _, tc := range []struct {
		name  string
		acl   *Access
		field string
	}{
		{"registration", &Access{Registration: ACL{Mode: "permit", IDs: []string{"nope"}}}, "dmr.access.registration"},
		{"subscribers", &Access{Subscribers: ACL{Mode: "permit", IDs: []string{"nope"}}}, "dmr.access.subscribers"},
		{"timeslot 1", &Access{Talkgroups: Talkgroups{Timeslot1: ACL{Mode: "permit", IDs: []string{"nope"}}}}, "dmr.access.talkgroups.timeslot_1"},
		{"timeslot 2", &Access{Talkgroups: Talkgroups{Timeslot2: ACL{Mode: "permit", IDs: []string{"nope"}}}}, "dmr.access.talkgroups.timeslot_2"},
		{"bad mode", &Access{Registration: ACL{Mode: "allow", IDs: []string{"9"}}}, "dmr.access.registration"},
		{"permit nothing", &Access{Registration: ACL{Mode: "permit"}}, "dmr.access.registration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := enabledDMR()
			c.DMR.Access = tc.acl

			err := c.Validate()
			if err == nil {
				t.Fatal("an invalid access list was accepted")
			}
			ve := err.(*ValidationError)
			var found bool
			for _, fe := range ve.Errors {
				if fe.Field == tc.field {
					found = true
					// The problem must not repeat the field name, which the
					// console already renders beside it.
					if strings.HasPrefix(fe.Problem, tc.field) {
						t.Errorf("the problem repeats the field: %q", fe.Problem)
					}
				}
			}
			if !found {
				t.Errorf("want an error on %s, got fields %v", tc.field, ve.Fields())
			}
		})
	}
}

// TestTimeslotErrorsAreDistinguishable is why the field path is threaded
// through the parser. An operator with a mistake in one slot's list needs to be
// told which slot.
func TestTimeslotErrorsAreDistinguishable(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{Talkgroups: Talkgroups{
		Timeslot1: ACL{Mode: "permit", IDs: []string{"nope"}},
		Timeslot2: ACL{Mode: "permit", IDs: []string{"3199-3100"}},
	}}

	err := c.Validate()
	if err == nil {
		t.Fatal("two invalid lists were accepted")
	}
	fields := err.(*ValidationError).Fields()
	joined := strings.Join(fields, ",")
	if !strings.Contains(joined, "timeslot_1") || !strings.Contains(joined, "timeslot_2") {
		t.Errorf("both slots should be reported separately, got %v", fields)
	}
}

func TestAccessRoundTripsThroughJSON(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{
		Registration: ACL{Mode: "permit", IDs: []string{"312100"}},
		Talkgroups:   Talkgroups{Timeslot2: ACL{Mode: "permit", IDs: []string{"9", "3100-3199"}}},
	}

	var buf bytes.Buffer
	if err := Save(&buf, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	// The block must be readable in the file an operator edits by hand.
	if !strings.Contains(buf.String(), `"3100-3199"`) {
		t.Errorf("the range is not written as a string:\n%s", buf.String())
	}

	got, err := Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.DMR.Access == nil {
		t.Fatal("the access block did not survive the round trip")
	}
	lists, err := got.AccessLists()
	if err != nil {
		t.Fatalf("AccessLists: %v", err)
	}
	if !lists.Talkgroups(2).Allows(3150) || !lists.Registration.Allows(312100) {
		t.Error("the lists did not survive the round trip")
	}
}

// TestAbsentAccessIsOmittedFromJSON keeps the document honest. Writing
// "access": null would look like a setting that had been cleared rather than
// one never made.
func TestAbsentAccessIsOmittedFromJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := Save(&buf, Default()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if strings.Contains(buf.String(), `"access"`) {
		t.Errorf("an absent access block should not be written:\n%s", buf.String())
	}
}

func TestAccessAdvisories(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{Registration: ACL{Mode: "permit", IDs: []string{"3121001"}}}

	if err := c.Validate(); err != nil {
		t.Fatalf("an advisory must not become a validation error: %v", err)
	}
	if got := c.AccessAdvisories(); len(got) == 0 {
		t.Error("expected an advisory for a seven-digit registration entry")
	}

	c.DMR.Access = &Access{Registration: ACL{Mode: "permit", IDs: []string{"312100"}}}
	if got := c.AccessAdvisories(); len(got) != 0 {
		t.Errorf("a six-digit repeater ID should be unremarkable, got %v", got)
	}
}

func TestAccessAdvisoriesOnInvalidConfiguration(t *testing.T) {
	// Validate reports an invalid configuration in full. Repeating part of it
	// as advice would be noise, so advisories stay quiet.
	c := enabledDMR()
	c.DMR.Access = &Access{Registration: ACL{Mode: "permit", IDs: []string{"nope"}}}
	if got := c.AccessAdvisories(); got != nil {
		t.Errorf("advisories should be silent on an invalid configuration, got %v", got)
	}
}

func TestReachableBeyondHost(t *testing.T) {
	for _, tc := range []struct {
		listen string
		want   bool
	}{
		{"0.0.0.0:62031", true},
		{":62031", true},
		{"[::]:62031", true},
		{"192.168.1.247:62031", true},
		{"qsp.hopto.me:62031", true},
		{"127.0.0.1:62031", false},
		{"127.0.0.53:62031", false},
		{"[::1]:62031", false},
		// A malformed address is reported by Validate separately. Treating it
		// as reachable stops a typo becoming the way past the check.
		{"not-an-address", true},
		{"", true},
	} {
		if got := reachableBeyondHost(tc.listen); got != tc.want {
			t.Errorf("reachableBeyondHost(%q) = %v, want %v", tc.listen, got, tc.want)
		}
	}
}

// TestAccessKindsCoverEveryList guards against a list being added to the schema
// and quietly never parsed.
func TestAccessKindsCoverEveryList(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{
		Registration: ACL{Mode: "permit", IDs: []string{"312100"}},
		Subscribers:  ACL{Mode: "permit", IDs: []string{"3121001"}},
		Talkgroups: Talkgroups{
			Timeslot1: ACL{Mode: "permit", IDs: []string{"9"}},
			Timeslot2: ACL{Mode: "permit", IDs: []string{"9"}},
		},
	}
	lists, err := c.AccessLists()
	if err != nil {
		t.Fatalf("AccessLists: %v", err)
	}
	for name, l := range map[string]access.List{
		"registration": lists.Registration,
		"subscriber":   lists.Subscriber,
		"timeslot 1":   lists.Talkgroup1,
		"timeslot 2":   lists.Talkgroup2,
	} {
		if l.Permissive() {
			t.Errorf("the %s list was configured but came through permissive", name)
		}
	}
}
