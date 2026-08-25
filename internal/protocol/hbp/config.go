package hbp

import (
	"fmt"
	"strings"
)

// Wire sizes of the configuration messages, from
// testdata/hbp/hbp-login-session.pcap.
const (
	configSize        = 302 // "RPTC" + id(4) + 294 bytes of fields
	gatewayConfigSize = 119 // "DMRC" + id(4) + 111 bytes of fields
)

// Field widths. Every configuration field is fixed-width, space-padded ASCII.
//
// The two dialects share their first six fields exactly; DMRC then skips
// Latitude through URL and continues at Slots. That is the whole difference
// between them, and both layouts sum to their observed message sizes with no
// remainder, which is what confirms these boundaries rather than an assumption
// about them.
const (
	wCallsign    = 8
	wRXFreq      = 9
	wTXFreq      = 9
	wTXPower     = 2
	wColorCode   = 2
	wLatitude    = 8
	wLongitude   = 9
	wHeight      = 3
	wLocation    = 20
	wDescription = 19
	wSlots       = 1
	wURL         = 124
	wSoftwareID  = 40
	wPackageID   = 40
)

// Config is a peer's full configuration announcement. Tag "RPTC".
//
// All fields are fixed-width, space-padded ASCII on the wire; the struct holds
// them trimmed of that padding. Marshal restores the padding, so a message
// round-trips byte for byte.
//
// Frequencies are in hertz as decimal text, for example "449625000". They are
// kept as strings rather than parsed into integers because the wire format is
// text and a peer that sends something unparseable should still round-trip
// through QSP unchanged rather than being silently corrected.
type Config struct {
	RepeaterID  RepeaterID
	Callsign    string
	RXFreq      string
	TXFreq      string
	TXPower     string
	ColorCode   string
	Latitude    string
	Longitude   string
	Height      string
	Location    string
	Description string
	Slots       string
	URL         string
	SoftwareID  string
	PackageID   string
}

// Kind implements Message.
func (Config) Kind() Kind { return KindConfig }

// Marshal implements Message.
func (m Config) Marshal() []byte { return m.AppendTo(make([]byte, 0, configSize)) }

// AppendTo implements Message.
func (m Config) AppendTo(dst []byte) []byte {
	dst = append(dst, "RPTC"...)
	var id [4]byte
	putID(id[:], m.RepeaterID)
	dst = append(dst, id[:]...)

	dst = padTo(dst, m.Callsign, wCallsign)
	dst = padTo(dst, m.RXFreq, wRXFreq)
	dst = padTo(dst, m.TXFreq, wTXFreq)
	dst = padTo(dst, m.TXPower, wTXPower)
	dst = padTo(dst, m.ColorCode, wColorCode)
	dst = padTo(dst, m.Latitude, wLatitude)
	dst = padTo(dst, m.Longitude, wLongitude)
	dst = padTo(dst, m.Height, wHeight)
	dst = padTo(dst, m.Location, wLocation)
	dst = padTo(dst, m.Description, wDescription)
	dst = padTo(dst, m.Slots, wSlots)
	dst = padTo(dst, m.URL, wURL)
	dst = padTo(dst, m.SoftwareID, wSoftwareID)
	dst = padTo(dst, m.PackageID, wPackageID)
	return dst
}

func parseConfig(b []byte) (Message, error) {
	if err := exactLen(b, configSize, KindConfig); err != nil {
		return nil, err
	}
	f := fields{buf: b, off: 8}
	m := Config{
		RepeaterID:  getID(b[4:8]),
		Callsign:    f.next(wCallsign),
		RXFreq:      f.next(wRXFreq),
		TXFreq:      f.next(wTXFreq),
		TXPower:     f.next(wTXPower),
		ColorCode:   f.next(wColorCode),
		Latitude:    f.next(wLatitude),
		Longitude:   f.next(wLongitude),
		Height:      f.next(wHeight),
		Location:    f.next(wLocation),
		Description: f.next(wDescription),
		Slots:       f.next(wSlots),
		URL:         f.next(wURL),
		SoftwareID:  f.next(wSoftwareID),
		PackageID:   f.next(wPackageID),
	}
	if f.off != configSize {
		// Unreachable while the widths above are correct; the check exists so
		// that editing one of them fails loudly instead of shifting every
		// subsequent field by a byte.
		return nil, fmt.Errorf("hbp: RPTC field widths sum to %d, want %d", f.off, configSize)
	}
	return m, nil
}

// GatewayConfig is the abbreviated configuration used on a local gateway link.
// Tag "DMRC".
//
// It is identical to Config through ColorCode, then omits Latitude, Longitude,
// Height, Location, Description and URL, continuing at Slots. A gateway relaying
// to a master fills those omitted fields from its own configuration.
type GatewayConfig struct {
	RepeaterID RepeaterID
	Callsign   string
	RXFreq     string
	TXFreq     string
	TXPower    string
	ColorCode  string
	Slots      string
	SoftwareID string
	PackageID  string
}

// Kind implements Message.
func (GatewayConfig) Kind() Kind { return KindGatewayConfig }

// Marshal implements Message.
func (m GatewayConfig) Marshal() []byte { return m.AppendTo(make([]byte, 0, gatewayConfigSize)) }

// AppendTo implements Message.
func (m GatewayConfig) AppendTo(dst []byte) []byte {
	dst = append(dst, "DMRC"...)
	var id [4]byte
	putID(id[:], m.RepeaterID)
	dst = append(dst, id[:]...)

	dst = padTo(dst, m.Callsign, wCallsign)
	dst = padTo(dst, m.RXFreq, wRXFreq)
	dst = padTo(dst, m.TXFreq, wTXFreq)
	dst = padTo(dst, m.TXPower, wTXPower)
	dst = padTo(dst, m.ColorCode, wColorCode)
	dst = padTo(dst, m.Slots, wSlots)
	dst = padTo(dst, m.SoftwareID, wSoftwareID)
	dst = padTo(dst, m.PackageID, wPackageID)
	return dst
}

func parseGatewayConfig(b []byte) (Message, error) {
	if err := exactLen(b, gatewayConfigSize, KindGatewayConfig); err != nil {
		return nil, err
	}
	f := fields{buf: b, off: 8}
	m := GatewayConfig{
		RepeaterID: getID(b[4:8]),
		Callsign:   f.next(wCallsign),
		RXFreq:     f.next(wRXFreq),
		TXFreq:     f.next(wTXFreq),
		TXPower:    f.next(wTXPower),
		ColorCode:  f.next(wColorCode),
		Slots:      f.next(wSlots),
		SoftwareID: f.next(wSoftwareID),
		PackageID:  f.next(wPackageID),
	}
	if f.off != gatewayConfigSize {
		return nil, fmt.Errorf("hbp: DMRC field widths sum to %d, want %d", f.off, gatewayConfigSize)
	}
	return m, nil
}

// fields walks fixed-width text fields.
type fields struct {
	buf []byte
	off int
}

// next returns the next n bytes with trailing spaces removed.
//
// It is only ever called after a length check, so the slice bounds are safe.
func (f *fields) next(n int) string {
	s := string(f.buf[f.off : f.off+n])
	f.off += n
	return strings.TrimRight(s, " ")
}

// padTo appends s truncated or space-padded to exactly n bytes.
//
// Truncation is silent because the wire format has no way to express a longer
// value; a peer sending an over-long field would have truncated it too.
// Validation of QSP's own configuration happens in internal/config, before a
// value ever reaches here.
func padTo(dst []byte, s string, n int) []byte {
	if len(s) > n {
		return append(dst, s[:n]...)
	}
	dst = append(dst, s...)
	for i := len(s); i < n; i++ {
		dst = append(dst, ' ')
	}
	return dst
}
