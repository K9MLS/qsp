// Package health provides QSP's health-check framework.
//
// The framework exists to answer an operator's questions directly: what is
// working, what is broken, and what is simply not present yet. That last
// category matters. QSP is built in phases, and a subsystem that has not been
// implemented must report StatusUnavailable rather than being omitted or, worse,
// reported healthy. Silence and false green are both lies.
//
// Checks are registered at startup and evaluated on demand. A check must be
// cheap and must not block indefinitely; the registry bounds each one with a
// timeout so a single wedged dependency cannot stall the whole report.
package health

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Status is the outcome of a check.
type Status string

const (
	// StatusHealthy means the subsystem is fully functional.
	StatusHealthy Status = "healthy"
	// StatusDegraded means the subsystem works but not correctly or not fully.
	// The operator should look, but service continues.
	StatusDegraded Status = "degraded"
	// StatusFailing means the subsystem is not working.
	StatusFailing Status = "failing"
	// StatusUnavailable means the subsystem is not implemented or not
	// configured in this build. It is not an error and never degrades the
	// overall result; it is an honest statement that there is nothing to check.
	StatusUnavailable Status = "unavailable"
)

// severity orders statuses for aggregation. StatusUnavailable deliberately
// ranks lowest: an unbuilt subsystem must not make a working system look sick.
func (s Status) severity() int {
	switch s {
	case StatusFailing:
		return 3
	case StatusDegraded:
		return 2
	case StatusHealthy:
		return 1
	case StatusUnavailable:
		return 0
	default:
		return 0
	}
}

// Valid reports whether s is a declared status.
func (s Status) Valid() bool {
	switch s {
	case StatusHealthy, StatusDegraded, StatusFailing, StatusUnavailable:
		return true
	default:
		return false
	}
}

// Result is one check's outcome.
type Result struct {
	// Name identifies the check, for example "database" or "clock".
	Name string `json:"name"`
	// Status is the outcome.
	Status Status `json:"status"`
	// Summary is a one-line operator-facing description. For anything other
	// than healthy it must say what is wrong.
	Summary string `json:"summary"`
	// Fix, when present, tells the operator what to do about it.
	Fix string `json:"fix,omitempty"`
	// Detail carries structured supporting data. It must never contain
	// credentials.
	Detail map[string]string `json:"detail,omitempty"`
	// Duration is how long the check took.
	//
	// The JSON name states nanoseconds because that is what a time.Duration
	// encodes to. Naming it milliseconds while emitting nanoseconds would make
	// every timing in the console wrong by a factor of a million.
	Duration time.Duration `json:"duration_ns"`
	// CheckedAt is when the check ran, in UTC.
	CheckedAt time.Time `json:"checked_at"`
}

// Healthy builds a passing result.
func Healthy(summary string) Result {
	return Result{Status: StatusHealthy, Summary: summary}
}

// Degraded builds a degraded result.
func Degraded(summary, fix string) Result {
	return Result{Status: StatusDegraded, Summary: summary, Fix: fix}
}

// Failing builds a failing result.
func Failing(summary, fix string) Result {
	return Result{Status: StatusFailing, Summary: summary, Fix: fix}
}

// Unavailable builds a result for a subsystem that is not present.
//
// The summary should state plainly why there is nothing to check, for example
// "not implemented until phase 5".
func Unavailable(summary string) Result {
	return Result{Status: StatusUnavailable, Summary: summary}
}

// Checker evaluates one subsystem.
//
// Check must respect ctx and return promptly when it is cancelled. Returning an
// error is equivalent to returning StatusFailing; the error text becomes the
// summary.
type Checker interface {
	Name() string
	Check(ctx context.Context) Result
}

// CheckerFunc adapts a function to Checker.
type CheckerFunc struct {
	CheckName string
	Fn        func(ctx context.Context) Result
}

// Name implements Checker.
func (c CheckerFunc) Name() string { return c.CheckName }

// Check implements Checker.
func (c CheckerFunc) Check(ctx context.Context) Result { return c.Fn(ctx) }

// Report is the aggregate result of every registered check.
type Report struct {
	// Status is the most severe status among the checks, ignoring
	// StatusUnavailable. An empty registry reports StatusUnavailable.
	Status Status `json:"status"`
	// Results are the individual outcomes, ordered by name.
	Results []Result `json:"results"`
	// GeneratedAt is when the report was produced, in UTC.
	GeneratedAt time.Time `json:"generated_at"`
}

// Ready reports whether the instance should accept traffic. A failing check
// means it should not.
func (r Report) Ready() bool { return r.Status != StatusFailing }

// ErrDuplicateCheck is returned when two checks share a name.
var ErrDuplicateCheck = errors.New("a health check with this name is already registered")

// DefaultTimeout bounds a single check.
const DefaultTimeout = 5 * time.Second

// Registry holds the registered checks. It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	checkers map[string]Checker
	timeout  time.Duration
	clock    func() time.Time
}

// Options configures a Registry.
type Options struct {
	// Timeout bounds each individual check. Zero selects DefaultTimeout.
	Timeout time.Duration
	// Clock supplies timestamps. Zero uses time.Now. Injected for tests.
	//
	// Run evaluates checks concurrently and calls Clock from each goroutine,
	// so an injected clock must be safe for concurrent use.
	Clock func() time.Time
}

// NewRegistry constructs an empty Registry.
func NewRegistry(opts Options) *Registry {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	return &Registry{
		checkers: make(map[string]Checker),
		timeout:  opts.Timeout,
		clock:    opts.Clock,
	}
}

// Register adds a check. Names must be unique.
func (r *Registry) Register(c Checker) error {
	if c == nil {
		return errors.New("cannot register a nil health check")
	}
	name := c.Name()
	if name == "" {
		return errors.New("a health check must have a name")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.checkers[name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateCheck, name)
	}
	r.checkers[name] = c
	return nil
}

// MustRegister is Register that panics on error.
//
// It is intended solely for wiring at startup, where a duplicate name is a
// programming error that should prevent the process from starting.
func (r *Registry) MustRegister(c Checker) {
	if err := r.Register(c); err != nil {
		panic("health: " + err.Error())
	}
}

// Names returns the registered check names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.checkers))
	for n := range r.checkers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Run evaluates every check and aggregates the results.
//
// Checks run concurrently, each bounded by the registry timeout. A check that
// exceeds its timeout is reported as failing rather than being allowed to stall
// the report.
func (r *Registry) Run(ctx context.Context) Report {
	r.mu.RLock()
	checkers := make([]Checker, 0, len(r.checkers))
	for _, c := range r.checkers {
		checkers = append(checkers, c)
	}
	timeout := r.timeout
	r.mu.RUnlock()

	results := make([]Result, len(checkers))
	var wg sync.WaitGroup
	for i, c := range checkers {
		wg.Add(1)
		go func(i int, c Checker) {
			defer wg.Done()
			results[i] = r.runOne(ctx, c, timeout)
		}(i, c)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })

	report := Report{
		Status:      StatusUnavailable,
		Results:     results,
		GeneratedAt: r.clock().UTC(),
	}
	for _, res := range results {
		if res.Status.severity() > report.Status.severity() {
			report.Status = res.Status
		}
	}
	return report
}

func (r *Registry) runOne(ctx context.Context, c Checker, timeout time.Duration) Result {
	start := r.clock()

	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan Result, 1)
	go func() {
		defer func() {
			// A panicking check must not take the process down. It is reported
			// as failing, which is the truthful outcome.
			if rec := recover(); rec != nil {
				done <- Failing(
					fmt.Sprintf("check panicked: %v", rec),
					"this is a defect in QSP; please report it with the surrounding log lines",
				)
			}
		}()
		done <- c.Check(checkCtx)
	}()

	var res Result
	select {
	case res = <-done:
	case <-checkCtx.Done():
		res = Failing(
			fmt.Sprintf("check did not complete within %s", timeout),
			"the subsystem is unresponsive; check its logs and connectivity",
		)
	}

	if !res.Status.Valid() {
		res = Failing(
			fmt.Sprintf("check returned an unrecognised status %q", res.Status),
			"this is a defect in QSP; please report it",
		)
	}

	res.Name = c.Name()
	res.Duration = r.clock().Sub(start)
	res.CheckedAt = start.UTC()
	return res
}
