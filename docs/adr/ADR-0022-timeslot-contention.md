# ADR-0022: Contention belongs to the timeslot, not the talkgroup

**Status:** Proposed
**Extends:** [ADR-0014](ADR-0014-contention.md)

## The bug this records

`routing.Core` reserved a destination as `(peer, talkgroup, timeslot)`. Two
different talkgroups arriving at the same peer on the same timeslot were
therefore two separate reservations, and both were delivered.

**A DMR timeslot is one TDMA channel and carries one call.** Sending two down it
produces interleaved audio nobody can understand — precisely the outcome
ADR-0014 was written to prevent, missed because the reservation was keyed on
something finer than the thing that can physically only do one job at a time.

A test makes it plain. Peers A, B and C, no bridges. A transmits on TG 9 TS2 and
reaches B and C. B transmits on TG 91 TS2 at the same moment and also reaches
C. C is now receiving two transmissions on one slot, and no drop was recorded,
because as far as the core was concerned nothing collided.

**Repeat made this the ordinary case rather than an exotic one.** Before
ADR-0019 a peer received only the talkgroups a bridge named, and a bridge
usually named one per slot. A master that repeats sends every talkgroup any peer
transmits on to every other peer, so several talkgroups converging on one
timeslot is now the normal shape of a busy club network.

## Decision

**A peer destination is reserved by `(peer, timeslot)`.** The talkgroup does not
take part in the key. One transmission per peer per slot, whatever talkgroup it
is on, whether it arrived by repeat or over a bridge.

The reservation still records the full endpoint, so a drop can name the
talkgroup that was refused and the console can show it. What changes is only
what counts as the same destination.

## A link is not a radio channel, and keeps the old rule

**Upstream destinations are still reserved by `(link, talkgroup, timeslot)`.**

Contention exists to model a physical constraint. A repeater's timeslot is one
TDMA channel because radio is; an OpenBridge link is an IP socket and has no
such limit. BrandMeister carries several talkgroups concurrently over one
OpenBridge connection, and applying the timeslot rule to a link would refuse
traffic for no reason other than a mistaken analogy — QSP would drop a
perfectly deliverable frame and tell the operator the link was busy.

So the two destination kinds contend differently, and this is the reasoning to
consult before anyone unifies them for tidiness.

## Consequences

- **Some traffic that used to be delivered is now refused.** That is the point:
  it was being delivered to a slot that could not carry it. An operator who
  wants two talkgroups to reach one peer simultaneously wants two timeslots,
  which is what DMR gives them.
- Refusals produce a `Drop` with a reason naming the talkgroup already on that
  slot, so the console shows what happened rather than an unexplained gap.
- `BusyCount` falls for any instance carrying several talkgroups to one peer,
  since what were separate reservations are now one.
- **This is what private call contention should use.** ADR-0021 left open what a
  private call reserves, and noted that the whole timeslot was probably right
  and not obviously right. Keying on the slot makes group and private calls
  contend identically without a special case, which is a better answer than the
  one that would have been invented for private calls alone.
- ADR-0014's rules all still hold: the origin is reserved as well as the
  destinations, a terminator releases everything the transmission held, and a
  reservation expires after `StreamTimeout`. Only the key changes.

## What this does not fix

Priority is still absent, as ADR-0014 recorded. Whoever keys up first wins, and
a club that wants an emergency talkgroup to interrupt a rag-chew has no way to
say so. Widening the reservation to the slot makes that more visible rather than
worse, because more traffic now contends.
