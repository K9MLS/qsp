// Package access decides which stations QSP will carry.
//
// It holds the four lists ADR-0020 describes — which repeaters may register,
// which subscribers may transmit, and which talkgroups are carried on each
// timeslot — and answers whether an ID is permitted.
//
// The package performs no I/O, holds no mutable state, and imports nothing from
// the rest of QSP. Both internal/peers and internal/routing depend on it, and
// routing must not acquire an edge to peers; keeping the evaluation pure is what
// makes that possible, and matches what ADR-0013 requires of the routing
// decision itself.
package access

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kind names the list an ID is being tested against.
//
// It exists for two reasons: an error message that names the setting is worth
// more than one that does not, and the three lists do not share a ceiling.
type Kind int

// The lists.
const (
	// Registration is the set of repeater IDs permitted to register.
	Registration Kind = iota
	// Subscriber is the set of subscriber IDs permitted to transmit.
	Subscriber
	// Talkgroup is the set of talkgroups carried, held per timeslot.
	Talkgroup
)

// Setting returns the configuration field this list is loaded from.
func (k Kind) Setting() string {
	switch k {
	case Registration:
		return "access.registration"
	case Subscriber:
		return "access.subscribers"
	case Talkgroup:
		return "access.talkgroups"
	}
	return "access"
}

func (k Kind) String() string { return k.Setting() }

// Ceiling is the largest ID this list can hold, taken from the width of the
// field the ID travels in rather than from any network's conventions.
//
// **The three are not the same, and assuming they were would be a live bug.**
// A DMRD frame carries the source and target as 24-bit values, so a talkgroup
// or subscriber ID above 16777215 cannot appear on the air and is a typo. The
// repeater ID is a 32-bit field, and must be, because a hotspot registers with
// its owner's seven-digit ID and a two-digit suffix — nine digits, which does
// not fit in 24 bits. A single shared ceiling would refuse every hotspot.
func (k Kind) Ceiling() uint32 {
	if k == Registration {
		return 1<<32 - 1
	}
	return 1<<24 - 1
}

// Mode says whether a list names what is allowed or what is refused.
type Mode string

// The modes.
const (
	// ModePermit refuses anything the list does not name.
	ModePermit Mode = "permit"
	// ModeDeny allows anything the list does not name. This is the zero
	// value's behaviour.
	ModeDeny Mode = "deny"
)

// span is an inclusive range of IDs. A single ID is a span of one.
type span struct{ lo, hi uint32 }

// List is a parsed access control list.
//
// **The zero value permits everything**, because a deny list naming nobody
// refuses nobody. That is deliberate: an absent access block must behave
// exactly as QSP did before access control existed, so that upgrading cannot
// silently disconnect a running club. The same reasoning made
// routing.CoreOptions.NoRepeat a negative — the empty struct has to be the
// correct one.
//
// A List is immutable once parsed and safe to share between goroutines.
type List struct {
	mode Mode
	// spans are sorted by lo and do not overlap.
	spans []span
}

// Parse builds a list from a mode and a set of entries.
//
// Each entry is a single ID ("3100") or an inclusive range ("3100-3199").
// Whitespace around an entry is tolerated because a hand-edited JSON file
// accumulates it; whitespace inside one is not, since "3100 - 3199" more often
// means a mistake than a preference.
func Parse(kind Kind, mode Mode, entries []string) (List, error) {
	switch mode {
	case "", ModeDeny:
		mode = ModeDeny
	case ModePermit:
	default:
		return List{}, fmt.Errorf("%s: mode is %q; it must be %q or %q",
			kind.Setting(), mode, ModePermit, ModeDeny)
	}

	if mode == ModePermit && len(entries) == 0 {
		// Permit-nothing refuses every station on the network. Nobody
		// configures that deliberately, and discovering it by having the
		// network go silent is a poor way to find out.
		return List{}, fmt.Errorf("%s: mode is %q with no entries, which refuses every station; "+
			`use {"mode": "deny", "ids": []} to permit everything`, kind.Setting(), ModePermit)
	}

	spans := make([]span, 0, len(entries))
	for _, raw := range entries {
		s, err := parseEntry(kind, raw)
		if err != nil {
			return List{}, err
		}
		spans = append(spans, s)
	}

	return List{mode: mode, spans: merge(spans)}, nil
}

func parseEntry(kind Kind, raw string) (span, error) {
	entry := strings.TrimSpace(raw)
	if entry == "" {
		return span{}, fmt.Errorf("%s: an entry is empty; each entry is an ID or a range such as \"3100-3199\"", kind.Setting())
	}

	lo, hi, isRange := strings.Cut(entry, "-")
	if !isRange {
		id, err := parseID(kind, entry, entry)
		return span{lo: id, hi: id}, err
	}

	low, err := parseID(kind, lo, entry)
	if err != nil {
		return span{}, err
	}
	high, err := parseID(kind, hi, entry)
	if err != nil {
		return span{}, err
	}
	if low > high {
		// Swapping it silently would accept a range the operator did not
		// write, and the two readings differ by a great deal when the list
		// is a permit list.
		return span{}, fmt.Errorf("%s: range %q runs backwards; write it as %d-%d",
			kind.Setting(), entry, high, low)
	}
	return span{lo: low, hi: high}, nil
}

func parseID(kind Kind, field, entry string) (uint32, error) {
	if field == "" {
		return 0, fmt.Errorf("%s: range %q is missing a number on one side", kind.Setting(), entry)
	}
	// ParseUint accepts a leading plus and underscores; neither belongs in a
	// radio ID, and accepting them would mean two spellings of one value in a
	// document that is versioned and diffed.
	for _, r := range field {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%s: %q is not a number; entries are digits, optionally as a range such as \"3100-3199\"",
				kind.Setting(), entry)
		}
	}
	id, err := strconv.ParseUint(field, 10, 64)
	if err != nil || id > uint64(kind.Ceiling()) {
		return 0, fmt.Errorf("%s: %s is above the largest value this field can carry (%d)",
			kind.Setting(), field, kind.Ceiling())
	}
	if id == 0 {
		return 0, fmt.Errorf("%s: 0 is not a valid station", kind.Setting())
	}
	return uint32(id), nil
}

// merge sorts spans and coalesces those that touch or overlap.
//
// Overlap is redundancy rather than error — an operator listing a range and one
// ID inside it means both — so it is folded away rather than refused.
func merge(spans []span) []span {
	if len(spans) == 0 {
		return nil
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].lo != spans[j].lo {
			return spans[i].lo < spans[j].lo
		}
		return spans[i].hi < spans[j].hi
	})
	out := spans[:1]
	for _, s := range spans[1:] {
		last := &out[len(out)-1]
		// +1 so that 100-199 and 200-299 become one span rather than two.
		if s.lo <= last.hi || s.lo == last.hi+1 {
			if s.hi > last.hi {
				last.hi = s.hi
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// Allows reports whether an ID may pass.
//
// On the zero value it reports true, which is what makes an absent access block
// behave as QSP did before this package existed.
func (l List) Allows(id uint32) bool {
	named := l.names(id)
	if l.mode == ModePermit {
		return named
	}
	return !named
}

// names reports whether the list mentions an ID, whatever the mode.
func (l List) names(id uint32) bool {
	// Binary search for the first span that could contain the ID. Lists are
	// short today, but this runs per destination per frame on the routing hot
	// path and the fan-out test carries a hundred peers.
	i := sort.Search(len(l.spans), func(i int) bool { return l.spans[i].hi >= id })
	return i < len(l.spans) && l.spans[i].lo <= id
}

// Permissive reports whether the list allows every possible ID.
//
// The startup check uses this: a listener reachable from beyond this host with
// nothing but permissive lists is the configuration ADR-0020 refuses to start
// with unless the operator has said permissive is what they meant.
func (l List) Permissive() bool { return l.mode != ModePermit && len(l.spans) == 0 }

// Mode returns the list's mode, defaulting to deny.
func (l List) Mode() Mode {
	if l.mode == "" {
		return ModeDeny
	}
	return l.mode
}

// Entries returns the list in the form it was written, ranges coalesced.
//
// It round-trips through Parse, so a configuration QSP has loaded and re-saved
// is one the operator still recognises.
func (l List) Entries() []string {
	out := make([]string, 0, len(l.spans))
	for _, s := range l.spans {
		if s.lo == s.hi {
			out = append(out, strconv.FormatUint(uint64(s.lo), 10))
			continue
		}
		out = append(out, fmt.Sprintf("%d-%d", s.lo, s.hi))
	}
	return out
}

// String renders the list for a log line or an error.
func (l List) String() string {
	if l.Permissive() {
		return "permit everything"
	}
	return string(l.Mode()) + " " + strings.Join(l.Entries(), ",")
}

// Advisories reports entries that parse but look like mistakes.
//
// These are never errors. The ID numbering they check against is a convention
// of the RadioID.net registry rather than a rule of the protocol — the
// underlying value is a flat 24-bit number and nothing enforces the structure —
// so QSP says what it noticed and carries on. Refusing on a convention would
// make QSP wrong the day the convention changed.
func (l List) Advisories(kind Kind) []string {
	if kind != Registration {
		return nil
	}
	var out []string
	for _, s := range l.spans {
		if s.lo != s.hi {
			// A range spans digit lengths often enough that flagging it
			// would be noise.
			continue
		}
		switch digits(s.lo) {
		case 7:
			out = append(out, fmt.Sprintf(
				"%s names %d, a seven-digit ID: the registry issues those to operators, "+
					"while repeaters register with six digits and hotspots with a "+
					"seven-digit ID plus a two-digit suffix", kind.Setting(), s.lo))
		case 8:
			out = append(out, fmt.Sprintf(
				"%s names %d, an eight-digit ID: a hotspot suffix is two digits, so this is "+
					"most likely a nine-digit ID with one missing", kind.Setting(), s.lo))
		}
	}
	return out
}

func digits(id uint32) int { return len(strconv.FormatUint(uint64(id), 10)) }
