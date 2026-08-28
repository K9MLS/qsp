package peers

import (
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

	lat, latOK := parseDegrees(p.Config.Latitude, 90)
	lon, lonOK := parseDegrees(p.Config.Longitude, 180)
	if !latOK || !lonOK {
		return pos
	}

	// Null Island. Zero is a real coordinate in the Gulf of Guinea and is
	// almost never where a hotspot is; it is what a field left at its default
	// looks like. Refusing it costs one station in the Atlantic the pin they
	// were never going to have, and saves every unconfigured hotspot from
	// claiming to be there.
	if lat == 0 && lon == 0 {
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
