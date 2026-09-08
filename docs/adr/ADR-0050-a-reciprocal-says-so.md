# ADR-0050: a reciprocal says so in the token

**Status:** accepted, 2026-09-08

## Context

A peering has two halves. One instance offers, the other accepts and returns a
reciprocal invitation, the first accepts that and the exchange is finished.

Recognising the end of it depended entirely on `offeredPassphrases`, an
in-memory map on the offering instance, and on the operator leaving the
passphrase box empty so that the lookup ran at all:

```go
if strings.TrimSpace(passphrase) == "" && inv.Fingerprint != "" {
    if held, ok := s.offered.take(inv.Fingerprint); ok { ... }
}
```

**Both conditions fail in ordinary use.** The offering operator holds the
passphrase — their own server generated it and showed it to them once — and the
box asking for one is right there, so filling it in was the natural thing to do
and skipped the check entirely. A restart between the two halves emptied the
map, and after that no path ended the exchange at all.

The consequence in both cases is the endless exchange that
[ADR-0032](ADR-0032-peering-is-agreed.md) was meant to close and 0259 was
believed to have fixed: accepting a reply produces another reply, which the page
presents as one more thing to send back, forever. **No instruction from anybody
gets the operator out**, because the page keeps handing them a fresh token.

## Decision

`Invitation` carries `Reply bool`, set by `peering.Reciprocal` and by nothing
else. Accepting an invitation marked as a reply ends the exchange.

The held-passphrase store stays, for what it is actually good at: saving an
operator retyping a secret. It is no longer the only evidence that a peering is
finished, and it is now peeked rather than consumed, so a refused acceptance
leaves it available for the retry.

## The wire-format question

`Decode` sets `DisallowUnknownFields`, so a field added here is one an older
QSP refuses. That is the reason the field is `omitempty`:

- An **offer** has `Reply` false, so it is omitted, and the token is
  byte-identical to what shipped before. An older QSP reads it exactly as
  before.
- A **reciprocal** carries the field. An older QSP refuses it with a decoding
  error rather than misreading it, which is the failure mode the version prefix
  exists to guarantee.

Bumping `QSP-PEER-1.` to `-2.` was the alternative and was rejected: it
invalidates every invitation in flight to fix a case that only arises between
two instances of different ages, one of which is already refusing to take part.

## Consequences

The exchange terminates on evidence carried in the artefact, which is durable,
rather than in a process's memory, which is not. Termination no longer depends
on an operator leaving a field blank.

Two QSP instances of different versions can complete a peering only in one
direction: the newer may accept the older's offer, and the older cannot accept
the newer's reciprocal. Peering is agreed between two administrators who are
already talking to each other, so "upgrade and try again" is a sentence one of
them can say.
