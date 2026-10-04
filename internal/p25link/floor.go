package p25link

import (
	"sync"
	"time"
)

// FloorHold is how long a talker keeps the floor after its last frame. Voice
// frames arrive every twenty milliseconds, so a second of nothing is a talker
// that has stopped without saying so.
const FloorHold = time.Second

// Floor is who is talking on the P25 side, so that one call at a time is
// carried between the gateways and the Motorola repeaters.
//
// **It exists because the two were about to be joined.** A gateway's call and
// a repeater's call relayed at once reach a listener as two streams of voice
// frames interleaved, and a radio fed that plays neither. The first talker
// keeps the floor until it ends its call or goes quiet for FloorHold; anybody
// else is counted and not carried.
//
// **The gateways are one holder among themselves**, GatewayFloor. What two
// gateways do when they key at once is not changed by this: that is a rule
// waiting on a second gateway to demonstrate it (P25-NETWORK.md), and it
// should not be settled as a side effect of linking a repeater.
//
// The zero value is ready to use. A nil *Floor grants everything, so a
// listener with nothing to share the floor with behaves as it always has.
type Floor struct {
	mu     sync.Mutex
	holder string
	last   time.Time
}

// GatewayFloor is the holder name every gateway talks under.
const GatewayFloor = "gateways"

// Take asks for the floor on behalf of holder, at the moment a frame of its
// call arrives. It reports whether the frame may be carried.
func (f *Floor) Take(holder string, now time.Time) bool {
	if f == nil {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holder == "" || f.holder == holder || now.Sub(f.last) > FloorHold {
		f.holder, f.last = holder, now
		return true
	}
	return false
}

// Release gives the floor up when holder's call ends. A holder that does not
// have it changes nothing.
func (f *Floor) Release(holder string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holder == holder {
		f.holder = ""
	}
}

// Holder is who has the floor at now, or "" for nobody.
func (f *Floor) Holder(now time.Time) string {
	if f == nil {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holder != "" && now.Sub(f.last) > FloorHold {
		return ""
	}
	return f.holder
}
