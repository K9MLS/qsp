# ADR-0034: P25 is a network of its own, and audio is never transcoded to reach it

**Status:** Accepted
**Relates to:** [ADR-0008](ADR-0008-protocol-licensing.md),
[ADR-0010](ADR-0010-protocol-codec-shape.md), [ADR-0028](ADR-0028-parrot.md),
[ADR-0029](ADR-0029-ipsc-from-capture.md)

## Context

QSP carries DMR. `internal/protocol/p25` is six lines saying P25 is not
implemented and is blocked on captured traffic and on ADR-0008's licence
question. The health report lists `p25` as a subsystem reporting *unavailable*
rather than absent, so the shape of the program has always assumed it arrives.

Two things have changed. Clubs are asking to connect, and **the operator has
stated the rule this decision has to satisfy: audio is king.** The best audio
that can be delivered to the amateur community is the first requirement and
overrules everything else, including features, convenience and this document.

That rule is not decoration here. It decides the architecture, and it decides it
against the design most cross-mode software has chosen.

### The vocoders are different, and that is not an implementation detail

P25 Phase 1 carries IMBE. DMR carries AMBE+2. They are different lossy codecs
with different frame sizes and different bit allocations, and neither can be
reinterpreted as the other.

Carrying a P25 call onto a DMR network therefore means decoding to audio and
encoding again — tandem vocoding. **That always sounds worse.** It is not a
quality of implementation that a careful program can avoid; it is what happens
when a lossy codec's output is fed through another lossy codec. Every cross-mode
gateway in amateur radio does this, and every one of them is audibly degraded.

So a design that routes P25 through DMR to reuse the existing routing core would
be simpler to build, would demonstrate well, and would make every P25 call worse
than it needs to be. Audio is king. That design is refused.

### A club running only P25 is not a degraded DMR club

**Most clubs will run one or the other.** Some will install QSP for DMR and never
touch P25; some will install it for P25 and never touch DMR. A P25-only club is
not a special case, a compatibility mode, or a DMR network with the DMR removed.
It is the whole product for that club, and it has to be as good as the DMR one.

That rules out an architecture where P25 is a translation layer over a DMR core:
under it, the P25-only club pays a transcode for traffic that never touches DMR
at all, which is the worst possible outcome for the largest group it serves.

## Decision

**P25 is a native network, carried the way DMR is carried: bytes in, bytes out,
nothing decoded.**

A P25 call between P25 endpoints crosses QSP without a vocoder, exactly as a DMR
call does today. ADR-0028's guarantee — that QSP carries bursts it never
understands — is extended to P25 rather than broken for it. The audio a club
hears is the audio their radio produced, unchanged.

### One binary, both networks, chosen by configuration

Not a build flag and not a question asked at install time.

An install-time choice is irreversible in practice: a club that adds a P25
repeater next year should not reinstall, and two builds mean two things to test
and two ways to be wrong. **A listener is enabled by configuration, the same as
every other subsystem**, and a club runs DMR, or P25, or both, by editing it.

The health report already reports each subsystem separately, so a P25-only
instance says plainly that DMR is off rather than appearing broken.

### Bridging the two is explicit, opt-in, and states its cost

A club that genuinely wants P25 and DMR members to hear each other can have it.
It cannot have it by accident.

Turning it on requires saying so, and the setting says what it costs: **audio
crossing between the two networks is decoded and re-encoded, and sounds worse
than audio that stays on one.** A club that accepts that has made an informed
trade; a club that has not been told has been quietly given the worst audio QSP
can produce.

This is also why it is not the default. The default is the rule.

### Nothing here is built from reading somebody else's implementation

ADR-0008 settled it and ADR-0029 restates why: reading a GPL implementation to
learn a protocol binds this project to a derivative-work licence from the moment
it is read, irreversibly, whether or not a line is copied. P25 is built from
captured traffic and published specification, in that order, exactly as the
homebrew work was.

## Consequences

- **A voice capture is the blocker, and it is the only blocker.**
  `testdata/p25/p25-gateway-idle.pcap` covers the idle path — 155 packets of
  P25Gateway polling and status, captured 2026-08-23 — and contains no voice. A
  capture of a P25 call through P25Gateway unblocks the rest.
- **The routing core is protocol-shaped already.** It routes an endpoint, a
  talkgroup and a timeslot; P25 has talkgroups and no timeslots, so a P25
  endpoint fixes the slot rather than needing a second core. What must not
  happen is P25 traffic being converted into DMR frames to reuse the DMR path,
  which is transcoding wearing an architecture diagram.
- **Talkgroup numbering is separate.** A P25 talkgroup 3148 and a DMR talkgroup
  3148 are different conversations on different networks, and QSP must not
  assume otherwise — the same reasoning that keeps a club's numbers out of
  QSP's code entirely.
- **A vocoder is still not shipped.** Bridging, when a club asks for it,
  orchestrates the administrator's vocoder rather than embedding one, which is
  BLUEPRINT §7's position and unchanged by this.
- **The console must show which networks an instance carries.** A page that
  assumes DMR is wrong for the P25-only club, and that club is not a minority
  case to be handled later.
