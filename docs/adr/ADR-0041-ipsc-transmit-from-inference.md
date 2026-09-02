# ADR-0041: Sending voice to a repeater is built from inference, not capture

**Status:** Accepted — provisional, to be confirmed by capture
**Relates to:** [ADR-0029](ADR-0029-ipsc-from-capture.md),
[ADR-0036](ADR-0036-ipsc-voice-is-not-a-dmr-burst.md)

## Context

[ADR-0029](ADR-0029-ipsc-from-capture.md) says nothing is implemented that a
capture does not demonstrate, and every constant in `internal/protocol/ipsc` has
obeyed it. That rule is why the Motorola-to-hotspot direction worked the first
time it was properly wired, and why the three things that went wrong on
2026-09-02 — the colour code, the slot polarity, the missing voice header — were
all places where something had been assumed rather than measured.

**Every voice frame ever captured travels peer to master.** Nothing has recorded
a master sending voice to a repeater. `ipsc-two-peers.pcap` established that this
direction is required rather than avoidable: two peers transmitted at once, all
326 voice frames were addressed to the master, and none went peer to peer. IPSC
relays; it does not mesh.

The capture that would settle it needs a repeater in the master role with a
second repeater pointed at it, and a host bridged into the path. It is roughly
ten minutes of work and it could not be scheduled: the only Motorola repeaters
on this network are in Denton, Post Falls and Wisconsin, and two of the three
belong to other people.

The cost of waiting is that two members' repeaters are heard by the network and
hear nobody.

## Decision

**QSP sends voice to IPSC repeaters, built from inference, at the operator's
direction and recorded as an exception rather than a discovery.**

ADR-0029 is not withdrawn or weakened. It governs everything else and it governs
this the moment a capture exists. What is written here is a hypothesis with a
test plan, not a finding.

### What is measured, and where from

| Piece | Source |
|---|---|
| Bytes 1–4 are the **sender's own** radio ID | `ipsc-phase2-registration.pcap`: the peer's messages carry the peer's, the master's carry the master's |
| Body layout — counter, source, destination, stream, slot bit, flags, sequence, timestamp | `ipsc-probe-voice.pcap`, `ipsc-two-peers.pcap` |
| Frame layout — marker, length, payload class, 19-byte core, trailer | `ipsc.Message.Payload`, measured |
| Transmission shape — three headers, superframe cycle, terminator | `ipsc-two-peers.pcap`, identical across two repeater models |
| The audio itself | `dmrfec.IPSCFromBurst`, 884 bursts round-tripped bit-exact |

The sender-ID question was the one most likely to be wrong, and it turned out
not to be an inference at all — a capture had already answered it. **A master
relaying somebody else's audio signs it with its own radio ID**, and the
originating radio travels in the body's 24-bit source.

### What is assumed, in the order to check it

1. **That a repeater accepts what a repeater sends.** If the master's frames
   differ in some field, a receiving repeater ignores them and the symptom is
   silence.
2. **That bytes 12–14 are constant.** They read `02 00 00` in every captured
   frame from both models; their meaning is unknown and they are copied.
3. **That three headers matter.** Motorola sends three, so this sends three.
4. **That the call counter may start anywhere.** It is per-transmission and kept
   by a repeater; a master has no repeater keeping it.

### What is deliberately not filtered

A repeater receives **everything**, and decides by its own codeplug what to
repeat. An IPSC peer announces no talkgroups — unlike a Homebrew peer, which
attaches to them explicitly — so filtering here would mean guessing at somebody
else's programming. Sending everything matches what the captures show a master's
peers receiving and leaves the decision where the knowledge is.

## Consequences

A rule that used to be structural is now half withdrawn. Until 0192 a frame
could not reach a Motorola repeater at all, and a test asserted it by naming one
as a bridge endpoint and requiring no delivery. **That test is replaced rather
than deleted**: a Motorola repeater is still not a Homebrew peer and still
cannot be resolved as a Homebrew destination. Audio reaches it out of the IPSC
listener's own socket, which is a different path and a deliberate one.

Repeater-to-repeater works as a side effect, since a frame from one Motorola
repeater is offered to all the others.

**If this does not work on air, the four assumptions above are the list, and a
capture of a real master is the answer.** That was the agreement under which it
was written, and this ADR exists so the next session finds a hypothesis rather
than a mystery.
