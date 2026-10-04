package config

import (
	"maps"
	"slices"
)

// Clone returns a configuration that shares no memory with c.
//
// **Assigning a Config copies the struct and shares everything behind it.** The
// upstreams, the bridges, every access list and the Access pointer itself are
// still the originals, so `cfg := manager.Current()` followed by
// `cfg.DMR.Upstreams[i].Address = address` wrote into the running
// configuration — before it was validated and outside the manager's mutex. The
// comparison that reports what needs a restart then compared the change with
// itself and found nothing, and a save that failed left the edit live, to be
// written to disk by the next unrelated save.
//
// Written out field by field rather than through a JSON round trip, which
// would drop Access's unexported advisory fields and turn an empty list into
// an absent one. A test fills every field by reflection and fails when a
// slice, map or pointer added later is not copied here.
func (c Config) Clone() Config {
	out := c

	out.P25.AllowedCallsigns = slices.Clone(c.P25.AllowedCallsigns)

	out.IPSC.AllowedPeers = slices.Clone(c.IPSC.AllowedPeers)
	out.IPSC.PeerNames = maps.Clone(c.IPSC.PeerNames)
	if c.IPSC.ColourCode != nil {
		cc := *c.IPSC.ColourCode
		out.IPSC.ColourCode = &cc
	}

	out.DMR.Bridges = slices.Clone(c.DMR.Bridges)
	for i := range out.DMR.Bridges {
		out.DMR.Bridges[i].Endpoints = slices.Clone(out.DMR.Bridges[i].Endpoints)
	}
	out.DMR.Triggers = slices.Clone(c.DMR.Triggers)
	for i := range out.DMR.Triggers {
		out.DMR.Triggers[i].On = slices.Clone(out.DMR.Triggers[i].On)
	}
	out.DMR.Schedule = slices.Clone(c.DMR.Schedule)
	for i := range out.DMR.Schedule {
		out.DMR.Schedule[i].Days = slices.Clone(out.DMR.Schedule[i].Days)
	}
	out.DMR.Upstreams = slices.Clone(c.DMR.Upstreams)
	for i := range out.DMR.Upstreams {
		if id := out.DMR.Upstreams[i].Identity; id != nil {
			identity := *id
			out.DMR.Upstreams[i].Identity = &identity
		}
	}
	out.DMR.Transcoders = slices.Clone(c.DMR.Transcoders)
	for i := range out.DMR.Transcoders {
		out.DMR.Transcoders[i].PermitPeers = slices.Clone(out.DMR.Transcoders[i].PermitPeers)
	}
	out.DMR.Subscription.Static = slices.Clone(c.DMR.Subscription.Static)
	out.DMR.Join.Talkgroups = slices.Clone(c.DMR.Join.Talkgroups)

	if c.DMR.Access != nil {
		// The struct copy carries openedAllowOnly and allowOnlyNamed, which
		// the startup advisory reads and which no JSON document holds.
		a := *c.DMR.Access
		a.Registration.IDs = slices.Clone(a.Registration.IDs)
		a.Subscribers.IDs = slices.Clone(a.Subscribers.IDs)
		a.Talkgroups.Timeslot1.IDs = slices.Clone(a.Talkgroups.Timeslot1.IDs)
		a.Talkgroups.Timeslot2.IDs = slices.Clone(a.Talkgroups.Timeslot2.IDs)
		out.DMR.Access = &a
	}

	out.Quantar.AllowedRouters = slices.Clone(c.Quantar.AllowedRouters)
	out.Weather.Zones = slices.Clone(c.Weather.Zones)
	out.Weather.Events = slices.Clone(c.Weather.Events)

	return out
}

// Upgrade applies the fixes Load applies to a document written by an older
// QSP: an allow-only subscriber list is opened, and the Weather page's first
// alert list is widened.
//
// **A configuration reaches a server by more doors than its file.** A version
// in the history and a backup are both documents an older QSP wrote, and each
// was refused where the same document in the file would have started the
// server. Every door calls this, so the next fix of this kind is added once.
//
// It writes through c's slices and its Access pointer, so call it on a
// configuration nothing else holds — a Clone, or one just decoded.
func (c *Config) Upgrade() {
	c.openSubscribers()
	c.widenWeather()
}
