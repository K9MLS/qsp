package callsigns

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Endpoint is the registry QSP asks.
//
// A constant rather than configuration: this is the amateur DMR registry, and
// an operator who wants a different one wants something QSP has not been asked
// for. Making it configurable would also make QSP a general-purpose client for
// whatever somebody points it at, which is a larger promise than resolving
// radio IDs.
const Endpoint = "https://radioid.net/api/dmr/user/"

// HTTPFetcher asks the registry over HTTP.
//
// **It identifies itself.** Their data use policy asks automated clients to
// carry a clear User-Agent and a contact address, and the contact comes from
// the operator because it is the operator making the requests.
type HTTPFetcher struct {
	endpoint  string
	client    *http.Client
	userAgent string
}

// NewHTTPFetcher constructs a fetcher pointed at the registry.
func NewHTTPFetcher(version, contact string) (*HTTPFetcher, error) {
	return NewHTTPFetcherAt(Endpoint, version, contact)
}

// NewHTTPFetcherAt constructs a fetcher pointed somewhere specific.
//
// **This exists for tests, not for configuration.** The endpoint is a constant
// so that QSP is a client of the amateur DMR registry rather than a general
// client of whatever somebody points it at, and a test needs to answer for it
// without reaching the real one.
func NewHTTPFetcherAt(endpoint, version, contact string) (*HTTPFetcher, error) {
	if strings.TrimSpace(contact) == "" {
		return nil, ErrNoContact
	}
	return &HTTPFetcher{
		endpoint: endpoint,
		// A short timeout, because nothing waits on this and a slow registry
		// should cost a name rather than a goroutine that lingers.
		client:    &http.Client{Timeout: 10 * time.Second},
		userAgent: UserAgent(version, contact),
	}, nil
}

// registryResponse is the shape the registry returns.
//
// Only the fields QSP uses are named. The registry holds more, and taking only
// what is displayed keeps the cache from becoming a copy of somebody else's
// database — which is the thing their policy puts behind approval.
type registryResponse struct {
	Count   int `json:"count"`
	Results []struct {
		ID       int    `json:"id"`
		Callsign string `json:"callsign"`
		FName    string `json:"fname"`
		Surname  string `json:"surname"`
		Country  string `json:"country"`
	} `json:"results"`
}

// Fetch implements Fetcher.
func (f *HTTPFetcher) Fetch(id uint32) (Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		f.endpoint+"?id="+strconv.FormatUint(uint64(id), 10), nil)
	if err != nil {
		return Entry{}, err
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/json")

	res, err := f.client.Do(req)
	if err != nil {
		return Entry{}, fmt.Errorf("callsigns: cannot reach the registry: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	// **A rate-limit answer is an error, not an absence.** Their policy says
	// they may rate-limit or block at any time, and recording that as "this ID
	// does not exist" would cache the refusal as a fact about the operator.
	if res.StatusCode == http.StatusTooManyRequests {
		return Entry{}, fmt.Errorf("callsigns: the registry is rate-limiting this instance; "+
			"lookups will be retried later (HTTP %d)", res.StatusCode)
	}
	if res.StatusCode == http.StatusNotFound {
		// The registry says it has no such ID, which is an answer worth
		// remembering rather than a failure.
		return Entry{Known: false}, nil
	}
	if res.StatusCode != http.StatusOK {
		return Entry{}, fmt.Errorf("callsigns: the registry answered HTTP %d", res.StatusCode)
	}

	var body registryResponse
	// Bounded, because a response far larger than one record is not one record
	// and reading it costs memory for nothing.
	if err := json.NewDecoder(&limitedReader{r: res.Body, n: 1 << 20}).Decode(&body); err != nil {
		return Entry{}, fmt.Errorf("callsigns: the registry's answer did not parse: %w", err)
	}

	if len(body.Results) == 0 {
		return Entry{Known: false}, nil
	}

	// The first result. A query by ID returns one record, and taking more than
	// the first would be inventing a policy for a case the registry does not
	// produce.
	r := body.Results[0]
	name := strings.TrimSpace(r.FName)
	if name == "" {
		name = strings.TrimSpace(r.Surname)
	}
	return Entry{
		Callsign: strings.TrimSpace(r.Callsign),
		Name:     name,
		Country:  strings.TrimSpace(r.Country),
		Known:    true,
	}, nil
}

// limitedReader caps a response body.
type limitedReader struct {
	r interface{ Read([]byte) (int, error) }
	n int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errors.New("callsigns: the registry's answer is larger than one record")
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}

// IsTemporary reports whether a fetch failure is worth retrying.
//
// A timeout or a refused connection is the registry or the network having a
// moment. Anything else is more likely to be QSP asking wrongly, and retrying
// that forever is how a client becomes the abusive one their policy describes.
func IsTemporary(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return strings.Contains(err.Error(), "rate-limiting")
}

// SQLStore keeps resolved entries in the database.
type SQLStore struct{ db *sql.DB }

// NewSQLStore wraps a database handle.
func NewSQLStore(db *sql.DB) (*SQLStore, error) {
	if db == nil {
		return nil, errors.New("callsigns: a database handle is required")
	}
	return &SQLStore{db: db}, nil
}

// storeLayout is how instants are held: RFC 3339 in UTC, text that sorts
// chronologically, as the other tables use.
const storeLayout = time.RFC3339Nano

// Load implements Store.
func (s *SQLStore) Load() ([]Entry, error) {
	const q = `SELECT id, callsign, name, country, known, fetched_at FROM callsigns`

	rows, err := s.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("callsigns: cannot read the cache: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Entry
	for rows.Next() {
		var (
			e       Entry
			known   int
			fetched string
		)
		if err := rows.Scan(&e.ID, &e.Callsign, &e.Name, &e.Country, &known, &fetched); err != nil {
			return nil, fmt.Errorf("callsigns: cannot read a cached entry: %w", err)
		}
		e.Known = known != 0
		if t, err := time.Parse(storeLayout, fetched); err == nil {
			e.FetchedAt = t.UTC()
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("callsigns: cannot read the cache: %w", err)
	}
	return out, nil
}

// Save implements Store.
//
// Replaces rather than inserts, so re-checking an absence that has since become
// a registration updates it in place.
func (s *SQLStore) Save(e Entry) error {
	const q = `
		INSERT INTO callsigns (id, callsign, name, country, known, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			callsign = excluded.callsign,
			name = excluded.name,
			country = excluded.country,
			known = excluded.known,
			fetched_at = excluded.fetched_at`

	known := 0
	if e.Known {
		known = 1
	}
	if _, err := s.db.Exec(q, e.ID, e.Callsign, e.Name, e.Country, known,
		e.FetchedAt.UTC().Format(storeLayout)); err != nil {
		return fmt.Errorf("callsigns: cannot cache an entry: %w", err)
	}
	return nil
}
