# Test fixtures

## Rule

**Fixtures are captured from real systems. They are never invented.**

A fabricated packet proves only that our parser agrees with our idea of the
protocol. It cannot reveal that our idea is wrong, which is the failure mode
that actually matters. See Constitution §3 and
[ADR-0008](../docs/adr/ADR-0008-protocol-licensing.md).

## Current fixtures

| File | Contents |
|---|---|
| `hbp/hbp-login-session.pcap` | Full HBP login handshake plus keepalives, two implementations |
| `hbp/hbp-voice-session.pcap` | Seven complete voice streams with headers and terminators |
| `p25/p25-gateway-idle.pcap` | P25Gateway polling only — **no voice**, insufficient for a parser |

Each has a sibling `.md` recording provenance, structure, sanitization and
expected parser behaviour. Read it before using the fixture.

## Layout

```
testdata/
├── hbp/    DMR Homebrew Protocol captures  — phase 1
└── p25/    P25 reflector captures          — phase 4
```

## Requesting a capture

[`CAPTURE-REQUEST.md`](CAPTURE-REQUEST.md) is a self-contained document that can
be forwarded to an operator willing to capture traffic. It assumes no knowledge
of this project.

## Contributing a capture

Each fixture needs a sibling `.md` recording:

- **Source** — which software or hardware produced it, and what version
- **Date** — when it was captured
- **Scenario** — what was happening (registration, a voice call, a keepalive,
  a malformed frame)
- **Expected behaviour** — what a correct implementation should do with it
- **Provenance** — that it was captured from a system you operate, or supplied
  by an operator who consented to its use here

Redact anything identifying beyond callsigns and radio IDs, which are public by
their nature.

Malformed and hostile frames are as valuable as well-formed ones. A parser that
handles only valid input has not been tested.
