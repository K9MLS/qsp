# ADR-0009: cgo connectors ship as separate binaries

**Status:** Accepted

## Context

The Zello connector needs Opus. No pure-Go Opus encoder of adequate quality
exists, so it requires cgo — which would break the static linking and
cross-compilation that justified choosing Go (ADR-0001).

## Decision

The core `qsp` binary is pure Go and never links cgo.

Connectors requiring cgo ship as separate binaries in the same repository and
container image: `cmd/qsp-zello`. They communicate with the core over **USRP**,
the UDP PCM protocol every analog system in this ecosystem already speaks.


## Consequences

- The core stays statically linked and cross-compilable. A club not using Zello
  never encounters cgo, and a Zello API change cannot break the core.
- **No new protocol is invented for the boundary.** USRP has to be implemented
  anyway for AllStar; reusing it costs nothing and keeps one integration surface
  rather than two.
- **Accepted cost:** two processes to supervise when Zello is in use. Compose
  handles it; the health check reports the connector's reachability so the
  operator is not left guessing.
- This pattern is the template for any future dependency that cannot be pure Go.
