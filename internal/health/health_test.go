package health

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fixedClock is safe for concurrent use because Registry.Run evaluates checks
// in parallel and calls the injected clock from each goroutine.
func fixedClock() func() time.Time {
	var n atomic.Int64
	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		return base.Add(time.Duration(n.Add(1)) * time.Millisecond)
	}
}

func stub(name string, r Result) Checker {
	return CheckerFunc{CheckName: name, Fn: func(context.Context) Result { return r }}
}

func TestRegisterRejectsDuplicates(t *testing.T) {
	r := NewRegistry(Options{})
	if err := r.Register(stub("database", Healthy("ok"))); err != nil {
		t.Fatalf("first register: %v", err)
	}
	err := r.Register(stub("database", Healthy("ok")))
	if !errors.Is(err, ErrDuplicateCheck) {
		t.Errorf("got %v, want ErrDuplicateCheck", err)
	}
}

func TestRegisterRejectsNilAndUnnamed(t *testing.T) {
	r := NewRegistry(Options{})
	if err := r.Register(nil); err == nil {
		t.Error("registered a nil check")
	}
	if err := r.Register(stub("", Healthy("ok"))); err == nil {
		t.Error("registered an unnamed check")
	}
}

func TestEmptyRegistryReportsUnavailable(t *testing.T) {
	r := NewRegistry(Options{Clock: fixedClock()})
	rep := r.Run(context.Background())
	if rep.Status != StatusUnavailable {
		t.Errorf("Status = %q, want %q", rep.Status, StatusUnavailable)
	}
	if !rep.Ready() {
		t.Error("an empty registry should not block readiness")
	}
}

func TestUnavailableDoesNotDegradeOverallStatus(t *testing.T) {
	// The central honesty property: an unbuilt subsystem must not make a
	// working system look sick, and must not be hidden either.
	r := NewRegistry(Options{Clock: fixedClock()})
	r.MustRegister(stub("database", Healthy("connected")))
	r.MustRegister(stub("vocoder", Unavailable("not implemented until phase 5")))

	rep := r.Run(context.Background())
	if rep.Status != StatusHealthy {
		t.Errorf("Status = %q, want %q", rep.Status, StatusHealthy)
	}
	if len(rep.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(rep.Results))
	}
	var found bool
	for _, res := range rep.Results {
		if res.Name == "vocoder" {
			found = true
			if res.Status != StatusUnavailable {
				t.Errorf("vocoder status = %q, want %q", res.Status, StatusUnavailable)
			}
		}
	}
	if !found {
		t.Error("the unavailable subsystem was omitted from the report")
	}
}

func TestAggregationTakesMostSevere(t *testing.T) {
	cases := []struct {
		name     string
		statuses []Status
		want     Status
	}{
		{"healthy only", []Status{StatusHealthy, StatusHealthy}, StatusHealthy},
		{"degraded wins over healthy", []Status{StatusHealthy, StatusDegraded}, StatusDegraded},
		{"failing wins over degraded", []Status{StatusDegraded, StatusFailing}, StatusFailing},
		{"unavailable loses to healthy", []Status{StatusUnavailable, StatusHealthy}, StatusHealthy},
		{"unavailable only", []Status{StatusUnavailable}, StatusUnavailable},
		{"failing wins over everything", []Status{StatusUnavailable, StatusHealthy, StatusDegraded, StatusFailing}, StatusFailing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRegistry(Options{Clock: fixedClock()})
			for i, s := range c.statuses {
				r.MustRegister(CheckerFunc{
					CheckName: string(rune('a'+i)) + "check",
					Fn:        func(context.Context) Result { return Result{Status: s, Summary: "x"} },
				})
			}
			if got := r.Run(context.Background()).Status; got != c.want {
				t.Errorf("Status = %q, want %q", got, c.want)
			}
		})
	}
}

func TestReadyIsFalseOnlyWhenFailing(t *testing.T) {
	for _, s := range []Status{StatusHealthy, StatusDegraded, StatusUnavailable} {
		if !(Report{Status: s}).Ready() {
			t.Errorf("Ready() = false for %q, want true", s)
		}
	}
	if (Report{Status: StatusFailing}).Ready() {
		t.Error("Ready() = true for failing")
	}
}

func TestResultsAreSortedByName(t *testing.T) {
	r := NewRegistry(Options{Clock: fixedClock()})
	for _, n := range []string{"network", "clock", "database", "disk"} {
		r.MustRegister(stub(n, Healthy("ok")))
	}
	rep := r.Run(context.Background())
	want := []string{"clock", "database", "disk", "network"}
	for i, res := range rep.Results {
		if res.Name != want[i] {
			t.Errorf("result %d is %q, want %q", i, res.Name, want[i])
		}
	}
}

func TestSlowCheckIsReportedFailingNotAllowedToStall(t *testing.T) {
	r := NewRegistry(Options{Timeout: 30 * time.Millisecond})
	r.MustRegister(CheckerFunc{
		CheckName: "wedged",
		Fn: func(ctx context.Context) Result {
			<-ctx.Done()
			time.Sleep(2 * time.Second)
			return Healthy("never reached")
		},
	})
	r.MustRegister(stub("fast", Healthy("ok")))

	start := time.Now()
	rep := r.Run(context.Background())
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("Run took %s; a wedged check stalled the report", elapsed)
	}
	for _, res := range rep.Results {
		if res.Name == "wedged" && res.Status != StatusFailing {
			t.Errorf("wedged check reported %q, want %q", res.Status, StatusFailing)
		}
	}
}

func TestPanickingCheckIsContained(t *testing.T) {
	r := NewRegistry(Options{Clock: fixedClock()})
	r.MustRegister(CheckerFunc{
		CheckName: "explodes",
		Fn:        func(context.Context) Result { panic("boom") },
	})
	r.MustRegister(stub("fine", Healthy("ok")))

	rep := r.Run(context.Background())
	if len(rep.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(rep.Results))
	}
	for _, res := range rep.Results {
		if res.Name == "explodes" {
			if res.Status != StatusFailing {
				t.Errorf("panicking check reported %q, want %q", res.Status, StatusFailing)
			}
			if res.Fix == "" {
				t.Error("panicking check produced no operator guidance")
			}
		}
	}
}

func TestUnrecognisedStatusBecomesFailing(t *testing.T) {
	r := NewRegistry(Options{Clock: fixedClock()})
	r.MustRegister(CheckerFunc{
		CheckName: "confused",
		Fn:        func(context.Context) Result { return Result{Status: Status("fine, probably")} },
	})
	rep := r.Run(context.Background())
	if rep.Results[0].Status != StatusFailing {
		t.Errorf("got %q, want %q", rep.Results[0].Status, StatusFailing)
	}
}

func TestNonHealthyResultsCarryFixGuidance(t *testing.T) {
	// Constitution §12 applied to health: say what to do, not merely that
	// something is wrong.
	for _, res := range []Result{
		Degraded("replication lagging", "check network to the peer"),
		Failing("cannot open database", "verify the path is writable"),
	} {
		if res.Fix == "" {
			t.Errorf("%q result has no fix guidance", res.Status)
		}
	}
}

func TestRunPopulatesTimingMetadata(t *testing.T) {
	r := NewRegistry(Options{Clock: fixedClock()})
	r.MustRegister(stub("database", Healthy("ok")))
	rep := r.Run(context.Background())

	res := rep.Results[0]
	if res.CheckedAt.IsZero() {
		t.Error("CheckedAt was not populated")
	}
	if res.CheckedAt.Location() != time.UTC {
		t.Errorf("CheckedAt is in %v, want UTC", res.CheckedAt.Location())
	}
	if rep.GeneratedAt.IsZero() {
		t.Error("GeneratedAt was not populated")
	}
}

func TestNamesAreSorted(t *testing.T) {
	r := NewRegistry(Options{})
	for _, n := range []string{"zeta", "alpha", "mu"} {
		r.MustRegister(stub(n, Healthy("ok")))
	}
	got := r.Names()
	want := []string{"alpha", "mu", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
}

func TestConcurrentRunAndRegister(t *testing.T) {
	r := NewRegistry(Options{})
	r.MustRegister(stub("base", Healthy("ok")))

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			r.Run(context.Background())
		}
	}()
	for i := 0; i < 50; i++ {
		_ = r.Register(stub(string(rune('a'+i%26))+"-check", Healthy("ok")))
	}
	<-done
}

func TestDurationJSONNameMatchesItsUnit(t *testing.T) {
	// time.Duration encodes to nanoseconds. A field named duration_ms holding
	// nanoseconds would make every timing in the console wrong by a factor of
	// a million.
	res := Result{Name: "database", Status: StatusHealthy, Duration: 1500 * time.Microsecond}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, wrong := decoded["duration_ms"]; wrong {
		t.Error("the duration field is named duration_ms but encodes nanoseconds")
	}
	got, ok := decoded["duration_ns"]
	if !ok {
		t.Fatal("no duration_ns field was emitted")
	}
	if got != float64(1_500_000) {
		t.Errorf("duration_ns = %v, want 1500000", got)
	}
}
