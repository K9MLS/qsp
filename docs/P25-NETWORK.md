# P25 as a network, not a reflector

**Written 2026-09-12**, after the operator asked whether QSP could be a P25
repeater linking network as capable as its DMR side, and whether it can carry
both at once.

This document exists because that assessment was reached in conversation and
would otherwise be lost. It is an assessment and a plan, not a decision;
[ADR-0057](adr/ADR-0057-p25-is-a-full-network.md) and
[ADR-0060](adr/ADR-0060-qsp-terminates-the-serial-tunnel.md) hold the
decisions.

## The short answer

**Yes, and it is a fraction of what the DMR side cost — with one unsolved
problem and one permanent limitation.**

Four or five sessions of real work, not a second three hundred patches. But
not a straightforward port either, and the two caveats are in §4 and §5 below
rather than buried.

## 1. Can it carry both at once?

**Concurrently: yes, and it is already proven.** On 2026-09-12 the test server
carried a DMR link from production and a P25 gateway from a Pi-Star at the
same time — separate sockets, separate counters, no interaction. Nothing
needed to be done to make that work, because the two listeners share nothing.

**Interoperably: deliberately not, and that should not change.** A DMR
talkgroup and a P25 talkgroup being one conversation means IMBE to AMBE
transcoding. [ADR-0034](adr/ADR-0034-p25-is-native.md) is explicit that a
vocoder is not shipped and that bridging, when a club asks for it, orchestrates
the administrator's own rather than embedding one; `docs/CAPABILITIES.md`
records the mechanism, an external transcoder with an AMBE dongle. Cross-mode
stays opt-in and outside the binary.

**And that is what makes the ambition cheap.** A P25-only network never needs a
vocoder at all: P25 to P25 is byte copying, per
[ADR-0034](adr/ADR-0034-p25-is-native.md). QSP could be a serious P25 linking
network with zero audio processing, which nothing free offers today.

## 2. What the two sides actually have

| | DMR | P25 |
|---|---|---|
| Talkgroup routing | yes | **no — flat relay** |
| Access lists | yes | callsign allow-list only |
| Per-talkgroup attachment | yes | no |
| Loop prevention | yes | n/a, no links |
| Deduplication | yes | no |
| Server-to-server links | yes | **no** |
| Bridges | yes | no |
| Call tracker and Last heard | yes | **no** ([ADR-0059](adr/ADR-0059-p25-in-last-heard.md)) |
| Contention | per timeslot | **undefined** |
| History | yes | no |
| Parrot | yes | yes |

The Quantar is one row of that table. Everything else is the network, and none
of it needs hardware — which makes it the right work while a V.24 card is on
order.

### The flat relay is the first thing to fix

`voice` in `internal/p25link/serve.go` builds its target list as every gateway
except the sender. **The talkgroup is read and then not used for routing.** So
what exists today is a single flat reflector: every registered gateway hears
every transmission, whatever talkgroup it is on.

That is adequate for one server and two hotspots. It is not a network, and on
any real one it is both wrong — a gateway on TG 10100 receiving TG 10297 audio
— and expensive, see §6.

## 3. What is genuinely easier here than it was for DMR

**The protocol is already understood.** `internal/protocol/p25` round-trips all
565 frames of `testdata/p25/p25-voice.pcap` byte for byte, the talkgroup and
source radio are located and confirmed against the operator's APX across four
talkgroups, and a live P25Gateway produced **zero** unparsed datagrams out of
837 on the first attempt. Most of the DMR side's effort went into reaching that
point.

**The core is reusable rather than rewritable.** Access lists, deduplication,
the call tracker, history and the link protocol are mode-independent ideas
wearing DMR-shaped types. Generalising types is a smaller job than rebuilding
behaviour.

## 4. The unsolved problem: P25 has no stream identifier

Every DMR frame carries a stream ID. It is how a transmission is identified,
tracked, ended and deduplicated, and it is one third of `calls.Key`.

**P25 has no equivalent.** The transmission boundary is a terminator, not an
identifier repeated in every frame. So "which transmission is this frame part
of" must be inferred from sequence and timing rather than read.

That is survivable on one server. It is the open question for links and
deduplication, both of which need a handle on a *specific* transmission that
means the same thing at both ends. A locally-assigned identifier is the obvious
direction and immediately raises the question of what it means on the far side
of a link.

**This is the one part of P25 parity with no known answer yet**, and it should
be solved on paper before any link code is written.

## 5. The permanent limitation: a P25 gateway does not authenticate

There is no login on the reflector protocol — a gateway asserts a callsign in a
poll and QSP believes it (patch 0330, and `Gateway.Callsign` says so in its
own comment). DMR peers exchange a password.

For [ADR-0052](adr/ADR-0052-qsp-is-federated.md)'s federation of sovereign
servers that is a materially weaker trust model, and **it is a property of the
protocol rather than of QSP**, so no amount of better code removes it. It bounds
what a P25 access list can honestly claim, and any documentation of P25 access
control has to say that a callsign is a claim.

## 6. Two capacity facts to know before it scales

**P25 is about three times the datagram rate of a DMR timeslot.** Nine frames
per logical data unit at roughly 50 per second, against DMR's ~17 per timeslot.
The live measurement agrees: about 573 frames for roughly eleven seconds of
audio.

**Relay cost is fan-out, and today it is fan-out to everybody.** Each frame is
written once per target. Fifty gateways means forty-nine writes per frame —
2,450 packets a second for one person talking, most of them to gateways on
other talkgroups. Talkgroup routing is not only correctness; it is what keeps
the cost proportional to a conversation rather than to the network.

## 7. Contention is undefined

DMR holds a timeslot: a second transmission to a busy slot is refused, and that
refusal is what the COLLISIONS counter exists to report. P25 has no timeslot.

If two gateways key the same talkgroup simultaneously today, both streams are
relayed to everybody and interleave, and the audio is ruined for every
listener. Nothing refuses and nothing counts it.

The obvious rule is first-keyup-wins per talkgroup, mirroring DMR's per-slot
rule — and it cannot be implemented before §2's talkgroup routing exists,
because there is no per-talkgroup state to hold.

## 8. A link cannot carry P25, and this is not a small extension

**Checked 2026-09-12 rather than assumed.** `upstream.Link.Send` takes an
`hbp.Data` and calls `openbridge.Encode`, and an OpenBridge datagram is a fixed
53-byte DMR frame plus a 20-byte signature — exactly 73 bytes, with **no type
field and no room for one**. `Encode` forces `hbp.Timeslot1` because OpenBridge
has a single slot.

So there is nowhere to hang a second frame type. Carrying P25 between servers
needs either a QSP-native link format alongside OpenBridge or a second socket,
and that is a federation decision rather than an implementation detail: it
determines what two admins have to agree on to connect, which
[ADR-0052](adr/ADR-0052-qsp-is-federated.md) says should be as little as
possible.

## 9. The order of work

Staged so that each step informs the next, and so the riskiest change is made
with evidence rather than in anticipation.

1. **Talkgroup routing and contention inside `internal/p25link`.** Self-
   contained, no core changes, closes the flat-reflector defect, and it reveals
   what P25 actually needs before anything is generalised. **Needs no
   hardware.** Worth doing whatever is decided about the rest.
2. **Answer §4 on paper.** What identifies a P25 transmission, and what that
   identifier means at the far end of a link.
3. **An ADR for the mode-agnostic key**, answering
   [ADR-0059](adr/ADR-0059-p25-in-last-heard.md) and the link question
   together, because they are the same problem and should not be decided
   twice. This is the change most likely to break something audible: it touches
   `calls.Key` and `routing.Endpoint`, including the contention and text-merge
   logic that has produced several of this project's worst defects, on a server
   carrying a live network.
4. **The link format decision** from §8, then P25 over links, which should
   mostly fall out of (3).

Steps 1 and 2 need nothing but time. Step 3 wants a quiet network.

## What this document is not

It is not a promise that P25 parity is straightforward. §4 has no answer yet,
§5 has no answer ever, and §3's optimism rests on the protocol work already
being done rather than on the network work being easy.
