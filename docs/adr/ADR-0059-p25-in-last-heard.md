# ADR-0059: A P25 transmission reaches Last heard through a tracker of its own

**Status:** Proposed — the decision is the operator's
**Relates to:** [ADR-0033](ADR-0033-last-heard-is-a-record.md),
[ADR-0034](ADR-0034-p25-is-native.md),
[ADR-0057](ADR-0057-p25-is-a-full-network.md)

## Context

QSP became a P25 reflector on 2026-09-11 and met a real gateway on 2026-09-12:
the operator's Pi-Star registered, his APX was heard, and roughly 573 voice
frames crossed. **None of it appeared in Last heard.** `internal/p25link` has
no reference to the call tracker, the routing core or `observe`, so a radio
heard through a P25 gateway is not a station on the network as far as ADR-0033
is concerned.

That is the same gap that once made a talker on the far end of a link invisible
while their audio was being relayed — the frame was carried and the record said
nobody had spoken.

### The precedent points the hard way, not the easy way

The obvious move is to have the P25 adapter contribute call views the way a
second listener might. **The IPSC adapter does exactly the opposite, on
purpose**, and the comment on `ipscPeerViews.CallViews` says why: every IPSC
transmission was appearing in Last heard twice, because `DeliverFromIPSC`
observes each converted burst into the shared tracker *and* the adapter
appended the IPSC listener's own view of the same transmission. A member saw
one over as two rows a fraction of a second apart, with frame counts of 8
against 10 — the IPSC side counts a header and terminator the converter folds
into one of each — and durations that disagreed because one truncates to whole
seconds.

So the settled rule is: **the tracker owns Last heard**, and a second listener
reaches it by observing into it rather than by keeping a second record.

### Which is where P25 does not fit

`calls.Key` is `{Peer hbp.RepeaterID, Stream hbp.StreamID, Timeslot
hbp.Timeslot}`. A P25 call arriving over the reflector protocol has **none of
those three**. There is no repeater ID: a gateway asserts a callsign in a poll
and nothing else, and there is no login (patch 0330). There is no stream ID:
the transmission boundary is the `0x80` terminator, not an identifier carried
in every frame. And P25 has no timeslot at all — it is FDMA, and the concept
does not exist.

Observing P25 into the shared tracker therefore means inventing all three, and
an invented key is a key that can collide with a real one.

## Decision, proposed

**A second tracker instance, for P25, whose views are appended to the
payload.**

This does not contradict the IPSC rule, and the distinction is the whole
argument: that rule forbids **two records of one event**, not two trackers. An
IPSC transmission is already in the shared tracker before the adapter runs, so
appending was duplication. A P25 transmission is in no tracker at all, so
appending duplicates nothing. One event, one record, from one source.

What it buys:

- P25 appears in Last heard beside DMR, which is what an operator wants and
  what ADR-0033 requires of a record of who has been on the network.
- Nothing in the DMR path changes. `calls.Key` keeps meaning what it means, and
  no synthetic repeater ID, stream ID or timeslot is ever minted.
- `p25GatewaySource.CallViews` already exists returning nil, which is where it
  arrives.

What it costs, stated rather than discovered later:

- **A `CallView` has a timeslot field and a P25 call has no timeslot.** Whatever
  goes there is either absent or a lie, and the console has to render the
  absence rather than printing TS1. §7: where absent and zero mean different
  things, the type has to be able to say so — and `ipsc.colour_code` is the
  recorded instance of getting that wrong.
- The transmission boundary has to come from the frame layer's terminator
  rather than from a stream ID changing, so the "lost stream" case is a
  timeout on silence and needs its own reason string.
- Two trackers means two configured history sizes and two retention settings
  unless they are deliberately shared.

## Alternatives considered

**Generalise `calls.Key` to be mode-agnostic** — a mode discriminator and
opaque identifiers. Cleaner in the abstract and the honest long-term answer if
QSP grows a third mode. Refused for now: it touches every DMR path, including
contention and the text-merge logic, to serve a mode with one live gateway. The
DMR side carries a real network and this would be a refactor of its most
defect-prone data structure for a feature that has no users yet.

**Synthesise a key and observe into the shared tracker.** Refused. It requires
minting a repeater ID that no repeater has, a stream ID the protocol does not
carry, and a timeslot the mode does not have — and a synthetic key that
collides with a real one puts a P25 call and a DMR call in the same bucket.

**Leave it out and show P25 only in the traffic panel**, as patch 0337 does.
This is the honest status quo and it is not sufficient: the operator had to
`curl /healthz` to find out whether his own radio had been heard, and Last
heard is the page that answers that question for every other mode.

## Why this is Proposed

The scope is settled by [ADR-0057](ADR-0057-p25-is-a-full-network.md) — P25 is
a full network, so its traffic belongs in the records a network keeps. What is
not settled is the second-tracker shape versus the mode-agnostic key, and that
is a judgement about how much churn the DMR path should absorb for P25's
benefit. The recommendation above takes the low-churn option deliberately, and
names the refactor it defers.

**And it should not be built at the end of a long session.** §8a records what
happened on 2026-09-09 when a console surface was begun late: four
pattern-matching edits to one script deleted a function while leaving its call
site, and it shipped. This is a record so that the next session starts with the
decision made rather than making it while writing.
