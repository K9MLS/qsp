package peers

import (
	"sort"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// DefaultAttachmentTimeout is how long a dynamically attached talkgroup keeps
// being delivered after the peer last used it.
//
// Long enough to survive a pause in a conversation, short enough that a
// talkgroup somebody worked at breakfast is not still arriving at lunch.
// Fifteen minutes is what the established networks settled on.
const DefaultAttachmentTimeout = 15 * time.Minute

// Attachment is one talkgroup a peer receives.
type Attachment struct {
	// Peer is the repeater or hotspot.
	Peer hbp.RepeaterID
	// Talkgroup and Timeslot identify what is attached. A talkgroup on one
	// slot is a different attachment from the same talkgroup on the other,
	// because they are different paths to the radio.
	Talkgroup uint32
	Timeslot  hbp.Timeslot
	// Static reports whether this attachment was configured rather than
	// created by the peer transmitting. A static attachment never lapses.
	Static bool
	// LastUsed is when the peer last transmitted on it, in UTC. Zero for a
	// static attachment nobody has used.
	LastUsed time.Time
}

// attachmentKey identifies one attachment.
type attachmentKey struct {
	peer      hbp.RepeaterID
	talkgroup uint32
	slot      hbp.Timeslot
}

// SubscriptionConfig turns per-peer attachment on and seeds the static ones.
//
// **The zero value delivers every talkgroup to every peer**, which is what QSP
// did before attachment existed. Enabling it without configuring anything would
// leave a network where nobody hears anything until they transmit, and that is
// a surprising thing to happen on upgrade.
type SubscriptionConfig struct {
	// Enabled turns per-peer attachment on. When false, every ready peer
	// receives every talkgroup.
	Enabled bool
	// Timeout is how long a dynamic attachment survives after last use. Zero
	// selects DefaultAttachmentTimeout.
	Timeout time.Duration
	// Static are attachments that never lapse, for the cases dynamics cannot
	// serve: a calling channel that must be there before anybody speaks, and a
	// repeater that should always carry its regional talkgroup.
	Static []Attachment
}

// attach records that a peer used a talkgroup, creating or refreshing a dynamic
// attachment.
//
// **It is called before routing, not after.** Observing the frame, attaching,
// and letting the next frame through would clip the first syllable of every
// transmission onto a newly attached talkgroup — the mistake ADR-0016 records
// making with PTT triggers.
func (m *Master) attach(peer hbp.RepeaterID, talkgroup uint32, slot hbp.Timeslot, now time.Time) {
	if !m.cfg.Subscription.Enabled {
		return
	}
	key := attachmentKey{peer: peer, talkgroup: talkgroup, slot: slot}
	if existing, ok := m.attachments[key]; ok {
		// A static attachment stays static; transmitting on it does not
		// demote it to something that can lapse.
		existing.LastUsed = now
		return
	}
	m.attachments[key] = &Attachment{
		Peer:      peer,
		Talkgroup: talkgroup,
		Timeslot:  slot,
		LastUsed:  now,
	}
}

// Attached reports whether a peer should receive a talkgroup on a timeslot.
//
// With subscription off it reports true for everything, which is what makes an
// instance that has not configured it behave exactly as it did before.
func (m *Master) Attached(peer hbp.RepeaterID, talkgroup uint32, slot hbp.Timeslot) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.cfg.Subscription.Enabled {
		return true
	}
	a, ok := m.attachments[attachmentKey{peer: peer, talkgroup: talkgroup, slot: slot}]
	if !ok {
		return false
	}
	if a.Static {
		return true
	}
	return m.cfg.Now().UTC().Sub(a.LastUsed) <= m.cfg.Subscription.Timeout
}

// Attachments returns every attachment, ordered, as a copy.
//
// "Why can I not hear that talkgroup" is the most common question on any DMR
// network, and this is the answer to it.
func (m *Master) Attachments() []Attachment {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Attachment, 0, len(m.attachments))
	for _, a := range m.attachments {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Peer != out[j].Peer {
			return out[i].Peer < out[j].Peer
		}
		if out[i].Timeslot != out[j].Timeslot {
			return out[i].Timeslot < out[j].Timeslot
		}
		return out[i].Talkgroup < out[j].Talkgroup
	})
	return out
}

// DropAttachments removes every dynamic attachment a peer holds and reports how
// many, which is what a member asks for by transmitting on the unlink
// talkgroup.
//
// **Static attachments survive.** A member pressing disconnect says what they
// want to stop hearing; a static attachment is an administrator's statement
// about what a peer must always carry, and a PTT does not overrule it —
// otherwise somebody drops themselves off the club calling channel and cannot
// work out why they have gone deaf.
//
// Waiting out the timeout is not a control, it is a delay. Somebody landing on a
// talkgroup, finding it empty and moving on wants to leave now, and doing that
// repeatedly is what exploring a network looks like.
func (m *Master) DropAttachments(peer hbp.RepeaterID) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	var n int
	for k, a := range m.attachments {
		if k.peer == peer && !a.Static {
			delete(m.attachments, k)
			n++
		}
	}
	return n
}

// SubscriptionEnabled reports whether per-peer attachment is in force.
func (m *Master) SubscriptionEnabled() bool { return m.cfg.Subscription.Enabled }

// AttachmentCount returns how many attachments are held.
func (m *Master) AttachmentCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.attachments)
}

// expireAttachments drops dynamic attachments that have gone quiet.
//
// Static ones are never dropped: they were configured to be there before
// anybody speaks, and a timeout would defeat the reason they exist.
func (m *Master) expireAttachments(now time.Time) []Attachment {
	var lapsed []Attachment
	for key, a := range m.attachments {
		if a.Static {
			continue
		}
		if now.Sub(a.LastUsed) > m.cfg.Subscription.Timeout {
			lapsed = append(lapsed, *a)
			delete(m.attachments, key)
		}
	}
	sort.Slice(lapsed, func(i, j int) bool {
		if lapsed[i].Peer != lapsed[j].Peer {
			return lapsed[i].Peer < lapsed[j].Peer
		}
		return lapsed[i].Talkgroup < lapsed[j].Talkgroup
	})
	return lapsed
}

// seedStaticAttachments installs the configured attachments.
func (m *Master) seedStaticAttachments() {
	for _, a := range m.cfg.Subscription.Static {
		m.attachments[attachmentKey{peer: a.Peer, talkgroup: a.Talkgroup, slot: a.Timeslot}] =
			&Attachment{
				Peer:      a.Peer,
				Talkgroup: a.Talkgroup,
				Timeslot:  a.Timeslot,
				Static:    true,
			}
	}
}

// SetSubscription replaces the attachment settings.
//
// Called from the listener's goroutine when a configuration is applied, which
// is the only goroutine that touches a Master. Static attachments are re-seeded
// and dynamic ones are kept: a member who attached a talkgroup by transmitting
// should not lose it because an administrator saved an unrelated change.
func (m *Master) SetSubscription(cfg SubscriptionConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultAttachmentTimeout
	}
	m.cfg.Subscription = cfg

	// Configured attachments that are no longer configured stop being static.
	// Dropping them outright would be wrong — the member may be using one — so
	// they become dynamic and lapse on their own if nobody does.
	for key, a := range m.attachments {
		if a.Static {
			a.Static = false
			m.attachments[key] = a
		}
	}
	m.seedStaticAttachments()
}
