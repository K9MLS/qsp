package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/logging"
)

// sseKeepaliveInterval bounds how long the stream may be silent.
//
// Proxies and load balancers close idle connections. A periodic comment frame
// keeps the connection open without inventing events, which would violate the
// rule that everything on the wire corresponds to something that happened.
const sseKeepaliveInterval = 25 * time.Second

// Limits on open event streams.
//
// **The stream is open to anybody and each one costs a goroutine, a buffered
// subscription and a connection for as long as the client likes**, so without
// a ceiling the number of them was whatever a stranger chose.
const (
	// maxEventStreams is how many streams may be open at once, signed in or
	// not. A club's worth of open dashboards is tens.
	maxEventStreams = 256
	// maxAnonymousEventStreams is how many of those may belong to nobody. The
	// difference is kept for signed-in administrators, so that the people
	// running the server can still watch it while strangers have taken every
	// place they can.
	maxAnonymousEventStreams = 192
	// maxAnonymousEventStreamsPerSource is how many one address may hold
	// without signing in, so that one machine cannot take every anonymous
	// place by itself. Generous, because a club meeting on one network is one
	// address with a dashboard open on every phone in the room.
	maxAnonymousEventStreamsPerSource = 32
	// streamRetryAfterSeconds is how long a refused client is asked to wait.
	streamRetryAfterSeconds = 30
)

// streamSlots counts open event streams.
//
// The per-address map needs no ceiling of its own: an address is in it only
// while it holds a stream, and streams are capped.
type streamSlots struct {
	mu        sync.Mutex
	total     int
	anonymous int
	bySource  map[string]int
}

// take claims a place for one stream and reports whether there was one.
func (s *streamSlots) take(source string, signedIn bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.total >= maxEventStreams {
		return false
	}
	if !signedIn {
		if s.anonymous >= maxAnonymousEventStreams ||
			s.bySource[source] >= maxAnonymousEventStreamsPerSource {
			return false
		}
		if s.bySource == nil {
			s.bySource = make(map[string]int)
		}
		s.bySource[source]++
		s.anonymous++
	}
	s.total++
	return true
}

// release gives a place back. It must be called exactly once for each take
// that succeeded, with the same arguments.
func (s *streamSlots) release(source string, signedIn bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.total--
	if signedIn {
		return
	}
	s.anonymous--
	if s.bySource[source] <= 1 {
		delete(s.bySource, source)
		return
	}
	s.bySource[source]--
}

// handleEvents streams bus events as Server-Sent Events.
//
// The stream is designed so a client can always tell whether its view is
// complete:
//
//   - The client may send Last-Event-ID (or a last_event_id query parameter)
//     with the sequence number it last processed.
//   - The server replays retained events beyond that point.
//   - If the retained history could not cover the gap, the server first emits a
//     "resync" event. That is the client's instruction to discard local state
//     and fetch a fresh snapshot. It is never omitted, because a silent gap
//     would leave the console confidently wrong.
//
// A client that falls behind mid-stream is disconnected with a resync event for
// the same reason.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Without flushing, events would buffer indefinitely and the stream
		// would appear silent. Refusing is clearer than appearing broken.
		http.Error(w, "streaming is not supported by this connection", http.StatusInternalServerError)
		return
	}

	// Counted before anything is written, so a refusal can still be a status.
	_, signedIn := s.session(r)
	source := clientIP(r, s.opts.BehindProxy)
	if !s.streams.take(source, signedIn) {
		// **A status and a time to come back, not a hung connection.** The
		// browser's EventSource does not retry a refusal by itself; the header
		// is for whatever does.
		w.Header().Set("Retry-After", strconv.Itoa(streamRetryAfterSeconds))
		writeJSON(w, s.log, http.StatusServiceUnavailable, map[string]string{
			"error": "too many live views are open on this server; close one or try again shortly",
		})
		return
	}
	defer s.streams.release(source, signedIn)

	// **The stream's write deadline is per write, not per response.** The
	// server's write timeout is counted from the request arriving and would cut
	// every stream off after it; no deadline at all would let a reader that
	// has stopped reading hold a slot for ever. Moved forward before each
	// write, a stream lasts as long as its reader keeps up.
	rc := http.NewResponseController(w)
	keepWriting := func() {
		if s.opts.WriteTimeout > 0 {
			// The error is a connection that cannot take a deadline — a test
			// recorder — which has no deadline to outlive either.
			_ = rc.SetWriteDeadline(time.Now().Add(s.opts.WriteTimeout))
		}
	}
	keepWriting()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// Defeats response buffering in nginx, which otherwise holds SSE frames.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	log := s.log.With(logging.RequestID(RequestIDFromContext(r.Context())))

	sub, currentSeq := s.bus.Subscribe()
	defer sub.Close()

	since, hasSince := lastEventID(r)

	if hasSince {
		replay, complete := s.bus.Replay(since)
		if !complete {
			writeResync(w, "the event history could not cover the gap since your last event")
			log.Info("client resynchronised: history gap",
				slog.Uint64("client_seq", since),
				slog.Uint64("server_seq", currentSeq),
			)
		}
		for _, ev := range replay {
			if err := writeEvent(w, ev); err != nil {
				return
			}
		}
	} else {
		// A client with no prior position must take a snapshot. Telling it so
		// explicitly is better than letting it assume the stream is the whole
		// story.
		writeResync(w, "no prior position supplied; fetch a snapshot and follow the stream from here")
	}
	flusher.Flush()

	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()

	lagBaseline := sub.Lagged()

	for {
		select {
		case <-r.Context().Done():
			return

		case ev, open := <-sub.C():
			if !open {
				// The bus is shutting down. Say so rather than closing silently.
				keepWriting()
				writeResync(w, "the server is shutting down")
				flusher.Flush()
				return
			}
			// Events replayed above may also arrive on the channel; skip any
			// the client has already been sent.
			if hasSince && ev.Seq <= since {
				continue
			}
			if sub.Lagged() != lagBaseline {
				keepWriting()
				writeResync(w, "this connection fell behind and events were dropped")
				flusher.Flush()
				log.Warn("client disconnected after falling behind",
					slog.Uint64("dropped", sub.Lagged()),
				)
				return
			}
			keepWriting()
			if err := writeEvent(w, ev); err != nil {
				return
			}
			flusher.Flush()

		case <-keepalive.C:
			keepWriting()
			// A comment frame. It is not an event and carries no sequence.
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// lastEventID reads the client's position from the standard header or, for
// clients that cannot set headers, a query parameter.
func lastEventID(r *http.Request) (uint64, bool) {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("last_event_id")
	}
	if raw == "" {
		return 0, false
	}
	seq, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}

func writeEvent(w http.ResponseWriter, ev events.Event) error {
	payload, err := marshalEvent(ev)
	if err != nil {
		// Skip an event that cannot be encoded rather than tearing down the
		// stream; the sequence number gap is visible to the client.
		return nil
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, payload)
	return err
}

// writeResync emits the control event instructing a client to discard local
// state and fetch a fresh snapshot. It carries no id, so it does not disturb
// the client's Last-Event-ID position.
func writeResync(w http.ResponseWriter, reason string) {
	payload, err := marshalResync(reason)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: resync\ndata: %s\n\n", payload)
}
