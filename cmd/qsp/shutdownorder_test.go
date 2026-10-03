package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/logging"
)

// TestShutdownWaitsForTranscodersBeforeClosingTheirSockets: a channel ends
// the call it is carrying on its way out -- a terminator through the DMR
// listener, a release through the USRP socket -- and shutdown used to close
// both while that was still happening, leaving repeaters keyed.
//
// The channel here takes 150 ms to finish; the closer stands for the sockets.
//
// To see it fail: delete the awaitDone call from shutdown, and the order is
// "sockets closed, call ended".
func TestShutdownWaitsForTranscodersBeforeClosingTheirSockets(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	note := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, s)
	}
	log := logging.Discard()
	done := make(chan struct{})
	a := &app{log: log, audit: audit.NewLogRecorder(log), transcodingDone: done}
	a.closers = append(a.closers, func(context.Context) error { note("sockets closed"); return nil })

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		time.Sleep(150 * time.Millisecond)
		note("call ended")
		close(done)
	}()
	if err := a.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	<-finished
	mu.Lock()
	got := strings.Join(order, ", ")
	mu.Unlock()
	if want := "call ended, sockets closed"; got != want {
		t.Errorf("order was %q, want %q", got, want)
	}
}

// TestAwaitDoneIsBounded: shutdown must wait for a call to end, and must not
// wait for ever on a vocoder that has stopped answering.
//
// To see it fail: remove the `case <-timer.C` arm from awaitDone, and "never
// finishes" hangs until the test's own deadline.
func TestAwaitDoneIsBounded(t *testing.T) {
	closed := make(chan struct{})
	close(closed)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	later := func(d time.Duration) chan struct{} {
		ch := make(chan struct{})
		time.AfterFunc(d, func() { close(ch) })
		return ch
	}
	tests := []struct {
		name    string
		ctx     context.Context
		done    <-chan struct{}
		limit   time.Duration
		want    bool
		atMost  time.Duration
		atLeast time.Duration
	}{
		{"never started", context.Background(), nil, time.Hour, true, 50 * time.Millisecond, 0},
		{"already finished", context.Background(), closed, time.Hour, true, 50 * time.Millisecond, 0},
		{"finishes inside the limit", context.Background(), later(50 * time.Millisecond), 5 * time.Second, true, time.Second, 40 * time.Millisecond},
		{"never finishes", context.Background(), make(chan struct{}), 100 * time.Millisecond, false, time.Second, 90 * time.Millisecond},
		{"the caller gave up first", cancelled, make(chan struct{}), time.Hour, false, 50 * time.Millisecond, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := make(chan bool, 1)
			start := time.Now()
			go func() { result <- awaitDone(tc.ctx, tc.done, tc.limit) }()
			select {
			case got := <-result:
				took := time.Since(start)
				if got != tc.want {
					t.Errorf("awaitDone = %v, want %v", got, tc.want)
				}
				if took > tc.atMost || took < tc.atLeast {
					t.Errorf("took %s, want between %s and %s", took, tc.atLeast, tc.atMost)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("awaitDone did not return")
			}
		})
	}
}
