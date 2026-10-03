package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNoContact refuses to build a client with no contact, because NWS blocks
// anonymous clients and a request it will refuse is a request not worth
// making.
var ErrNoContact = errors.New("weather: the National Weather Service asks for a contact email, and none is set")

// ErrUnknownZone is returned when NWS does not know a code.
var ErrUnknownZone = errors.New("weather: not a code the National Weather Service knows")

// errBadRequest is NWS refusing a request as malformed. For a zone lookup
// that means a code of the right shape that NWS still does not accept, such
// as a state prefix that does not exist.
var errBadRequest = errors.New("weather: the National Weather Service refused the request as malformed")

// maxBody bounds a response. The whole active-alert list for a state is a few
// hundred kilobytes; a few megabytes is room enough and refuses anything
// pathological.
const maxBody = 4 << 20

// Client asks the NWS API.
type Client struct {
	base      string
	userAgent string
	http      *http.Client
}

// NewClient builds a client for base (DefaultBaseURL when empty) that
// identifies itself with version and contact.
func NewClient(base, version, contact string) (*Client, error) {
	if strings.TrimSpace(contact) == "" {
		return nil, ErrNoContact
	}
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{
		base: strings.TrimRight(base, "/"),
		// The same shape radioid.net is sent, so an operator who reads either
		// service's logs recognises it.
		userAgent: fmt.Sprintf("QSP/%s (+https://github.com/K9MLS/qsp; %s)", version, strings.TrimSpace(contact)),
		http:      &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// get fetches path and decodes the JSON into out. A 404 is ErrUnknownZone,
// because the only 404 this package can cause is a code NWS does not have.
func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fmt.Errorf("weather: building a request for %s: %w", path, err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/geo+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("weather: cannot reach the National Weather Service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("weather: reading %s: %w", path, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrUnknownZone
	case resp.StatusCode == http.StatusBadRequest:
		return errBadRequest
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("weather: the National Weather Service answered %s for %s", resp.Status, path)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("weather: the answer for %s is not what NWS sends: %w", path, err)
	}
	return nil
}

// zoneResponse is the part of /zones/{type}/{id} that is read.
type zoneResponse struct {
	Properties struct {
		ID       string   `json:"id"`
		Type     string   `json:"type"`
		Name     string   `json:"name"`
		State    string   `json:"state"`
		TimeZone []string `json:"timeZone"`
	} `json:"properties"`
}

// Zone looks a county or forecast-zone code up.
//
// The letter after the state says which list it is in: C is a county, Z a
// forecast zone. Asking the wrong list is a 404, so the code's shape decides.
func (c *Client) Zone(ctx context.Context, code string) (Zone, error) {
	if !ValidZoneCode(code) {
		return Zone{}, ErrUnknownZone
	}
	kind := "forecast"
	if code[2] == 'C' {
		kind = "county"
	}
	var zr zoneResponse
	if err := c.get(ctx, "/zones/"+kind+"/"+url.PathEscape(code), &zr); err != nil {
		if errors.Is(err, errBadRequest) {
			// **Unknown, not unreachable.** A code with an invalid state
			// prefix is refused with 400, and treated as "could not be
			// checked" it stayed in the alert request, which NWS then
			// refused too — silencing every good code with it.
			return Zone{}, ErrUnknownZone
		}
		return Zone{}, err
	}
	z := Zone{
		Code:  code,
		Name:  zr.Properties.Name,
		State: zr.Properties.State,
		Kind:  kind,
	}
	if len(zr.Properties.TimeZone) > 0 {
		z.TimeZone = zr.Properties.TimeZone[0]
	}
	return z, nil
}

// alertsResponse is the part of /alerts/active that is read.
type alertsResponse struct {
	Features []struct {
		Properties struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			MessageType string `json:"messageType"`
			Event       string `json:"event"`
			Severity    string `json:"severity"`
			Headline    string `json:"headline"`
			AreaDesc    string `json:"areaDesc"`
			Geocode     struct {
				UGC  []string `json:"UGC"`
				SAME []string `json:"SAME"`
			} `json:"geocode"`
			References []struct {
				Identifier string `json:"identifier"`
			} `json:"references"`
			Sent    *time.Time `json:"sent"`
			Expires *time.Time `json:"expires"`
			Ends    *time.Time `json:"ends"`
			// Parameters is read leniently, one name at a time: it is a bag
			// of whatever the issuing office attached, and a shape nobody
			// expected in a field QSP does not need must not fail the poll.
			Parameters map[string]json.RawMessage `json:"parameters"`
		} `json:"properties"`
	} `json:"features"`
}

// ActiveAlerts returns every active alert covering any of zones.
//
// One request for all of them: /alerts/active takes a comma-separated zone
// list and accepts county and forecast-zone codes together.
func (c *Client) ActiveAlerts(ctx context.Context, zones []string) ([]Alert, error) {
	if len(zones) == 0 {
		return nil, nil
	}
	var ar alertsResponse
	q := url.Values{"zone": {strings.Join(zones, ",")}}
	if err := c.get(ctx, "/alerts/active?"+q.Encode(), &ar); err != nil {
		if errors.Is(err, ErrUnknownZone) || errors.Is(err, errBadRequest) {
			return nil, fmt.Errorf("weather: the National Weather Service refused the codes %s", strings.Join(zones, ", "))
		}
		return nil, err
	}
	out := make([]Alert, 0, len(ar.Features))
	for _, f := range ar.Features {
		p := f.Properties
		a := Alert{
			ID:          p.ID,
			Status:      p.Status,
			MessageType: p.MessageType,
			Event:       p.Event,
			Severity:    p.Severity,
			Headline:    p.Headline,
			AreaDesc:    p.AreaDesc,
			UGC:         p.Geocode.UGC,
			SAME:        p.Geocode.SAME,
		}
		for _, r := range p.References {
			if r.Identifier != "" {
				a.References = append(a.References, r.Identifier)
			}
		}
		if p.Sent != nil {
			a.Sent = *p.Sent
		}
		if p.Expires != nil {
			a.Expires = *p.Expires
		}
		if p.Ends != nil {
			a.Ends = *p.Ends
		}
		a.NoSetEnd = p.Ends == nil && untilFurtherNotice(p.Parameters["VTEC"])
		if a.ID == "" {
			// An alert with no identity cannot be remembered as sent, so it
			// would be sent on every poll. Skipped rather than trusted.
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// vtecNoEnd is how a VTEC line ends when the alert is in effect until further
// notice: an end time of all zeros, where a date and time would be.
const vtecNoEnd = "-000000T0000Z/"

// untilFurtherNotice reports whether an alert's VTEC lines, as NWS gives them
// in parameters.VTEC, say it has no set end.
//
// VTEC is the coded line NWS puts on every watch, warning and advisory, such
// as /O.CON.KDVN.FL.W.0067.000000T0000Z-000000T0000Z/, and its last field is
// when the event ends. Of 396 alerts active on 2026-10-03, every one with a
// null "ends" either had this all-zero end or had no VTEC line at all, and
// every one with a real end there had the same time in "ends".
func untilFurtherNotice(raw json.RawMessage) bool {
	var lines []string
	if err := json.Unmarshal(raw, &lines); err != nil {
		// Absent, or not the list of strings NWS sends: nothing is known, and
		// the alert is read as it was before this field was.
		return false
	}
	for _, line := range lines {
		if strings.HasSuffix(strings.TrimSpace(line), vtecNoEnd) {
			return true
		}
	}
	return false
}
