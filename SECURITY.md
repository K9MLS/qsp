# Security Policy

## Reporting a vulnerability

**Please do not open a public issue for a security vulnerability.**

Report it privately through GitHub's private vulnerability reporting on this
repository. Include what you found, how to reproduce it, and what an attacker
could achieve.

Expect an acknowledgement within a week. QSP is maintained by volunteers and
there is no SLA, but security reports are the first thing looked at.

## Scope

QSP is **hobbyist software for amateur radio**. It carries no warranty and must
never be used in public safety or life safety critical applications.

That said, a QSP instance is typically internet-facing, accepts UDP from
anywhere, and controls a club's repeater network. Vulnerabilities are taken
seriously.

### In scope

- Remote crash or hang from network input
- Memory or goroutine exhaustion from network input
- Authentication or authorisation bypass
- Injection of any kind
- Credential disclosure, including via logs or the audit trail
- Path traversal or unsafe file access
- Unauthorised routing changes
- Replay attacks against peer identity

### Out of scope

- Anything requiring physical access to the server
- Denial of service by overwhelming the host's bandwidth
- Missing hardening headers with no demonstrated impact
- Vulnerabilities in a reverse proxy the operator deployed in front of QSP

## Security model

### Assumptions

1. **Every network packet is hostile.** Parsers validate lengths, identifiers,
   ranges and state before acting. Malformed input must never panic. Parsers are
   fuzzed.
2. **The console may be exposed.** A strict Content-Security-Policy with no
   `unsafe-inline` is applied; the console is built to satisfy it.
3. **TLS is terminated by a reverse proxy.** QSP does not implement TLS. It
   honours forwarding headers **only** when the operator has declared
   `behind_proxy` — otherwise any client could forge its address in the audit
   trail.
4. **Handlers can have defects.** Panics are recovered into a 500 so that one
   broken console endpoint cannot drop a bridge carrying live traffic.
5. **Secrets reach logs by accident.** `audit.Redact` matches key names
   over-eagerly by design.

### Credentials

Passwords are hashed with PBKDF2-HMAC-SHA256 at 600,000 iterations with a
16-byte random salt. Hashes are self-describing, so the algorithm and cost can
be upgraded without invalidating existing passwords; see
[ADR-0006](docs/adr/ADR-0006-password-hashing.md).

Work per verification attempt is bounded so that an unauthenticated caller
cannot force arbitrary CPU consumption.

### Not yet implemented

Sessions, roles and authorisation are designed before any state-changing
endpoint exists. **There are none today** — the current build exposes only
`/healthz`, `/readyz`, `/api/events`, `/api/peers` and static console assets,
all read-only.

`/api/peers` returns callsigns, radio IDs and peer source addresses. Like every
other endpoint it is unauthenticated, which is a further reason to follow the
deployment guidance below rather than exposing the console directly.

### Deployment guidance

- Bind the console to `127.0.0.1` and reach it through a reverse proxy with TLS.
- Do not expose the console directly to the internet.
- Set `behind_proxy` only when a proxy you control is genuinely in front.
- Back up the configuration export before every upgrade
  ([ADR-0007](docs/adr/ADR-0007-schema-downgrade.md)).
