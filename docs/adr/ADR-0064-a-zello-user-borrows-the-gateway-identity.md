# ADR-0064: A Zello user borrows the gateway's identity, and identifies by voice

**Status:** Accepted
**Relates to:** [ADR-0062](ADR-0062-as-much-as-possible-in-qsp.md),
[ADR-0063](ADR-0063-a-transcoder-is-a-routing-destination.md),
[ADR-0059](ADR-0059-p25-in-last-heard.md)

## Context

[ADR-0062](ADR-0062-as-much-as-possible-in-qsp.md)'s fourth item is identity
both directions: a transcoded transmission should appear in Last heard as a
station rather than an anonymous burst, and a DMR talker's callsign should
reach the far side.

The DMR side of that is not optional. **Radios embed the source ID in every
burst** — the voice LC header, the terminator and the embedded LC all carry
it, which is how QSP reads a transmission today. A radio has an ID because
somebody programmed one into it. A Zello user has no radio, so there is no ID
to embed and QSP has to supply one. What it supplies is this record.

Three options were considered and the argument that settled it was not
technical.

## Decision

**A Zello user transmits under the gateway's own DMR ID, with an optional
administrator-set alias, and identifies themselves by voice.**

Four parts, and the separation is the point:

### 1. Admission is the Zello channel's job, not QSP's

A moderated Zello channel already decides who may be present. Zello's own
tiers are explicit: on a Zelect channel a new user may only listen until a
moderator marks them trusted, and on Zelect+ they may neither listen nor talk
until approved. Added to the channel means trusted means permitted.

QSP does **not** keep a second allow-list of Zello usernames. An earlier draft
of this decision did, on the grounds that Zello cannot record a callsign and
QSP can. That was rejected: two gates deciding the same question is the second
code path [ADR-0052](ADR-0052-qsp-is-federated.md) warns about, and the one
fewer people use is the one that rots. The channel's moderators are the
operator's own admins, so the list would have recorded the same decision
twice.

**BrandMeister reached the same place**: they require moderated channels —
SELECT or SELECT+ access only — citing radio regulations, rather than asking
for proof of licence.

### 2. The gateway has its own DMR ID, and QSP never invents one

One ID per transcoder channel, configured, and **not shared with a repeater or
a hotspot**. Sharing the XPR8300's 999999 or the Pi-Star's 3132910 would make
Zello traffic indistinguishable from that machine's own in Last heard and in
the logs of every linked server.

**QSP does not mint IDs, and this is the part to hold firm on.** A block of
synthetic IDs allocated to Zello usernames would give each user a distinct
station — and would eventually collide with a real radio on a linked network,
attributing a stranger's transmission to somebody's callsign. That is the same
reasoning [ADR-0059](ADR-0059-p25-in-last-heard.md) used to refuse synthetic
DMR keys for P25, and it applies with more force here because the identity
would be asserted on RF rather than only in a database.

An additional ID for a gateway is issued against a callsign by the usual
registry, so the operator obtains one the ordinary way.

### 3. The alias is administrator-set, optional, and never self-asserted

DMR has an in-band text field for this. **Talker Alias** was added to the ETSI
DMR specifications in 2016 and behaves like D-STAR's free text: a radio sends
text data during a voice call. Motorola calls it Inband Caller Alias and
allows a user-defined string of up to 31 characters with every voice call. The
specification is TS 102 361-2, and generating one in a gateway is established
practice rather than an invention — BrandMeister generates Talker Alias for
its own gateway applications, including its Simple Application Protocol,
AutoPatch, D-STAR and Fusion gateways.

**It must come from configuration and only from configuration.** An earlier
draft had the alias carry the Zello username. That was rejected on the
operator's objection, and the objection is decisive: **Zello display names are
set by the user**, so a Zello user could rename themselves to a licensed
operator's callsign and appear on that operator's repeater as them. Identity
spoofing, handed over for free.

So:

- where the administrator has mapped a Zello user to a callsign, the alias
  carries that callsign
- where they have not, the alias carries a fixed configured string or nothing
- **never** a string the Zello user chose

Off by default, because the operator knows which of their radios display it
and QSP does not.

### 4. Identification is the operator's obligation, by voice

§97.119(b) lists the ways a call sign may be sent: CW, phone in English, RTTY
using a specified digital code where part of the communication is a RTTY or
data emission, or image. A DMR voice call is a phone emission and Talker Alias
is embedded signalling riding inside it. **The alias is display data, not
station identification** — a receiving radio may not support it, may have it
switched off, and a network may strip it.

The operator identifies by voice, exactly as they do on an EchoLink-equipped
repeater and exactly as they do on analog: you are on the air, so you
identify. **QSP records and displays; it does not police.** An earlier draft
refused to put unmapped audio on RF at all, which was a piece of software
solving a regulation the operator's own mouth solves.

## The responsibility this leaves, named on purpose

**The licensing judgement rests with whoever moderates the Zello channel.**

§97.115 is where this matters. Third-party participation requires a control
operator present at the control point, continuously monitoring and
supervising; and §97.115(c) bars third-party traffic from an automatically
controlled station altogether, except for RTTY or data. A repeater is
automatically controlled. So an unlicensed Zello user on a repeater is not
comfortably within the rules, while a licensed one is arguably not a third
party at all.

Nothing in the software can tell those two apart. **Zello has no field for a
callsign or a licence** — trusted there means a moderator approved somebody,
not that they hold a licence. EchoLink differs here: it will not issue an
account without callsign validation, so "trusted on EchoLink" carries an
implicit "licensed" that "trusted on Zello" does not.

This is written down so that a future operator reading it knows where the
responsibility sits, rather than assuming the software checked something. It
checked nothing of the kind, and could not.

## Consequences

**A whole subsystem is not built.** Because the source ID is real, Last heard
needs no second tracker and no new key for the Zello side — ADR-0059's problem
does not arise, since that was about P25 having no DMR ID at all.

**Every Zello user is one station in Last heard**, distinguished only by the
alias where one is set and by their voice otherwise. That is the accepted cost
of not minting IDs, and it is the same cost an analog phone patch has always
had.

**The P25 side gets radio ID only.** Talker Alias is a DMR feature and P25 has
no equivalent in common use, so a transcoded transmission toward P25 carries
the gateway's ID and nothing else. ADR-0058 gates P25 work anyway.

**Talker Alias generation is real work and is not in this record.** It is
embedded link control in the voice superframe, and while `internal/dmrfec`
already has the EMB code, the LCSS positions, the Reed-Solomon parity and the
BPTC machinery, the Talker Alias link-control messages themselves need
TS 102 361-2 in hand.

**That specification has to be a file, not a recollection.** PROJECT_MEMORY
§8r records what guessing at a field's length cost: a wedged dongle, recovered
only by removing its power. ADR-0040 permits published standards for the DMR
air interface, so the document is allowed; what is not allowed is writing
FLCO values from memory and finding out on the air.

## Alternatives considered

**Synthetic per-user DMR IDs.** Rejected: collides with real radios on linked
networks, and asserts an invented identity on RF. ADR-0059's reasoning.

**Mandatory callsign mapping, no mapping no RF.** Rejected on the operator's
argument: it enforces in software an obligation that the operator's voice
already discharges, and would stop a club pointing a Zello channel at its own
network without first enumerating every member.

**An allow-list in QSP in addition to the channel.** Rejected as a second
gate deciding a question the channel already decides, recording the same
admin's decision twice.

**Deriving the alias from the Zello username.** Rejected as identity
spoofing: the user controls that string.
