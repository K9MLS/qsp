# ADR-0028: Parrot replays bytes it never understood

**Status:** Proposed

## Context

A member connects a new hotspot and has no way to tell whether it works without
finding somebody awake to talk to. That happened on this network at 06:15 on the
morning after the first member joined, and it is the ordinary case rather than
the unlucky one: a club has a handful of active operators and a member who keys
up into silence cannot distinguish a broken radio from an empty channel.

Parrot answers that. A member transmits on a talkgroup, and the network plays
their own audio back to them. It is the one feature that tells somebody their
whole path works — radio, hotspot, network, and back — without anybody else
being involved.

## Why this is cheap when audio features are not

**QSP does not decode audio and does not need to.** A transmission is a sequence
of frames carrying 33-byte DMR bursts, and QSP relays them without looking
inside — which is what makes the vocoder a phase 5 concern rather than a
prerequisite for everything.

Parrot is therefore not an audio feature. It is a buffer: keep the frames, send
them back. QSP replays bytes it never understood, and the radio at the far end
decodes its own audio exactly as it would anybody else's.

That is why this arrives now and a repeater's audio processing does not.

## Decision

**A configured talkgroup records and replays.** `dmr.parrot` names it, and it
is off unless configured, because a talkgroup QSP swallows is one an operator
did not choose to lose.

```json
"parrot": {
  "enabled": true,
  "talkgroup": 9990,
  "timeslot": 2,
  "max_duration": "30s",
  "gap": "1s"
}
```

There is no default talkgroup. 9990 is conventional on some networks and 9998 on
others, and §0 refused to ship one network's numbers for the same reason it
refused their talkgroup lists.

### Parrot answers a group call, and cannot yet answer a private one

*Amended 2026-08-29, after it did not work on air.*

Most operators program parrot as a private call, and QSP records one: a private
call to the parrot number is parrot traffic on either timeslot, since it is
addressed to a number rather than carried on a talkgroup.

**It cannot replay one usefully, and the reason is the thing that makes parrot
cheap.** A DMR voice header carries the call's addressing *inside* the 33-byte
burst, in the Link Control, under its own error correction. A radio believes the
Link Control, not the wrapper around it.

A first attempt swapped the source and target in the wrapper so a private replay
would be addressed back to the calling radio. Five replays went out at correct
timing, and the radio played none of them: the Link Control still said "private
call to 9990", so the frames arrived addressed to a number that was not the
radio's own and were muted. The swap achieved nothing except making the wrapper
disagree with the payload, and has been removed.

Rewriting the Link Control means decoding and re-encoding a DMR burst:
deinterleaving, error correction, checksums. That is exactly what QSP does not
do, and not doing it is what lets parrot exist at all without a vocoder.

**So parrot answers a group call.** Replayed unchanged, the Link Control still
says "group call to this talkgroup", and a radio with that talkgroup in its
receive list un-mutes it with nothing rewritten anywhere. An operator keeps
whichever number is already in their radios and programs it as a group contact.

A private parrot remains possible and is a different piece of work: the first
place QSP would have to understand a burst rather than carry it.

### The frames go back the way they came

A recording is replayed **to the peer that sent it and to no one else**. Parrot
is a test of one member's path, and a club whose net is interrupted by somebody
testing their radio has been given a worse thing than they had.

The replayed frames carry a new stream ID, because a repeated stream ID is a
duplicate transmission to a radio and will be discarded as one. Source and
target are preserved, so the member's own display shows what it showed when they
transmitted.

### Timing is the whole problem

DMR frames arrive every 60 milliseconds and a radio decodes them on that
schedule. **Replaying faster produces nothing a radio can use**, and replaying
from the listener's sweep is impossible: the sweep runs every second, sixteen
times too slow for a single frame.

So playback owns a goroutine, and it is the only part of QSP that writes to the
socket without being the listener. That is a deliberate exception to ADR-0002
and is safe for a narrow reason: `net.UDPConn` is safe for concurrent use, and
playback touches no routing state — it holds its own frames and sends them to
one address.

**It does not go through the routing core at all.** A recording being routed
would be subject to access control, contention and subscription on the way back
out, and none of those questions apply to a member hearing their own voice.

### What a second transmission does

A member who keys up while their recording is playing **stops the playback**.
The alternative is two audio streams on one timeslot, which is what contention
exists to prevent, and a member testing their radio pressing PTT again means
"start over" far more often than it means anything else.

### Bounds

`max_duration` bounds a recording, defaulting to 30 seconds. Without it, a stuck
PTT is unbounded memory, and a member who transmits for five minutes and waits
five more to hear it has not been served well either. A recording that reaches
the limit is replayed truncated rather than discarded: hearing thirty seconds of
your own audio answers the question.

One recording per peer at a time. Two hotspots may test at once and will not
collide; the same hotspot testing twice replaces its own recording, which is the
behaviour above.

## Consequences

- **A new member can prove their setup alone**, which is the whole point and the
  reason this outranks features with more visible ambition.
- QSP gains a goroutine that writes to the peer socket, which the architecture
  document should say plainly rather than leave to be discovered.
- Parrot traffic is invisible to bridges and upstreams: a recording never enters
  the routing core, so it cannot leak onto a linked network. That falls out of
  the design rather than needing a rule.
- The talkgroup is swallowed. Nothing else on the network can use the configured
  number while parrot is enabled, which is why it is configuration and why there
  is no default.
