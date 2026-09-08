package server

import (
	"sync"
	"time"
)

// Passphrases this instance has offered and not yet seen come back.
//
// # Why this exists
//
// A peering is agreed in two halves: one side offers, the other accepts and
// sends a reciprocal invitation, and the first side accepts that. **Only one
// passphrase exists** — OpenBridge authenticates every datagram against one
// shared secret, so the accepting side is not choosing a new one.
//
// Which means the administrator pasting a reciprocal back has nothing to type
// into the passphrase box, and the form demanded it anyway. The peering ended
// with "a passphrase must be at least 24 characters" printed under an empty
// field that could not be filled, on the second of two steps, after everything
// else had worked.
//
// So the offering side keeps its own passphrase until the reciprocal arrives.
//
// # In memory, and only in memory
//
// A restart forgets them, and the operator is asked for the passphrase as they
// were before. Writing them to disk would mean a file of secrets for peerings
// that may never be accepted, to save typing something the operator was shown.
//
// **This used to say that made it "a worse experience and not a broken one",
// and that was false.** Recognising the end of an exchange depended entirely on
// this store, so a restart between the two halves meant the offering side no
// longer knew a reciprocal was a reciprocal, built another one, and could not
// stop. Invitation.Reply carries that in the token now, which is durable;
// this store only saves the operator retyping a passphrase.
type offeredPassphrases struct {
	mu sync.Mutex
	// by fingerprint, which is what a reciprocal carries.
	held map[string]offeredPassphrase
}

type offeredPassphrase struct {
	passphrase string
	at         time.Time
}

// offerMemory is how long an unaccepted offer is held.
//
// Long enough for two administrators to exchange an email and a message on
// another channel, short enough that a secret is not kept for a peering nobody
// completed. An invitation expires on its own terms as well.
const offerMemory = 7 * 24 * time.Hour

// offerMemoryMax bounds the map, because an authenticated administrator can
// generate offers in a loop and a process should not grow because of it.
const offerMemoryMax = 64

func (o *offeredPassphrases) put(fingerprint, passphrase string) {
	if fingerprint == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.held == nil {
		o.held = make(map[string]offeredPassphrase)
	}
	now := time.Now()
	for k, v := range o.held {
		if now.Sub(v.at) > offerMemory {
			delete(o.held, k)
		}
	}
	// Still too many after expiring: drop the oldest rather than refuse, so a
	// fresh offer always works and an abandoned one is what is lost.
	for len(o.held) >= offerMemoryMax {
		oldest, at := "", now
		for k, v := range o.held {
			if v.at.Before(at) || oldest == "" {
				oldest, at = k, v.at
			}
		}
		delete(o.held, oldest)
	}
	o.held[fingerprint] = offeredPassphrase{passphrase: passphrase, at: now}
}

// peek returns a held passphrase and keeps it.
//
// **Separate from forgetting it, because the accept can still fail.** take used
// to do both, and it ran before the invitation was checked, before the name was
// checked and before anything was written — so a refused acceptance consumed
// the passphrase, and the retry the operator immediately made could no longer
// fill it in. A name that was already in use was enough to cause it.
func (o *offeredPassphrases) peek(fingerprint string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	held, ok := o.held[fingerprint]
	if !ok {
		return "", false
	}
	if time.Since(held.at) > offerMemory {
		return "", false
	}
	return held.passphrase, true
}

// forget drops a held passphrase, once the peering it belongs to is written.
//
// The secret lives in its passphrase file from that point; a second copy in
// memory afterwards is one nobody asked for.
func (o *offeredPassphrases) forget(fingerprint string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.held, fingerprint)
}

// take returns a held passphrase and forgets it, in one step.
//
// Kept for the tests that describe the offer/accept lifecycle in terms of it.
func (o *offeredPassphrases) take(fingerprint string) (string, bool) {
	held, ok := o.peek(fingerprint)
	if ok {
		o.forget(fingerprint)
	}
	return held, ok
}
