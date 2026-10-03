package weather

import (
	"slices"
	"strings"
	"time"
)

// Verdict is what was decided about one alert.
type Verdict string

const (
	// Send means the alert passed every check and goes on the air — in this
	// version, into the preview only.
	Send Verdict = "send"
	// Hold means it did not, for the Reason given.
	Hold Verdict = "hold"
)

// Reasons an alert is held, in the operator's words. They are what the
// Weather page shows beside each alert, so each one says what happened rather
// than which rule fired.
const (
	ReasonNotReal     = "not a real alert: the National Weather Service marked it as a test or exercise"
	ReasonCancel      = "the National Weather Service cancelled an earlier alert"
	ReasonNotHere     = "not for the areas you chose"
	ReasonNotChosen   = "not a type of alert you chose"
	ReasonExpired     = "already expired"
	ReasonAlreadySent = "already sent"
	ReasonUpdate      = "an update to an alert already sent, saying nothing new"
	ReasonBaseline    = "issued before QSP started; a restart does not repeat what was already in effect"
)

// Decide runs one alert through the filter chain of ADR-0068, in its order.
//
// sent holds the IDs of alerts already decided on, sent or not; it is how an
// alert is decided once rather than once a minute, and how an update to one
// is recognised. Decide does not change it — the caller records the outcome.
//
// **The status check is first and must stay first.** NWS issues tests and
// exercises through the same feed, and without it a required monthly test
// reads as a tornado warning on somebody's radio.
func Decide(a Alert, s Settings, sent map[string]bool, now time.Time) (Verdict, string) {
	switch {
	case !strings.EqualFold(a.Status, "Actual"):
		return Hold, ReasonNotReal
	case strings.EqualFold(a.MessageType, "Cancel"):
		return Hold, ReasonCancel
	case !coversAny(a, s.Zones):
		return Hold, ReasonNotHere
	case !chosen(a.Event, s.Events):
		return Hold, ReasonNotChosen
	case !a.until().IsZero() && !now.Before(a.until()):
		return Hold, ReasonExpired
	case sent[a.ID]:
		return Hold, ReasonAlreadySent
	}
	// **An update is not a new alert.** A warning is reissued as its polygon
	// moves, with a new ID that references the old one. Sending each would put
	// the same storm on the air every few minutes.
	for _, ref := range a.References {
		if sent[ref] {
			return Hold, ReasonUpdate
		}
	}
	return Send, ""
}

func coversAny(a Alert, codes []string) bool {
	return slices.ContainsFunc(codes, func(code string) bool { return covers(a, code) })
}

// covers reports whether an alert applies to one of the operator's codes.
//
// **By UGC, and for a county by SAME as well** (ADR-0068 names both). NWS
// issues warnings by county (TXC121) and most watches and advisories by
// forecast zone (TXZ103), and an alert lists only the kind it was issued by in
// UGC. Every alert also lists its counties as SAME codes — the FIPS state and
// county — so a county code catches a zone-issued alert through them. Without
// this, an operator who gave only their county missed every winter storm and
// heat warning for it. A zone code has no FIPS form, which is why the page
// asks for the county too.
func covers(a Alert, code string) bool {
	for _, u := range a.UGC {
		if strings.EqualFold(u, code) {
			return true
		}
	}
	if len(code) != 6 || code[2] != 'C' {
		return false
	}
	state, ok := stateFIPS[code[:2]]
	if !ok {
		return false
	}
	want := state + code[3:]
	for _, same := range a.SAME {
		// Six digits: a part-of-county digit, then the state and county.
		if len(same) == 6 && same[1:] == want {
			return true
		}
	}
	return false
}

// stateFIPS maps a UGC state prefix to its FIPS state code, for reading SAME.
var stateFIPS = map[string]string{
	"AL": "01", "AK": "02", "AZ": "04", "AR": "05", "CA": "06", "CO": "08", "CT": "09",
	"DE": "10", "DC": "11", "FL": "12", "GA": "13", "HI": "15", "ID": "16", "IL": "17",
	"IN": "18", "IA": "19", "KS": "20", "KY": "21", "LA": "22", "ME": "23", "MD": "24",
	"MA": "25", "MI": "26", "MN": "27", "MS": "28", "MO": "29", "MT": "30", "NE": "31",
	"NV": "32", "NH": "33", "NJ": "34", "NM": "35", "NY": "36", "NC": "37", "ND": "38",
	"OH": "39", "OK": "40", "OR": "41", "PA": "42", "RI": "44", "SC": "45", "SD": "46",
	"TN": "47", "TX": "48", "UT": "49", "VT": "50", "VA": "51", "WA": "53", "WV": "54",
	"WI": "55", "WY": "56", "AS": "60", "GU": "66", "MP": "69", "PR": "72", "VI": "78",
}

// chosen reports whether an alert's type is one the operator asked for.
//
// **A choice may be a whole class.** "* Warning" is every alert whose name
// ends in Warning, "* Watch" every watch, and "*" everything NWS issues for
// the area. NWS has well over a hundred alert types, and a list of exact
// names is a list of the ones nobody thought of: the first week on the air a
// Flood Watch sat over the county for two days and nothing was sent, because
// the list said Flash Flood. Classes are what a new Weather page starts with.
func chosen(event string, events []string) bool {
	event = strings.TrimSpace(event)
	for _, e := range events {
		e = strings.TrimSpace(e)
		// "*Warning" is read as "* Warning": a class typed by hand without
		// its space would otherwise be an exact name no alert has.
		switch class, isClass := strings.CutPrefix(e, "*"); {
		case e == EveryAlert:
			return true
		case isClass:
			words := strings.Fields(event)
			if len(words) > 0 && strings.EqualFold(words[len(words)-1], strings.TrimSpace(class)) {
				return true
			}
		case strings.EqualFold(e, event):
			return true
		}
	}
	return false
}

// Format writes an alert as it will read on a radio:
//
//	TORNADO WARNING Denton until 7:45PM CDT
//
// **Event, area, expiry, and nothing else.** NWS's headline is prose and its
// description runs to a dozen lines; neither survives being cut to fit, so
// neither is used (ADR-0068). The area is the name of the first of the
// operator's own zones, in their order, that the alert covers, so a warning
// for four counties names the one the operator chose. The time is in that
// zone's own time zone, with the day added when it is not today there. An
// alert with no set end reads "FLOOD WARNING Denton until further notice". If NWS
// has not yet said what the zone is called, the server's own time zone is used
// rather than UTC, which on the air would read as a wrong time.
//
// The result always fits one group text. If the full form is too long the
// area goes, then the expiry, then the event name is cut.
func Format(a Alert, zones []Zone, now time.Time) string {
	event := strings.ToUpper(strings.TrimSpace(a.Event))
	area, loc := "", time.Local
	for _, z := range zones {
		if !covers(a, z.Code) {
			continue
		}
		area = z.Name
		if z.TimeZone != "" {
			if l, err := time.LoadLocation(z.TimeZone); err == nil {
				loc = l
			}
		}
		break
	}
	if area == "" {
		// The alert covers the operator's area through a code they did not
		// give a name for, which cannot happen through the page but costs
		// nothing to handle: NWS's own first area name.
		area, _, _ = strings.Cut(a.AreaDesc, ";")
		area = strings.TrimSpace(area)
	}

	// An alert NWS issued until further notice says so. Its Expires is only
	// when the message is next reissued, and on the air it read as an end.
	until := ""
	if a.Ends.IsZero() && a.NoSetEnd {
		until = "until further notice"
	} else if t := a.hazardEnd(); !t.IsZero() {
		local := t.In(loc)
		layout := "3:04PM MST"
		if !sameDay(local, now.In(loc)) {
			layout = "Mon 3:04PM MST"
		}
		until = "until " + local.Format(layout)
	}

	for _, candidate := range []string{
		join(event, area, until),
		join(event, until),
		event,
	} {
		if fits(candidate) {
			return candidate
		}
	}
	r := []rune(event)
	for !fits(string(r)) {
		r = r[:len(r)-1]
	}
	return strings.TrimSpace(string(r))
}

func sameDay(a, b time.Time) bool {
	y1, m1, d1 := a.Date()
	y2, m2, d2 := b.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

func join(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}
