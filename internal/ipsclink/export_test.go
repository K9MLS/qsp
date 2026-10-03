package ipsclink

// PerPeerState reports how many entries each map keyed by a peer holds, so a
// test outside the package can see that a peer leaving takes them with it.
func (l *Listener) PerPeerState() (peers, bridges, encoders, relayed, slots int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.peers), len(l.bridges), len(l.encoders), len(l.relayed), len(l.slots)
}
