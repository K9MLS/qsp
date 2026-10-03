// Package weather watches the National Weather Service for alerts in the
// areas an operator chose, and decides which of them to put on the air as a
// group text (ADR-0068, as amended 2026-09-29).
//
// **It is off until an operator turns it on, from the Weather page.** Nothing
// here is configured on a command line. An operator gives the NWS county or
// forecast-zone codes they already know from SkywarnPlus or alerts.weather.gov,
// ticks the kinds of alert they want, and chooses a talkgroup.
//
// **It starts in Preview**: every alert that would go on the air is shown on
// the Weather page and logged, exactly as it would read on a radio, so an
// operator can watch real alerts for their area before anything reaches a
// radio. Switched to Transmit, an alert goes out as a group text on this
// server's own stations only — never to a linked server or a bridged network,
// because weather is local.
//
// The package has no dependency on the rest of QSP beyond the text length a
// radio can display: the binary converts the configuration into Settings, and
// the console reads Status.
package weather

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	// **The zone's own time zone, on a server that may not have one.** An
	// alert says "until 7:45PM CDT" in the time of the area it covers, which
	// NWS names per zone. The container image is built from scratch and has
	// no zoneinfo, so without this every time would silently come out in UTC.
	_ "time/tzdata"

	"github.com/k9mls/qsp/internal/tms"
)

// DefaultBaseURL is the National Weather Service API.
//
// No key and no account: it is the United States Government's own service,
// which is also the source Part 97 §97.113(e) names for retransmitting weather
// information. It asks for a User-Agent carrying contact details and refuses
// anonymous clients, which is why Settings carries a contact.
const DefaultBaseURL = "https://api.weather.gov"

// PollInterval is how often the active alerts are read.
//
// Once a minute is well inside what NWS asks of automated clients and quicker
// than any warning's lead time matters at. Not a setting: an operator has no
// way to judge it, and a wrong value either misses warnings or earns a block.
const PollInterval = time.Minute

// MaxText is the longest alert text, in UTF-16 units: what one group text can
// carry and a radio can show.
const MaxText = tms.MaxText

// DefaultEvents are the alert types ticked on a new Weather page.
//
// The warnings and watches for weather that hurts people quickly. An operator
// adds winter storms, heat or anything else NWS issues by ticking it; these are
// only where a new page starts.
var DefaultEvents = []string{EveryWarning, EveryWatch}

// Classes of alert an operator can choose in place of exact names.
const (
	EveryWarning = "* Warning"
	EveryWatch   = "* Watch"
	EveryAlert   = "*"
)

// NarrowDefaultEvents is what the Weather page started with before classes
// existed. A saved list that is exactly this was never chosen by anybody, and
// configuration widens it to DefaultEvents when it is read.
var NarrowDefaultEvents = []string{
	"Tornado Warning",
	"Severe Thunderstorm Warning",
	"Flash Flood Warning",
	"Tornado Watch",
	"Severe Thunderstorm Watch",
}

// Settings is what the Weather page decides.
type Settings struct {
	// Enabled watches for alerts. Off, nothing is fetched at all.
	Enabled bool
	// Zones are NWS county codes (TXC121) and forecast-zone codes (TXZ103),
	// upper case. An alert is considered when any of its areas is one of
	// these.
	Zones []string
	// Events are the NWS event names to put on the air, such as
	// "Tornado Warning". Compared without regard to case.
	Events []string
	// Transmit puts alerts on the air. Off is Preview: shown and logged only.
	Transmit bool
	// Talkgroup, Timeslot and SenderID are where an alert goes and whom it
	// comes from.
	Talkgroup uint32
	Timeslot  int
	SenderID  uint32
	// Contact is the email NWS is given in the User-Agent.
	Contact string
}

// zoneCode is the shape of an NWS county (C) or forecast-zone (Z) code.
var zoneCode = regexp.MustCompile(`^[A-Z]{2}[CZ][0-9]{3}$`)

// ValidZoneCode reports whether s looks like an NWS county or zone code. It
// says nothing about whether NWS knows it; CheckZones asks.
func ValidZoneCode(s string) bool { return zoneCode.MatchString(s) }

// NormalizeZones upper-cases, trims and de-duplicates codes, keeping order.
// Codes may arrive separated by commas, spaces or new lines, because that is
// how people paste them.
func NormalizeZones(in []string) []string {
	var out []string
	for _, item := range in {
		for _, f := range strings.FieldsFunc(item, func(r rune) bool {
			return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
		}) {
			code := strings.ToUpper(strings.TrimSpace(f))
			if code != "" && !slices.Contains(out, code) {
				out = append(out, code)
			}
		}
	}
	return out
}

// Zone is what NWS says about one county or forecast zone.
type Zone struct {
	// Code is the county or zone code, such as TXC121.
	Code string `json:"code"`
	// Name is NWS's name for it, such as "Denton".
	Name string `json:"name"`
	// State is the two-letter state.
	State string `json:"state"`
	// Kind is "county" or "forecast".
	Kind string `json:"kind"`
	// TimeZone is the IANA time zone NWS gives for the area, used to write an
	// alert's expiry in local time.
	TimeZone string `json:"time_zone,omitempty"`
}

// ZoneCheck is the answer to "is this a code NWS knows", for the page.
type ZoneCheck struct {
	Code string `json:"code"`
	// OK means NWS knows the code, and Zone is filled in.
	OK   bool  `json:"ok"`
	Zone *Zone `json:"zone,omitempty"`
	// Problem says why not, in words for the operator.
	Problem string `json:"problem,omitempty"`
}

// Alert is the part of an NWS alert QSP reads.
type Alert struct {
	ID          string
	Status      string
	MessageType string
	Event       string
	Severity    string
	Headline    string
	AreaDesc    string
	// UGC are the county and zone codes the alert covers.
	UGC []string
	// SAME are the counties it covers as FIPS codes, which NWS gives on every
	// alert whether it was issued by county or by forecast zone.
	SAME []string
	// References are the IDs of earlier alerts this one updates or cancels.
	References []string
	Sent       time.Time
	Expires    time.Time
	// Ends is when the hazard ends. NWS leaves it null on many alerts while
	// Expires is set, so it is preferred only when present.
	Ends time.Time
}

// until is when the alert stops applying: Ends if NWS gave one, else Expires.
func (a Alert) until() time.Time {
	if !a.Ends.IsZero() {
		return a.Ends
	}
	return a.Expires
}

// fits reports whether s is short enough for one group text.
func fits(s string) bool { return len(utf16.Encode([]rune(s))) <= MaxText }
