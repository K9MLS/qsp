package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestTheSourceTableIsBounded. Every entry is put there by an anonymous sender,
// so a table with no ceiling is memory a flood from many addresses can spend.
//
// To see it fail: delete the `t.makeRoom(now)` call in sourceThrottle.fail.
func TestTheSourceTableIsBounded(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	const window = 10 * time.Minute

	tests := []struct {
		name string
		// fill is run against a table holding at most four addresses.
		fill func(th *sourceThrottle)
		// wantKept and wantGone are addresses checked afterwards.
		wantKept, wantGone []string
	}{
		{
			name: "past the ceiling the address quiet longest is forgotten",
			fill: func(th *sourceThrottle) {
				for i := range 6 {
					th.fail(fmt.Sprintf("192.0.2.%d", i), 0, start.Add(time.Duration(i)*time.Second))
				}
			},
			wantKept: []string{"192.0.2.5", "192.0.2.4"},
			wantGone: []string{"192.0.2.0", "192.0.2.1"},
		},
		{
			name: "expired entries go before live ones",
			fill: func(th *sourceThrottle) {
				for i := range 3 {
					th.fail(fmt.Sprintf("192.0.2.%d", i), 0, start)
				}
				// Heard from longest ago of the live ones, and still in its window.
				th.fail("198.51.100.1", 0, start.Add(window-time.Minute))
				th.fail("198.51.100.2", 0, start.Add(window+time.Second))
			},
			wantKept: []string{"198.51.100.1", "198.51.100.2"},
			wantGone: []string{"192.0.2.0", "192.0.2.1", "192.0.2.2"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			th := newSourceThrottle(5, window)
			th.max = 4
			tc.fill(th)

			if n := th.size(); n > th.max {
				t.Fatalf("the table holds %d addresses, over its ceiling of %d", n, th.max)
			}
			for _, key := range tc.wantKept {
				if _, ok := th.sources[key]; !ok {
					t.Errorf("%s was forgotten", key)
				}
			}
			for _, key := range tc.wantGone {
				if _, ok := th.sources[key]; ok {
					t.Errorf("%s is still remembered", key)
				}
			}
		})
	}
}

// noAccounts is a Repository holding nobody, which is all a flood of unknown
// names needs.
type noAccounts struct{ Repository }

func (noAccounts) AccountByUsername(context.Context, string) (Account, bool, error) {
	return Account{}, false, nil
}

// TestPasswordChecksTakeTurns. A check is expensive on purpose and the form is
// open to anybody, so the number running at once is what a flood of unknown
// names can cost the machine. With every turn taken, the next attempt waits —
// and gives up when its request does, rather than being answered "incorrect"
// without having been checked.
//
// To see it fail: in Service.verify, add a `default:` case to the select so
// that it stops waiting for a turn.
func TestPasswordChecksTakeTurns(t *testing.T) {
	svc, err := NewService(noAccounts{}, Policy{
		Hash: Params{Iterations: 1000, SaltLength: 16, KeyLength: 32},
	}, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if cap(svc.verifying) != maxConcurrentVerifies {
		t.Fatalf("%d checks may run at once, want %d", cap(svc.verifying), maxConcurrentVerifies)
	}
	// Take every turn, as a flood would.
	for range cap(svc.verifying) {
		svc.verifying <- struct{}{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = svc.Authenticate(ctx, "NOBODY", "wrong", "203.0.113.5", "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("an attempt with no turn free returned %v, want it to wait and give up", err)
	}
	if svc.throttle.size() != 0 {
		t.Error("an attempt that was never checked was counted as a failure")
	}

	// A turn given back is a turn the next attempt gets.
	<-svc.verifying
	_, err = svc.Authenticate(context.Background(), "NOBODY", "wrong", "203.0.113.5", "")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("with a turn free the attempt returned %v", err)
	}
}
