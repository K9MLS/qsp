package audio

import "sync"

// Queue holds frames between the socket and whatever plays them, and decides
// what is lost when the player falls behind.
//
// **A keyup or a release is never lost, and when something must be, it is the
// oldest audio.** The queue this replaces was a channel that refused whatever
// arrived once it was full. That lost the wrong things twice over: the
// release, which left the far side's transmission open until a timer closed
// it, and the newest audio, so a listener heard what was said five seconds
// ago and never what was being said.
type Queue struct {
	mu     sync.Mutex
	frames []Frame
	limit  int
	ready  chan struct{}
	lost   uint64
}

// NewQueue returns a queue that holds up to limit frames. A limit below one
// is one.
func NewQueue(limit int) *Queue {
	return &Queue{limit: max(limit, 1), ready: make(chan struct{}, 1)}
}

// Signal reports whether f is a keyup or a release: a datagram that says a
// transmission began or ended and carries no audio.
func (f Frame) Signal() bool { return !f.PTT || len(f.Samples) == 0 }

// Push adds a frame and reports whether one was lost to make room. It never
// blocks.
//
// A full queue gives up its oldest audio frame. A full queue holding nothing
// but signals gives up its oldest signal, which takes more keyups and
// releases in a row than any transmission makes.
func (q *Queue) Push(f Frame) (lost bool) {
	q.mu.Lock()
	if len(q.frames) >= q.limit {
		oldest := 0
		for i, held := range q.frames {
			if !held.Signal() {
				oldest = i
				break
			}
		}
		q.frames = append(q.frames[:oldest], q.frames[oldest+1:]...)
		q.lost++
		lost = true
	}
	q.frames = append(q.frames, f)
	q.mu.Unlock()
	q.wake()
	return lost
}

// Pop removes the oldest frame. The second result is false when there is
// none.
func (q *Queue) Pop() (Frame, bool) {
	q.mu.Lock()
	if len(q.frames) == 0 {
		q.mu.Unlock()
		return Frame{}, false
	}
	f := q.frames[0]
	q.frames[0] = Frame{}
	q.frames = q.frames[1:]
	more := len(q.frames) > 0
	q.mu.Unlock()
	if more {
		q.wake()
	}
	return f, true
}

// Ready is readable while the queue may hold a frame. A reader takes from it
// and then calls Pop, which can still find nothing.
func (q *Queue) Ready() <-chan struct{} { return q.ready }

// Discard empties the queue and reports how many frames it held.
func (q *Queue) Discard() int {
	q.mu.Lock()
	n := len(q.frames)
	q.frames = nil
	q.mu.Unlock()
	return n
}

// Lost is how many frames have been given up to make room, ever.
func (q *Queue) Lost() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.lost
}

func (q *Queue) wake() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}
