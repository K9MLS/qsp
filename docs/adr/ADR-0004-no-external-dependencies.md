# ADR-0004: No external dependencies in the core

**Status:** Accepted, amended 2026-08-26 by [ADR-0017](ADR-0017-first-dependency.md)

## Context

Constitution §22 requires every dependency to justify itself. Separately, the
environment in which this foundation was built had no access to the Go module
proxy, which forced the question earlier than it would otherwise have arisen.

## Amendment, 2026-08-26

The binary now registers `modernc.org/sqlite`, bringing ten modules. The
decision below is left as written because it was correct when taken and remains
the default for everything else — QSP's own code is still standard library only.
See [ADR-0017](ADR-0017-first-dependency.md) for the justification required by
the last bullet of this document.

## Decision

The core binary depends on the Go standard library only.

Specifically not adopted:

| Not used | Standard library instead |
|---|---|
| HTTP router / web framework | `net/http` with `http.ServeMux` and Go 1.22 method patterns |
| Logging library | `log/slog` |
| Assertion library | table-driven tests with `testing` |
| Migration library | ~200 lines in `internal/database` |
| ORM | `database/sql` |
| UUID library | `crypto/rand` and `encoding/hex` for correlation IDs |
| PBKDF2 from `x/crypto` | RFC 8018 over `crypto/hmac` (ADR-0006) |

## Consequences

- The supply chain attack surface is the Go standard library.
- `go.mod` has no `require` block. Reviewing dependency changes is trivial
  because there are none. *(No longer true as of ADR-0017; `go.sum` is now part
  of review.)*
- **Accepted cost:** a small amount of code we maintain ourselves — PBKDF2 and
  the migration runner most notably. Both are well specified and thoroughly
  tested; PBKDF2 is verified against externally generated vectors.
- This is a default, not a vow. A dependency may be added when it earns its
  place under §22 — with its purpose, licence, maintenance status and build
  implications documented in a new ADR.
