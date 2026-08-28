# ADR-0020: Access control, and why it is checked in two places

**Status:** Proposed

## Context

ADR-0019 named access control as layer 2 and the next gap after repeat. QSP has
none: a registered peer may transmit on any talkgroup, and every talkgroup any
peer transmits on reaches every other peer. That is workable for a club whose
members know each other and it is not workable on an address the internet can
reach.

HBlink has four lists and QSP should have the same four, because operators
arriving from HBlink already know what they mean and a different vocabulary
would buy nothing:

| HBlink | What it decides |
|---|---|
| `REG_ACL` | which repeater IDs may register |
| `SUB_ACL` | which subscriber IDs may transmit |
| `TGID_TS1_ACL`, `TGID_TS2_ACL` | which talkgroups are carried, per timeslot |

## Decision

**A new package, `internal/access`.** It parses lists, and answers whether an ID
is permitted. It performs no I/O, holds no state, and imports nothing from the
rest of QSP, so both `internal/peers` and `internal/routing` can depend on it
without a cycle — `routing` deliberately does not know about `peers` and this
must not be what introduces the edge. An ACL evaluation is a pure function of an
ID and a list, which is the same property ADR-0013 requires of the routing
decision.

**The check happens in two places, and this is the substantive decision.**

Registration and subscriber checks belong at the master, in
`internal/peers`: they are questions about who is talking, and the answer is
known when the datagram arrives. Registration is tested at login, before the
password lookup, so that a banned ID never reaches the credential path. The
subscriber ID is tested per frame at the data path, which is deliberate — on
DMR a hotspot is shared infrastructure and the offending party is a radio.
Refusing one subscriber must not disconnect the peer carrying it.

The talkgroup check is tested **twice, on ingress and again on egress**, and the
egress test lives in `routing` where the destination is known.

A permit-list applied only on ingress is not access control. Traffic arriving
over a bridge or an OpenBridge link never passes the ingress gate, so an
ingress-only check permits precisely the traffic an operator is least able to
vouch for — somebody else's network — while stopping their own members. The
egress test is what makes the list mean "this talkgroup is carried here" rather
than "local peers may key up on it".

For peer-originated traffic the two tests are today redundant, since the list is
one global list and a frame that passed ingress will pass egress. That
redundancy is temporary and is the point: when per-peer lists arrive, egress
becomes a genuinely per-destination question and the call site is already there.

## Configuration

```json
"access": {
  "registration": {"mode": "permit", "ids": ["3121001-3121099"]},
  "subscribers":  {"mode": "deny",   "ids": ["3121077"]},
  "talkgroups": {
    "timeslot_1": {"mode": "deny", "ids": []},
    "timeslot_2": {"mode": "permit", "ids": ["9", "3100-3199"]}
  }
}
```

`mode` is `permit` or `deny`. A permit list refuses anything not named; a deny
list allows anything not named. Entries are a single ID or an inclusive range.

**Ranges are strings rather than structured objects.** A structured
`{"from": 3100, "to": 3199}` would validate itself and need no parser, but there
is no admin interface and will not be for some time, so every list is
hand-edited in a text editor for a good while yet — and a talkgroup permit
list for a real network is mostly ranges. The string form also translates
almost exactly from what an operator already types in HBlink. The parser is
strict in exchange: reversed ranges are refused rather than swapped, and a
number is digits only, because a leading plus or an underscore would mean two
spellings of one value in a document that is versioned and diffed.

**The zero value permits everything**, because `deny` with an empty list denies
nobody. An absent `access` block therefore behaves exactly as 0.1.9 does, and
upgrading does not silently disconnect a running club. That is the reasoning
that made `CoreOptions.NoRepeat` a negative: the empty struct has to be the
correct one.

`permit` with an empty list refuses every station and is a validation error
rather than a runtime surprise, since nobody configures that on purpose.

**Default-permit on a reachable address is a fatal startup error.**

A warning was the first decision here and is wrong. A startup warning is read
once by whoever happens to be watching the journal, and never again; the risk it
guards is that the moment UDP 62031 is forwarded at the router, an unconfigured
instance repeats everything to everyone from the open internet. The weakest
available mitigation for the strongest available risk is not a trade.

So: a DMR listener bound to an address reachable from beyond this host, with
every list permissive and no access block written, refuses to start.

**The escape hatch is to say it on purpose.** `{"mode": "deny", "ids": []}` —
deny nobody, permit everything — starts without complaint. The operator types
one line to mean it, and that line lives in the versioned configuration where it
appears in a diff and can be grepped for, rather than in a journal that has
since rotated.

This is the ADR-0012 precedent exactly: enabled-with-no-password-file is fatal,
and the mitigation is an error that says what to create. The zero value still
permits everything, so nothing about the semantics changes; what changes is that
silence about it is no longer allowed on an address the internet can reach.

A list that spells out the whole range is a deliberate choice and does not trip
the check either. Only silence does.

## The three lists do not share a ceiling

An ID's largest permitted value comes from the width of the field it travels in,
and the fields differ. A DMRD frame carries the source and target as 24-bit
values, so a talkgroup or a subscriber ID above 16,777,215 cannot appear on the
air and is a typo worth refusing at load. The repeater ID is a 32-bit field.

**That difference is load-bearing.** A hotspot registers with its owner's
seven-digit ID and a two-digit suffix — nine digits, which does not fit in 24
bits. A single shared ceiling would have refused every hotspot on the network,
and the ceiling that looked correct is the one that would have caused it.
`TestRegistrationCeilingAdmitsHotspots` is there to keep it that way.

## Numbering conventions produce advisories, never refusals

The registry issues six-digit repeater IDs and seven-digit operator IDs,
structured on the ITU Mobile Country Code. Underneath, the ID is a flat 24-bit
number and nothing enforces any of that structure; the segregation exists so
radios can filter contact lists by region.

So a registration list naming a bare seven-digit ID is probably an operator ID
where a repeater or hotspot ID belongs, and an eight-digit one is probably a
nine-digit ID with a digit missing. QSP says what it noticed and carries on.
Refusing on a convention would make QSP wrong on the day the convention changed,
and this is a registry's habit rather than a rule of the protocol.

## What QSP will not ship: a network's talkgroup list

It is tempting to bundle BrandMeister's talkgroups as a starting point, and it
is the wrong instinct.

There are on the order of 1,750 of them, and the lists people rely on are
regenerated from BrandMeister's API hourly, so anything committed here is stale
within the week. Worse, the numbers are not portable: a talkgroup number on one
network is not the same talkgroup on another, and DMR-MARC and BrandMeister
differ on numbers in the same range. Bundling one network's list would make QSP
quietly wrong for every a commercial DMR server network, every club network, and everyone who
is not on BrandMeister — which is §0's rule with a specific number attached.

A talkgroup list belongs to the operator. When the admin interface exists it may
offer to *fetch* one from a network the operator names, as data they opt into at
runtime. It does not belong in this repository and it does not belong in the
access engine, which should know about IDs and nothing about who issued them.

The narrow exception worth an advisory rather than behaviour: a few numbers have
conventional meanings across most networks: 4000 to unlink, 9990 for the
parrot, 9 as local, and talkgroups below 90 reserved and not routed between
BrandMeister masters. An operator putting those in a permit list is usually
surprised by what happens, and saying so costs nothing.

## An ACL change takes effect on the next frame, not the next transmission

`SetTable` deliberately lets in-flight transmissions finish, because a
configuration change must not cut somebody off mid-sentence (clarification R4).

**Access control gets the opposite rule.** An operator adding an ID to a deny
list is intervening in something happening now, and a ban that waits politely
for the offender to stop transmitting is not a ban. The two rules differ because
the intents differ, and the difference is recorded here so it does not later
read as an inconsistency to be tidied away.

## Refusals are logged once per stream

Constitution §18 forbids dropping traffic silently. A denied subscriber holding
the key for thirty seconds is roughly five hundred frames, and five hundred
identical log lines is not an explanation. It is an operator's journal rotated
past the evidence they needed, which is the same failure the join-page polling
fix addressed at 0.1.9.

The refusal is logged once, when a stream is first refused, and its frames are
counted. That requires remembering which stream was refused, which is state, and
state does not go in `access`: a peer transmits on one stream per timeslot at a
time, so it belongs beside the peer's existing registration state.

## What this does not do

**Per-peer lists.** There is nowhere to put them. ADR-0012 keeps the peer secret
in a file and QSP has no per-peer record in its configuration at all — one
password, no per-peer identity. Per-peer access control needs the admin
interface and a peers table, and follows them rather than preceding them.

**Console authentication.** `/api/peers` discloses callsigns, radio IDs and
source addresses to anyone who can reach the port, and no DMR ACL affects that.
It is a separate gap, named in PROJECT_MEMORY §8, and this ADR does not narrow
it.

## Consequences

- MSTNAK carries no reason, so a refused operator sees only a rejection. The
  drop reason in QSP's own log is the entire diagnosis, and must name the list
  that refused and the ID it refused.
- Egress adds a range test per destination per frame on the hot path. It is an
  integer comparison against a short slice; the hundred-peer fan-out test is the
  regression check, and if it needs an index later, it can have one.
- A talkgroup refused at egress appears in `Result.Drops`, which the console
  already renders. Access control needs no new plumbing to be visible.
- The existing routing tests are the regression check for the egress hook, as
  they were for repeat. A default-permissive ACL must leave every one of them
  passing unchanged.
