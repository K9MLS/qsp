# ADR-0010: Protocol codecs parse but do not interpret

**Status:** Accepted

## Context

While implementing the HBP codec against captured traffic, one message turned
out to be genuinely ambiguous on the wire.

`RPTACK` is ten bytes carrying a four-byte payload. Immediately after `RPTL` that
payload is an authentication salt. After `RPTK` or `RPTC` it is the peer's own
repeater ID echoed back. **Nothing in the packet distinguishes the two cases** —
in the captured session the identical shape carried salt `0x9947b430` and, twice,
repeater ID 3132910. Only the connection's state tells them apart.

This forced a decision that applies to every protocol QSP will speak.

## Decision

**A codec reports what the bytes say. It does not decide what they mean.**

Concretely:

- `Ack` exposes the raw four-byte payload and offers `Salt()` and `RepeaterID()`
  as *interpretations*. It does not choose between them.
- `Data.DataType` is exposed as a raw nibble rather than decoded into an enum,
  because its meaning depends on frame type and was not established from the
  capture.
- `Data.Trailing` is preserved verbatim. MMDVMHost appends two bytes understood
  to carry link quality; their layout was not established, so they are carried
  through untouched rather than parsed.
- Message kinds known to exist but never captured are rejected with
  `ErrNotCaptured`, naming the capture that would be needed.

Interpretation belongs to the peer state machine, which knows what it last sent.

## Consequences

- **The codec is stateless and trivially testable.** It has no connection
  context to set up, which is why every frame in both fixtures can be replayed
  through it in a single loop.
- **Round-tripping is exact.** Because nothing is discarded or normalised, a
  parsed frame re-serialises byte-for-byte. This is enforced by test over every
  captured frame and by a fuzz property. It matters because QSP relays: a lossy
  parse would corrupt traffic passing through, not merely misreport it.
- **Ambiguity is visible in the API** rather than resolved by a guess buried in
  a parser. A caller cannot accidentally treat a salt as a repeater ID without
  writing the line that does so.
- **Accepted cost:** the state machine (M9) carries more responsibility, and
  `Ack` is slightly awkward to use. That awkwardness is the protocol's, and
  hiding it would not remove it.

## What this ruled out

An earlier shape had `Parse` take a connection-state argument so it could return
`Salt` and `Ack` as distinct types. That was rejected: it would make the codec
untestable without constructing fake connection state, and it would put the
consequences of a state bug inside the parser, where they are hardest to see.
