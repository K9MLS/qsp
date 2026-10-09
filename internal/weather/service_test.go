package weather

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeNWS serves /zones and /alerts/active in the shapes api.weather.gov
// uses.
//
// **Constructed, not captured.** When these were written the development
// container could not reach api.weather.gov (ADR-0068 measured a 403), so they are
// built from the fields ADR-0068 recorded from the live API on 2026-09-27 and
// from the published API. What this version exists to do is show the real
// feed on production in Preview before anything transmits, which is the check
// these cannot be.
type fakeNWS struct {
	t        *testing.T
	mu       sync.Mutex
	zones    map[string]Zone
	alerts   []map[string]any
	fail     bool
	requests atomic.Int32
	asked    []string
	agents   []string
	// onAlerts runs as an alert request arrives, before it is answered, to
	// change things while a poll is out at NWS.
	onAlerts func()
	// badRequest are codes answered 400, as NWS answers an invalid prefix.
	badRequest map[string]bool
	// lists are the requests for a state's list, as asked; refuseParams
	// answers a list request carrying include_geometry with a 400, as NWS
	// answers a parameter it does not know.
	lists        []string
	refuseParams bool
}

func (f *fakeNWS) setAlerts(a ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alerts = a
}

func (f *fakeNWS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if r.URL.Path == "/alerts/active" {
		f.mu.Lock()
		hook := f.onAlerts
		f.onAlerts = nil
		f.mu.Unlock()
		if hook != nil {
			hook()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents = append(f.agents, r.UserAgent())
	if f.fail {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/zones/"):
		parts := strings.Split(r.URL.Path, "/")
		code := parts[len(parts)-1]
		if f.badRequest[code] {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"title":"Invalid Parameter","status":400}`)
			return
		}
		z, ok := f.zones[code]
		if !ok || (parts[2] == "county") != (z.Kind == "county") {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"title":"Not Found","status":404}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "Feature",
			"properties": map[string]any{
				"id": z.Code, "type": z.Kind, "name": z.Name, "state": z.State,
				"timeZone": []string{z.TimeZone},
			},
		})
	case r.URL.Path == "/zones":
		f.lists = append(f.lists, r.URL.RawQuery)
		q := r.URL.Query()
		if f.refuseParams && q.Has("include_geometry") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"title":"Invalid Parameter","status":400}`)
			return
		}
		features := []map[string]any{}
		for _, z := range f.zones {
			if z.State == q.Get("area") && z.Kind == q.Get("type") {
				features = append(features, map[string]any{"type": "Feature", "geometry": nil,
					"properties": map[string]any{"id": z.Code, "type": z.Kind, "name": z.Name,
						"state": z.State, "timeZone": []string{z.TimeZone}}})
			}
		}
		w.Header().Set("Content-Type", "application/geo+json")
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "FeatureCollection", "features": features})
	case r.URL.Path == "/alerts/active":
		f.asked = append(f.asked, r.URL.Query().Get("zone"))
		features := make([]map[string]any, 0, len(f.alerts))
		for _, a := range f.alerts {
			features = append(features, map[string]any{"type": "Feature", "properties": a})
		}
		w.Header().Set("Content-Type", "application/geo+json")
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "FeatureCollection", "features": features})
	default:
		f.t.Errorf("unexpected request %s", r.URL)
		http.NotFound(w, r)
	}
}

func newFakeNWS(t *testing.T) (*fakeNWS, *httptest.Server) {
	t.Helper()
	f := &fakeNWS{t: t, zones: map[string]Zone{
		"TXC121": {Code: "TXC121", Name: "Denton", State: "TX", Kind: "county", TimeZone: "America/Chicago"},
		"TXZ103": {Code: "TXZ103", Name: "Denton", State: "TX", Kind: "forecast", TimeZone: "America/Chicago"},
	}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

// alertJSON is one alert's properties as NWS sends them for a warning with a
// set end.
//
// **"ends" is the same time as "expires", not null.** Read from the live feed
// on 2026-10-03, every tornado, severe thunderstorm and flash flood warning
// carried "ends", and on a new one it equalled "expires". Null is what an
// alert with no set end or no VTEC line has; noSetEnd makes one of those.
func alertJSON(id, event, status string, ugc []string, sent, expires time.Time, refs ...string) map[string]any {
	references := make([]map[string]any, 0, len(refs))
	for _, r := range refs {
		references = append(references, map[string]any{"identifier": r, "sender": "w-nws.webmaster@noaa.gov"})
	}
	return map[string]any{
		"id": id, "status": status, "messageType": "Alert", "event": event,
		"severity": "Extreme", "certainty": "Observed", "urgency": "Immediate",
		"headline":   event + " issued by NWS Fort Worth TX",
		"areaDesc":   "Denton, TX",
		"geocode":    map[string]any{"SAME": sameFor(ugc), "UGC": ugc},
		"references": references,
		"sent":       sent.Format(time.RFC3339), "effective": sent.Format(time.RFC3339),
		"expires": expires.Format(time.RFC3339), "ends": expires.Format(time.RFC3339),
	}
}

// noSetEnd turns a fixture into an alert in effect until further notice, in
// the shape of the Flood Warning KDVN reissued on 2026-10-03: "ends" null, an
// all-zero end in its VTEC line, and "expires" only when the next reissue is
// due.
func noSetEnd(a map[string]any) map[string]any {
	a["ends"] = nil
	a["parameters"] = map[string]any{"VTEC": []string{"/O.CON.KDVN.FL.W.0067.000000T0000Z-000000T0000Z/"}}
	return a
}

// sameFor gives the SAME codes NWS would list for the counties among ugc, so
// a fixture for one county never matches another through them.
func sameFor(ugc []string) []string {
	out := []string{}
	for _, u := range ugc {
		if len(u) == 6 && u[2] == 'C' {
			out = append(out, "0"+stateFIPS[u[:2]]+u[3:])
		}
	}
	return out
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.now = c.now.Add(d) }

func newService(t *testing.T, srv *httptest.Server, c *clock) *Service {
	t.Helper()
	return New(Options{
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "0.1.292", BaseURL: srv.URL, Now: c.Now,
	})
}

func denton() Settings {
	return Settings{
		Enabled: true, Zones: []string{"TXC121", "TXZ103"}, Events: DefaultEvents,
		Talkgroup: 2, Timeslot: 2, SenderID: 9990, Contact: "k9mls@example.org",
	}
}

func verdicts(st Status) map[string]string {
	out := map[string]string{}
	for _, a := range st.Active {
		out[a.ID] = string(a.Verdict) + ":" + a.Reason
	}
	return out
}

// **The restart must not blast** (ADR-0068), and a new warning after that is
// sent exactly once, and its update not at all.
//
// To see it fail: remove the `!s.baselined` demotion in decide, and the watch
// that was already in effect is sent on the first poll; drop `s.decided`
// reuse, and the new warning is decided again on the third poll, which the
// recent list shows as a second entry.
func TestTheFirstPollSendsNothingAndLaterOnesSendOnce(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)
	s.Apply(denton())
	ctx := context.Background()

	watch := alertJSON("urn:watch", "Tornado Watch", "Actual", []string{"TXC121"}, c.Now().Add(-2*time.Hour), c.Now().Add(4*time.Hour))
	f.setAlerts(watch)
	s.Poll(ctx)
	if got := verdicts(s.Status())["urn:watch"]; got != "hold:"+ReasonBaseline {
		t.Fatalf("a watch in effect before QSP looked was %q, want held as the baseline", got)
	}

	c.Advance(time.Minute)
	warning := alertJSON("urn:warn", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(30*time.Minute))
	f.setAlerts(watch, warning)
	s.Poll(ctx)
	st := s.Status()
	if got := verdicts(st)["urn:warn"]; got != "send:" {
		t.Fatalf("a new tornado warning was %q, want sent", got)
	}
	var text string
	for _, a := range st.Active {
		if a.ID == "urn:warn" {
			text = a.Text
		}
	}
	if text != "TORNADO WARNING Denton until 7:31PM CDT" {
		t.Errorf("it would read %q on a radio", text)
	}

	c.Advance(time.Minute)
	s.Poll(ctx)
	if n := countRecent(s.Status(), "urn:warn"); n != 1 {
		t.Errorf("the warning was decided %d times across two polls, want once", n)
	}

	c.Advance(time.Minute)
	update := alertJSON("urn:warn2", "Tornado Warning", "Actual", []string{"TXC121"}, c.Now(), c.Now().Add(30*time.Minute), "urn:warn")
	f.setAlerts(watch, update)
	s.Poll(ctx)
	if got := verdicts(s.Status())["urn:warn2"]; got != "hold:"+ReasonUpdate {
		t.Errorf("an update to the warning was %q, want held as an update", got)
	}
}

func countRecent(st Status, id string) int {
	n := 0
	for _, a := range st.Recent {
		if a.ID == id {
			n++
		}
	}
	return n
}

// Adding a county sends the warning already running there: the operator just
// asked for that county, and silence until the next new alert is what they
// would least expect.
//
// To see it fail: remove the zones comparison from Apply, and the warning
// stays held as not for the areas chosen.
func TestAddingACountySendsWhatIsRunningThere(t *testing.T) {
	f, srv := newFakeNWS(t)
	f.zones["TXC085"] = Zone{Code: "TXC085", Name: "Collin", State: "TX", Kind: "county", TimeZone: "America/Chicago"}
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)
	s.Apply(denton())
	ctx := context.Background()
	s.Poll(ctx) // baseline, nothing active

	collin := alertJSON("urn:collin", "Tornado Warning", "Actual", []string{"TXC085"}, c.Now(), c.Now().Add(time.Hour))
	f.setAlerts(collin)
	c.Advance(time.Minute)
	s.Poll(ctx)
	if got := verdicts(s.Status())["urn:collin"]; got != "hold:"+ReasonNotHere {
		t.Fatalf("a Collin warning with only Denton chosen was %q", got)
	}

	more := denton()
	more.Zones = append(more.Zones, "TXC085")
	s.Apply(more)
	c.Advance(time.Minute)
	s.Poll(ctx)
	if got := verdicts(s.Status())["urn:collin"]; got != "send:" {
		t.Errorf("after adding Collin its running warning was %q, want sent", got)
	}
}

// An unknown code is reported by name and left out of the alert request,
// rather than failing it for every code.
//
// To see it fail: ask for set.Zones instead of ask in Poll, and the request
// carries TXZ999.
func TestAnUnknownCodeIsReportedAndLeftOut(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)
	set := denton()
	set.Zones = []string{"TXC121", "TXZ999"}
	s.Apply(set)
	s.Poll(context.Background())

	st := s.Status()
	byCode := map[string]ZoneCheck{}
	for _, z := range st.Zones {
		byCode[z.Code] = z
	}
	if z := byCode["TXC121"]; !z.OK || z.Zone == nil || z.Zone.Name != "Denton" {
		t.Errorf("TXC121 reported as %+v, want Denton", z)
	}
	if z := byCode["TXZ999"]; z.OK || !strings.Contains(z.Problem, "does not know") {
		t.Errorf("TXZ999 reported as %+v, want a problem saying NWS does not know it", z)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.asked) != 1 || f.asked[0] != "TXC121" {
		t.Errorf("alerts were requested for %v, want only TXC121", f.asked)
	}
	if st.LastError != "" {
		t.Errorf("one bad code failed the poll: %s", st.LastError)
	}
}

// Off means off: not one request.
//
// To see it fail: remove the Enabled check at the top of Poll.
func TestNothingIsFetchedWhileOff(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)
	set := denton()
	set.Enabled = false
	s.Apply(set)
	s.Poll(context.Background())
	if n := f.requests.Load(); n != 0 {
		t.Errorf("%d requests were made while weather alerts were off", n)
	}
	if st := s.Status(); st.Enabled || st.Mode != ModePreview {
		t.Errorf("status %+v", st)
	}
}

// A failure is shown on the page and cleared by the next success.
func TestAnUnreachableServiceIsReportedAndRecovers(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)
	s.Apply(denton())

	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	s.Poll(context.Background())
	if st := s.Status(); st.LastError == "" || !st.LastSuccess.IsZero() {
		t.Fatalf("a failed poll reported error %q, last success %v", st.LastError, st.LastSuccess)
	}

	f.mu.Lock()
	f.fail = false
	f.mu.Unlock()
	c.Advance(time.Minute)
	s.Poll(context.Background())
	if st := s.Status(); st.LastError != "" || st.LastSuccess.IsZero() {
		t.Errorf("after recovery: error %q, last success %v", st.LastError, st.LastSuccess)
	}
}

// NWS refuses anonymous clients, so every request names QSP and the contact.
//
// To see it fail: drop the User-Agent header in Client.get.
func TestEveryRequestCarriesTheContact(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)
	s.Apply(denton())
	s.Poll(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.agents) == 0 {
		t.Fatal("no requests were made")
	}
	for _, ua := range f.agents {
		if !strings.Contains(ua, "QSP/0.1.292") || !strings.Contains(ua, "k9mls@example.org") {
			t.Errorf("User-Agent %q does not name QSP and the contact", ua)
		}
	}
	if _, err := NewClient("", "0.1.292", " "); err != ErrNoContact {
		t.Errorf("a client with no contact: %v, want ErrNoContact", err)
	}
}

// The page's "Check codes" answers every code in words.
func TestCheckZonesSaysWhatEachCodeIs(t *testing.T) {
	_, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)

	checks := s.CheckZones(context.Background(), []string{"txc121, TXZ103, TXZ999, Denton"}, "k9mls@example.org")
	want := []struct {
		code string
		ok   bool
		says string
	}{
		{"TXC121", true, ""},
		{"TXZ103", true, ""},
		{"TXZ999", false, "does not know"},
		{"DENTON", false, "not the shape"},
	}
	if len(checks) != len(want) {
		t.Fatalf("got %d checks, want %d: %+v", len(checks), len(want), checks)
	}
	for i, w := range want {
		got := checks[i]
		if got.Code != w.code || got.OK != w.ok || !strings.Contains(got.Problem, w.says) {
			t.Errorf("check %d = %+v, want %s ok=%v problem containing %q", i, got, w.code, w.ok, w.says)
		}
	}

	none := s.CheckZones(context.Background(), []string{"TXC121"}, "")
	if none[0].OK || !strings.Contains(none[0].Problem, "contact email") {
		t.Errorf("with no contact anywhere: %+v", none[0])
	}
}

// Run polls at once when turned on, not a minute later.
func TestRunPollsWhenTurnedOn(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := New(Options{
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "0.1.292", BaseURL: srv.URL, Now: c.Now, Interval: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	s.Apply(denton())

	deadline := time.Now().Add(2 * time.Second)
	for s.Status().LastSuccess.IsZero() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if s.Status().LastSuccess.IsZero() {
		t.Errorf("turning weather on did not poll within 2s (requests: %d)", f.requests.Load())
	}
}

// A save while a poll is out at NWS must not let that poll decide for the new
// area. Its answer was worked out under the old one, where a warning in the
// county just added is "not for the areas you chose", and kept, it would stay
// held for as long as the warning ran.
//
// To see it fail: remove the generation check at the top of decide.
func TestASaveDuringAPollIsDecidedUnderTheNewSettings(t *testing.T) {
	f, srv := newFakeNWS(t)
	f.zones["TXC085"] = Zone{Code: "TXC085", Name: "Collin", State: "TX", Kind: "county", TimeZone: "America/Chicago"}
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)
	s.Apply(denton())
	ctx := context.Background()
	s.Poll(ctx)

	collin := alertJSON("urn:collin", "Tornado Warning", "Actual", []string{"TXC085"}, c.Now(), c.Now().Add(time.Hour))
	f.setAlerts(collin)
	more := denton()
	more.Zones = append(more.Zones, "TXC085")
	f.mu.Lock()
	f.onAlerts = func() { s.Apply(more) } // saved while this poll is at NWS
	f.mu.Unlock()
	c.Advance(time.Minute)
	s.Poll(ctx) // under the old area; its result must be discarded

	c.Advance(time.Minute)
	s.Poll(ctx)
	if got := verdicts(s.Status())["urn:collin"]; got != "send:" {
		t.Errorf("a warning already running in the county just added was %q, want sent", got)
	}
}

// NWS answers 400 for a code with a state prefix that does not exist. That is
// an unknown code, left out of the alert request, not a failure that keeps it
// in and silences every good code with it.
//
// To see it fail: remove the errBadRequest mapping in Client.Zone.
func TestAnInvalidPrefixIsAnUnknownCode(t *testing.T) {
	f, srv := newFakeNWS(t)
	f.badRequest = map[string]bool{"TKC121": true}
	c := &clock{now: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)
	set := denton()
	set.Zones = []string{"TXC121", "TKC121"}
	s.Apply(set)
	s.Poll(context.Background())

	f.mu.Lock()
	asked := slices.Clone(f.asked)
	f.mu.Unlock()
	if len(asked) != 1 || asked[0] != "TXC121" {
		t.Errorf("alerts were requested for %v, want only TXC121", asked)
	}
	for _, z := range s.Status().Zones {
		if z.Code == "TKC121" && (z.OK || z.Problem != problemUnknown) {
			t.Errorf("TKC121 reported as %+v", z)
		}
	}
	// And it is not asked about again every minute.
	before := f.requests.Load()
	c.Advance(time.Minute)
	s.Poll(context.Background())
	if n := f.requests.Load() - before; n != 1 {
		t.Errorf("the second poll made %d requests, want only the alert request", n)
	}
}

// TestAStatesCodesAreListedByName. alerts.weather.gov, where the Weather
// page sent operators to find their codes, was retired by NWS in December
// 2025. The list now comes from the API QSP already reads alerts from:
// counties first, then forecast zones, each by name, and only the shapes an
// alert request takes.
//
// To see it fail: drop either kind from Client.ZonesIn, or the
// ValidZoneCode filter in zoneList.
func TestAStatesCodesAreListedByName(t *testing.T) {
	f, srv := newFakeNWS(t)
	f.zones["TXC085"] = Zone{Code: "TXC085", Name: "Collin", State: "TX", Kind: "county"}
	f.zones["TXZ104"] = Zone{Code: "TXZ104", Name: "Collin", State: "TX", Kind: "forecast"}
	f.zones["GMZ250"] = Zone{Code: "GMZ250", Name: "Coastal waters", State: "TX", Kind: "forecast"}
	f.zones["OKC001"] = Zone{Code: "OKC001", Name: "Adair", State: "OK", Kind: "county"}
	f.zones["TXF999"] = Zone{Code: "TXF999", Name: "Fire zone", State: "TX", Kind: "forecast"}
	c := &clock{now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)

	got, err := s.ZonesIn(context.Background(), "tx", "k9mls@example.org")
	if err != nil {
		t.Fatalf("ZonesIn: %v", err)
	}
	var codes []string
	for _, z := range got {
		codes = append(codes, z.Code)
	}
	want := []string{"TXC085", "TXC121", "GMZ250", "TXZ104", "TXZ103"}
	if !slices.Equal(codes, want) {
		t.Errorf("Texas lists %v, want %v: counties then zones, by name, no fire zone", codes, want)
	}
	if got[1].Name != "Denton" || got[1].Kind != "county" {
		t.Errorf("TXC121 is listed as %+v", got[1])
	}
}

// TestAStatesListIsAskedForOnceADay. Five hundred names, which NWS changes a
// few times a year, are not fetched each time the picker is opened.
//
// To see it fail: remove the cache test from Service.ZonesIn.
func TestAStatesListIsAskedForOnceADay(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)
	ask := func() {
		t.Helper()
		if _, err := s.ZonesIn(context.Background(), "TX", "k9mls@example.org"); err != nil {
			t.Fatalf("ZonesIn: %v", err)
		}
	}
	ask()
	ask()
	c.Advance(23 * time.Hour)
	ask()
	if n := len(f.lists); n != 2 {
		t.Fatalf("NWS was asked %d times within a day, want twice (counties and zones, once)", n)
	}
	c.Advance(2 * time.Hour)
	ask()
	if n := len(f.lists); n != 4 {
		t.Errorf("after a day the list was not asked for again: %d requests", n)
	}
}

// TestAListStillComesIfNWSRefusesAParameter. Geometry is asked to be left
// out, which spares megabytes; NWS answers a parameter it does not know with
// a 400, and the list is asked for again without it.
//
// To see it fail: remove the retry in zoneList.
func TestAListStillComesIfNWSRefusesAParameter(t *testing.T) {
	f, srv := newFakeNWS(t)
	f.refuseParams = true
	c := &clock{now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)
	got, err := s.ZonesIn(context.Background(), "TX", "k9mls@example.org")
	if err != nil || len(got) != 2 {
		t.Fatalf("ZonesIn: %v, %+v", err, got)
	}
}

// TestAListNeedsAStateAndAContact. Nothing is asked of NWS with neither.
func TestAListNeedsAStateAndAContact(t *testing.T) {
	f, srv := newFakeNWS(t)
	c := &clock{now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	s := newService(t, srv, c)
	for _, state := range []string{"", "T", "TEX", "T1", "../zones"} {
		if _, err := s.ZonesIn(context.Background(), state, "k9mls@example.org"); err == nil {
			t.Errorf("%q was taken for a state", state)
		}
	}
	if _, err := s.ZonesIn(context.Background(), "TX", ""); !errors.Is(err, ErrNoContact) {
		t.Errorf("with no contact anywhere: %v", err)
	}
	if len(f.lists) != 0 {
		t.Errorf("NWS was asked %v", f.lists)
	}
}
