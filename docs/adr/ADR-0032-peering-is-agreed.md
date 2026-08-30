# ADR-0032: A peering is agreed by two people, and QSP can prove it was

**Status:** Accepted
**Relates to:** [ADR-0018](ADR-0018-openbridge.md),
[ADR-0020](ADR-0020-access-control.md), [ADR-0031](ADR-0031-loop-prevention.md)

## Context

Two QSP instances were run on one machine, peered over OpenBridge, and linked
without either being asked to confirm anything. The operator's reaction was the
right one: *I have two instances running and I did not approve a thing on
either.*

The link was in fact consented to. OpenBridge has no connection establishment —
no login, no accept, no session — so a link exists only because both sides hold
a passphrase agreed out of band, and neither administrator can peer without the
other handing them a secret. On one machine with one operator a script wrote
both halves, which made real consent look automatic.

**The consent was real and completely invisible, and invisible consent is worth
very little.** Nothing in a console showed the far end, who agreed to it, or
when. No audit event fired when a link opened. Nothing distinguished a peering
two people negotiated from a line somebody pasted from a forum post. And QSP
accepted `pair-test-passphrase` without complaint.

Between two instances on one desk that is untidy. When club #2 is a different
person's server it is the difference between a network and an open relay.

## Decision

**Nothing changes on the wire.** OpenBridge stays exactly as it is: DMRD frames
with an HMAC-SHA1 signature, no handshake, no registration.

A QSP-only handshake was the obvious alternative and it is the wrong answer.
OpenBridge is worth having because it is what BrandMeister requires and what
HBlink speaks; replacing it with something QSP invented means QSP peers with QSP
and with nothing else, which is the opposite of what a community alternative is
for. ADR-0018 chose this protocol for interoperability and that choice is not
reopened here.

**Instead, the out-of-band exchange becomes an artefact.** `internal/peering`
renders a peering offer as one line of text an administrator sends by whatever
means they already trust. The other administrator pastes it into their console,
sees who is asking and what is proposed, and either accepts it or does not.

The trust is between two licensed operators who know each other. The token does
not create that trust — it makes the thing they are agreeing to specific, and
it makes the agreement recordable.

### The passphrase does not travel with the invitation

An invitation carries the address, the network ID, the callsign, the position,
and the talkgroups proposed in each direction. It carries a **fingerprint** of
the passphrase and never the passphrase.

The invitation is meant to go by email. `internal/hotspot` already refuses to
put a peer password in generated configuration on exactly that reasoning — it
is the one thing that must not travel by email — and it would be incoherent to
apply that rule to a club member and not to a peering that carries the whole
network's audio.

So the two move by different channels. A token forwarded in a mail thread is not
a credential. And a mistyped or stale passphrase fails against the fingerprint
at the moment of pasting, rather than as silence on a link that reports itself
configured — silence being indistinguishable from a firewall, a NAT rebind, or a
far end nobody has started yet.

### QSP generates the passphrase

`NewPassphrase` returns 256 bits from `crypto/rand`. An administrator asked to
invent one produces something closer to `pair-test-passphrase`, and the
passphrase is the entire security boundary: anyone holding it can put audio on
this network as a peer network. A passphrase below 24 characters is refused even
when it matches the fingerprint, because matching is not the property that
matters.

### An invitation expires

Fourteen days. An invitation is a standing offer to send audio to a network, and
one left in a mail archive is as good the day somebody leaves the club as the
day it was written.

Expiry is reported before a passphrase mismatch, deliberately. "This expired"
tells an administrator to ask for a new one; "wrong passphrase" sends them
hunting through an email thread for a secret that would not have worked anyway.

### The reply carries the agreed secret, not a new one

OpenBridge authenticates every datagram against one shared passphrase. The side
accepting an invitation answers with its own address, network ID and proposed
talkgroups, under the secret already agreed. A side that generated its own would
produce a link that carries traffic one way while both ends report healthy —
which is this project's most familiar failure shape.

## Consequences

- **A truncated paste says so.** The token carries a CRC, because half a token
  in an email is the ordinary accident and without a checksum it decodes into a
  plausible-looking invitation with a wrong address. An administrator then
  debugs a link instead of a paste.
- **A newer format is distinguished from rubbish.** `QSP-PEER-2.` tells an
  operator to upgrade; anything else tells them they pasted the wrong thing.
- **Accepting an invitation must write an audit event**, naming the callsign,
  the address and the administrator who accepted. This is the record that did
  not exist, and without it the artefact has not solved the problem it was
  built for.
- **The console must show the far end of every link** — callsign, address, and
  when it was agreed — for the same reason.
- **This does not authenticate the far end's identity.** A callsign in an
  invitation is a claim. It is checkable against RadioID.net by a human, and
  QSP does not check it: an operator who has agreed to peer with somebody has
  already decided who they are dealing with, and a certificate authority for
  amateur radio is not a thing this project is going to build.
- **OpenBridge authenticates but does not encrypt, and has no replay
  protection.** HMAC-SHA1 proves a datagram came from someone holding the
  passphrase and was not altered. It does not hide the audio, which is in the
  clear over the air regardless, and a captured datagram can be sent again
  later. This is inherent to the protocol rather than to QSP, and it is recorded
  here so that nobody has to rediscover it while deciding what a peering means.
- **Nothing here prevents a link that was agreed and later regretted.**
  Disabling one is an edit to configuration, as it was before.
