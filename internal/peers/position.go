package peers

import (
	"fmt"
	"strconv"
	"strings"
)

// Position is where a peer says it is.
//
// **Says**, not is. These fields arrive in `RPTC` as fixed-width free text from
// a station QSP does not control, and nothing verifies them. A hotspot in a
// rucksack announces whatever its owner typed into a web form months ago.
type Position struct {
	// Latitude and Longitude are decimal degrees. Valid only when Located.
	Latitude  float64
	Longitude float64
	// Located reports whether the coordinates parsed and are plausible.
	Located bool
	// Height is metres above ground, and zero when absent or unparseable —
	// which is indistinguishable from a peer that is genuinely at ground
	// level, and does not matter, because nothing depends on the difference.
	Height int
	// Location is the free-text place name, exactly as announced. It is shown
	// whether or not the coordinates parsed: "Denton, TX" is useful to an
	// operator even when the latitude field holds rubbish.
	Location string
	// Refused explains coordinates that arrived and were not used, and is
	// empty when none arrived or when they were fine.
	//
	// **A peer that sent nothing and a peer that sent something unusable need
	// different things done about them**, and they looked identical from the
	// console: both produced no pin and the same empty state. The first is a
	// hotspot nobody has configured; the second is a hotspot configured wrongly,
	// and only its owner can tell which if nobody says.
	Refused string
}

// Position reports where the peer says it is.
//
// A peer that has not sent its configuration has no position, and neither does
// one whose coordinates do not parse. **Reporting a position QSP cannot stand
// behind would be worse than reporting none**: a pin in the wrong place is
// believed, while a missing pin prompts somebody to ask.
func (p *Peer) Position() Position {
	if p.Config == nil {
		return Position{}
	}

	pos := Position{
		Location: strings.TrimSpace(p.Config.Location),
		Height:   parseHeight(p.Config.Height),
	}

	rawLat := strings.TrimSpace(p.Config.Latitude)
	rawLon := strings.TrimSpace(p.Config.Longitude)

	lat, latOK := parseDegrees(rawLat, 90)
	lon, lonOK := parseDegrees(rawLon, 180)
	if !latOK || !lonOK {
		// Silence and rubbish are different problems. Only say something was
		// refused when something arrived.
		if rawLat != "" || rawLon != "" {
			pos.Refused = fmt.Sprintf("announced latitude %q and longitude %q, which are not "+
				"usable coordinates", rawLat, rawLon)
		}
		return pos
	}

	// Null Island. Zero is a real coordinate in the Gulf of Guinea and is
	// almost never where a hotspot is; it is what a field left at its default
	// looks like — a WPSD hotspot with DMRGateway's [Info] block disabled
	// sends exactly "0.000000" and "00.000000", which is how this was
	// confirmed rather than guessed.
	if lat == 0 && lon == 0 {
		// **The advice does not know what it is talking to.** It says "set it
		// on the hotspot", which is right for the three that send this and
		// wrong for a QSP server in a rack that correctly has no position —
		// and a linked server announced 0, 0 and was told to configure a
		// hotspot it does not have. Naming the station rather than a device it
		// might not be is the fix that does not require this code to guess.
		pos.Refused = "announced 0, 0, which is what an unconfigured position field looks " +
			"like rather than a place; set the latitude and longitude where this station " +
			"is configured, or leave them unset"
		return pos
	}

	pos.Latitude = lat
	pos.Longitude = lon
	pos.Located = true
	return pos
}

// parseDegrees reads a decimal-degree field and bounds it.
//
// The field is eight or nine characters of free text. MMDVMHost sends decimal
// degrees, but nothing obliges a peer to, so anything that does not parse to a
// plausible number is treated as absent rather than guessed at.
func parseDegrees(field string, limit float64) (float64, bool) {
	s := strings.TrimSpace(field)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	// NaN fails every comparison, so the bounds check rejects it without
	// needing to be asked about it separately. Infinity is caught the same way.
	if !(v >= -limit && v <= limit) {
		return 0, false
	}
	return v, true
}

// parseHeight reads the three-character height field, in metres.
func parseHeight(field string) int {
	s := strings.TrimSpace(field)
	if s == "" {
		return 0
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
