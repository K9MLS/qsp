package access

import (
	"strings"
	"testing"
)

// TestZeroValuePermitsEverything is the compatibility guarantee. An absent
// access block must behave exactly as QSP did at 0.1.9, or upgrading
// disconnects a running club.
func TestZeroValuePermitsEverything(t *testing.T) {
	var l List
	for _, id := range []uint32{1, 9, 3100, 16777215, 4294967295} {
		if !l.Allows(id) {
			t.Errorf("the zero value refused %d; an absent access block must permit everything", id)
		}
	}
	if !l.Permissive() {
		t.Error("the zero value does not report itself permissive, so the startup check would not fire")
	}
	if got := l.Mode(); got != ModeDeny {
		t.Errorf("the zero value's mode is %q, want %q", got, ModeDeny)
	}
}

func TestDenyWithNoEntriesPermitsEverything(t *testing.T) {
	l, err := Parse("dmr.access.test", Talkgroup, ModeDeny, nil)
	if err != nil {
		t.Fatalf("deny with no entries should be accepted: %v", err)
	}
	if !l.Allows(3100) || !l.Permissive() {
		t.Error("deny naming nobody must refuse nobody")
	}
}

func TestPermitWithNoEntriesIsRefused(t *testing.T) {
	_, err := Parse("dmr.access.test", Talkgroup, ModePermit, nil)
	if err == nil {
		t.Fatal("permit with no entries refuses every station and must not be accepted silently")
	}
	// The error has to say what to write instead, or the operator is left
	// guessing how to express "permit everything".
	if !strings.Contains(err.Error(), `"mode": "deny"`) {
		t.Errorf("the error does not say how to permit everything: %v", err)
	}
}

func TestPermitAndDeny(t *testing.T) {
	permit, err := Parse("dmr.access.test", Talkgroup, ModePermit, []string{"9", "3100-3199"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	deny, err := Parse("dmr.access.test", Talkgroup, ModeDeny, []string{"9", "3100-3199"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	for _, tc := range []struct {
		id     uint32
		permit bool
	}{
		{9, true},
		{8, false},
		{10, false},
		{3099, false},
		{3100, true},
		{3150, true},
		{3199, true},
		{3200, false},
	} {
		if got := permit.Allows(tc.id); got != tc.permit {
			t.Errorf("permit list allows %d = %v, want %v", tc.id, got, tc.permit)
		}
		// A deny list over the same entries is the exact complement.
		if got := deny.Allows(tc.id); got == tc.permit {
			t.Errorf("deny list allows %d = %v; it must be the complement of the permit list", tc.id, got)
		}
	}
}

// TestRegistrationCeilingAdmitsHotspots is the finding that would otherwise
// have been a field bug. A hotspot registers with its owner's seven-digit ID
// and a two-digit suffix, which is nine digits and does not fit in the 24 bits
// that carry a talkgroup. One shared ceiling would refuse every hotspot.
func TestRegistrationCeilingAdmitsHotspots(t *testing.T) {
	const hotspot = "312100101" // a nine-digit hotspot ID
	if _, err := Parse("dmr.access.test", Registration, ModePermit, []string{hotspot}); err != nil {
		t.Fatalf("a registration list must accept a nine-digit hotspot ID: %v", err)
	}
	if _, err := Parse("dmr.access.test", Subscriber, ModePermit, []string{hotspot}); err == nil {
		t.Fatal("a subscriber ID travels in 24 bits and cannot be nine digits; it must be refused")
	}
	if _, err := Parse("dmr.access.test", Talkgroup, ModePermit, []string{"16777215"}); err != nil {
		t.Fatalf("16777215 is the largest 24-bit value and must be accepted: %v", err)
	}
	if _, err := Parse("dmr.access.test", Talkgroup, ModePermit, []string{"16777216"}); err == nil {
		t.Fatal("16777216 does not fit in 24 bits and must be refused")
	}
}

func TestRejectedEntries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entry   string
		wantErr string
	}{
		{"empty", "", "is empty"},
		{"letters", "abc", "not a number"},
		{"a callsign", "K9MLS", "not a number"},
		{"internal space", "3100 - 3199", "not a number"},
		{"leading plus", "+3100", "not a number"},
		{"underscores", "3_100", "not a number"},
		{"negative", "-3100", "missing a number"},
		{"trailing hyphen", "3100-", "missing a number"},
		{"backwards range", "3199-3100", "runs backwards"},
		{"zero", "0", "not a valid station"},
		{"zero in a range", "0-99", "not a valid station"},
		{"above the ceiling", "99999999", "above the largest value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse("dmr.access.test", Talkgroup, ModePermit, []string{tc.entry})
			if err == nil {
				t.Fatalf("%q was accepted", tc.entry)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error for %q is %q, want it to mention %q", tc.entry, err, tc.wantErr)
			}
			// Every error names the setting, so it is actionable without
			// reading the documentation. ADR-0012 set that precedent.
			if !strings.Contains(err.Error(), "dmr.access.test") {
				t.Errorf("error for %q does not name the setting: %v", tc.entry, err)
			}
		})
	}
}

func TestBackwardsRangeIsNotSilentlySwapped(t *testing.T) {
	// Swapping would accept a range the operator did not write. On a permit
	// list the two readings differ by everything.
	_, err := Parse("dmr.access.test", Talkgroup, ModePermit, []string{"3199-3100"})
	if err == nil {
		t.Fatal("a backwards range was accepted")
	}
	if !strings.Contains(err.Error(), "3100-3199") {
		t.Errorf("the error should suggest the corrected range: %v", err)
	}
}

func TestSurroundingWhitespaceIsTolerated(t *testing.T) {
	l, err := Parse("dmr.access.test", Talkgroup, ModePermit, []string{"  9  ", "\t3100-3199\n"})
	if err != nil {
		t.Fatalf("a hand-edited file accumulates whitespace around entries: %v", err)
	}
	if !l.Allows(9) || !l.Allows(3150) {
		t.Error("entries with surrounding whitespace did not parse to the right values")
	}
}

func TestBadMode(t *testing.T) {
	if _, err := Parse("dmr.access.test", Talkgroup, "allow", []string{"9"}); err == nil {
		t.Fatal(`"allow" is not a mode and must be refused`)
	}
	// An empty mode is the zero value and means deny.
	l, err := Parse("dmr.access.test", Talkgroup, "", []string{"9"})
	if err != nil {
		t.Fatalf("an empty mode should default to deny: %v", err)
	}
	if l.Allows(9) || !l.Allows(10) {
		t.Error("an empty mode did not behave as deny")
	}
}

func TestOverlappingEntriesMerge(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		want    string
	}{
		{"an ID inside a range", []string{"3100-3199", "3150"}, "3100-3199"},
		{"overlapping ranges", []string{"3100-3150", "3140-3199"}, "3100-3199"},
		{"adjacent ranges", []string{"100-199", "200-299"}, "100-299"},
		{"an ID adjacent to a range", []string{"3100-3199", "3200"}, "3100-3200"},
		{"duplicates", []string{"9", "9", "9"}, "9"},
		{"unsorted input", []string{"3100", "9", "91"}, "9,91,3100"},
		{"a range of one", []string{"9-9"}, "9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := Parse("dmr.access.test", Talkgroup, ModePermit, tc.entries)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := strings.Join(l.Entries(), ","); got != tc.want {
				t.Errorf("entries are %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEntriesRoundTrip matters because the configuration is versioned and
// diffed. A document QSP has loaded and re-saved must be one the operator still
// recognises, and must not drift on each save.
func TestEntriesRoundTrip(t *testing.T) {
	first, err := Parse("dmr.access.test", Talkgroup, ModePermit, []string{"3100-3199", "9", "91"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	second, err := Parse("dmr.access.test", Talkgroup, ModePermit, first.Entries())
	if err != nil {
		t.Fatalf("reparsing what Entries produced failed: %v", err)
	}
	if strings.Join(first.Entries(), ",") != strings.Join(second.Entries(), ",") {
		t.Errorf("entries drifted on a round trip: %v then %v", first.Entries(), second.Entries())
	}
	for id := uint32(1); id < 4000; id++ {
		if first.Allows(id) != second.Allows(id) {
			t.Fatalf("the round-tripped list disagrees about %d", id)
		}
	}
}

func TestPermissive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    Mode
		entries []string
		want    bool
	}{
		{"deny nobody", ModeDeny, nil, true},
		{"deny somebody", ModeDeny, []string{"9"}, false},
		{"permit somebody", ModePermit, []string{"9"}, false},
		{"permit the whole range", ModePermit, []string{"1-16777215"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := Parse("dmr.access.test", Talkgroup, tc.mode, tc.entries)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := l.Permissive(); got != tc.want {
				t.Errorf("Permissive = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPermitEverythingByRangeIsNotPermissive records a deliberate distinction.
// An operator who writes out the whole range has said what they mean, and the
// startup check is about silence rather than about the resulting behaviour.
func TestPermitEverythingByRangeIsNotPermissive(t *testing.T) {
	l, err := Parse("dmr.access.test", Talkgroup, ModePermit, []string{"1-16777215"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !l.Allows(3100) {
		t.Error("a list covering the whole range should allow everything")
	}
	if l.Permissive() {
		t.Error("an explicit full range is a deliberate choice and should not trip the startup check")
	}
}

func TestAdvisories(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kind  Kind
		entry string
		want  bool
	}{
		{"six-digit repeater ID", Registration, "312100", false},
		{"nine-digit hotspot ID", Registration, "312100101", false},
		{"seven-digit operator ID", Registration, "3121001", true},
		{"eight-digit ID", Registration, "31210011", true},
		{"a talkgroup is not checked", Talkgroup, "3121001", false},
		{"a subscriber is not checked", Subscriber, "3121001", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := Parse("dmr.access.test", tc.kind, ModePermit, []string{tc.entry})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := l.Advisories("dmr.access.registration", tc.kind)
			if (len(got) > 0) != tc.want {
				t.Errorf("advisories for %s %q = %v, want any = %v", tc.kind, tc.entry, got, tc.want)
			}
		})
	}
}

// TestAdvisoriesAreNotErrors is the point of them. The numbering they check is
// a registry convention rather than a rule of the protocol, so a list that
// trips one still parses and still works.
func TestAdvisoriesAreNotErrors(t *testing.T) {
	l, err := Parse("dmr.access.test", Registration, ModePermit, []string{"3121001"})
	if err != nil {
		t.Fatalf("an advisory must not become an error: %v", err)
	}
	if !l.Allows(3121001) {
		t.Error("an entry that trips an advisory must still be honoured")
	}
	if len(l.Advisories("dmr.access.registration", Registration)) == 0 {
		t.Error("expected an advisory for a seven-digit registration entry")
	}
}

func TestString(t *testing.T) {
	var zero List
	if got := zero.String(); got != "permit everything" {
		t.Errorf("the zero value renders as %q", got)
	}
	l, err := Parse("dmr.access.test", Talkgroup, ModePermit, []string{"9", "3100-3199"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := l.String(); got != "permit 9,3100-3199" {
		t.Errorf("String = %q", got)
	}
}

func TestZeroListsPermitEverything(t *testing.T) {
	var l Lists
	if !l.Permissive() {
		t.Error("the zero Lists does not report itself permissive, so the startup check would not fire")
	}
	for _, ts := range []int{1, 2} {
		if !l.Talkgroups(ts).Allows(3100) {
			t.Errorf("the zero Lists refused a talkgroup on timeslot %d", ts)
		}
	}
	if !l.Registration.Allows(312100) || !l.Subscriber.Allows(3121001) {
		t.Error("the zero Lists refused a registration or a subscriber")
	}
}

func TestListsTalkgroupsPerTimeslot(t *testing.T) {
	one, err := Parse("dmr.access.talkgroups.timeslot_1", Talkgroup, ModePermit, []string{"9"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	two, err := Parse("dmr.access.talkgroups.timeslot_2", Talkgroup, ModePermit, []string{"3100"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l := Lists{Talkgroup1: one, Talkgroup2: two}

	if !l.Talkgroups(1).Allows(9) || l.Talkgroups(1).Allows(3100) {
		t.Error("timeslot 1 is not using its own list")
	}
	if !l.Talkgroups(2).Allows(3100) || l.Talkgroups(2).Allows(9) {
		t.Error("timeslot 2 is not using its own list")
	}
	if l.Permissive() {
		t.Error("a set with a configured list is not permissive")
	}
	// An unrecognised timeslot is a protocol problem, refused where protocol
	// problems are refused rather than disguised as an access decision.
	if !l.Talkgroups(3).Allows(9) {
		t.Error("an unrecognised timeslot should get a permissive list, not a refusal")
	}
}

// TestErrorsNameTheFieldTheyCameFrom is why the field is threaded through: the
// talkgroup lists are per timeslot, so an error saying only "talkgroups" would
// not tell the operator which of the two to edit.
func TestErrorsNameTheFieldTheyCameFrom(t *testing.T) {
	const field = "dmr.access.talkgroups.timeslot_2"
	_, err := Parse(field, Talkgroup, ModePermit, []string{"nope"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.HasPrefix(err.Error(), field) {
		t.Errorf("the error does not lead with the field: %v", err)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"9", "3100-3199", "", "-", "0", "abc", "16777216", "3199-3100", " 9 "} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, entry string) {
		for _, kind := range []Kind{Registration, Subscriber, Talkgroup} {
			l, err := Parse("dmr.access.test", kind, ModePermit, []string{entry})
			if err != nil {
				continue
			}
			// Anything that parses must be evaluable without panicking, and
			// must round-trip.
			l.Allows(0)
			l.Allows(kind.Ceiling())
			again, err := Parse("dmr.access.test", kind, ModePermit, l.Entries())
			if err != nil {
				t.Fatalf("%q parsed but its own Entries() did not: %v", entry, err)
			}
			if strings.Join(again.Entries(), ",") != strings.Join(l.Entries(), ",") {
				t.Fatalf("%q does not round-trip: %v then %v", entry, l.Entries(), again.Entries())
			}
		}
	})
}
