# ADR-0017: Adopting modernc.org/sqlite, the first dependency

**Status:** Accepted
**Date:** 2026-08-26

## Context

ADR-0004 says the core binary depends on the standard library only, and closes
with the terms on which that ends:

> This is a default, not a vow. A dependency may be added when it earns its
> place under §22 — with its purpose, licence, maintenance status and build
> implications documented in a new ADR.

This is that ADR. ADR-0005 already decided *which* driver and *where* it is
registered; what was never written down is the justification for taking the
dependency at all, because until now none was taken.

The forcing function was taken to be BLUEPRINT §16's Phase 3 gate: without
persistence, a restart on day nine loses nine days of evidence.

**That reasoning was wrong, and is corrected here rather than quietly dropped.**
QSP's audit trail is written by `audit.LogRecorder` to the structured log, not
to the database. Nothing inserts into `configuration_versions` or
`audit_events`; the only `INSERT` in the codebase is the migration runner's own
bookkeeping. Soak evidence comes from the journal, and survives a restart
because journald does, not because QSP does.

What the driver actually buys today is narrower and still worth having: the
migration runner and the storage layer execute for the first time in any build,
the schema exists and is proven to survive a restart, and the health check
reports a real database rather than a permanent absence. It is a prerequisite
for persistence, not persistence itself.

The honest justification is therefore ordering rather than urgency. The
dependency has to land before anything can be written to storage, it is easier
to land while the tree is quiet than beside a feature, and it forced a Go
upgrade that was overdue on its own terms. `docs/SOAK.md` states plainly that
the journal is the evidence.

## Decision

The binary registers `modernc.org/sqlite` v1.57.0.

This required moving to Go 1.27 and, in turn, bumping `staticcheck` from
`2024.1.1` to `2026.2.1` — the older release no longer compiles under 1.27.

### Purpose

Persistence for configuration versions and the audit log — the two schemas in
`migrations/`. Without a registered driver, `database.Open` returns
`ErrDriverNotRegistered`, and roughly two hundred lines of migration and storage
code have never executed in any build.

### Licence

BSD-3-Clause, held by The Sqlite Authors. Compatible with QSP's GPL-3.0: a
permissive licence imposes no condition that GPL-3.0 cannot satisfy, and
distributing a combined binary is unencumbered. The transitive dependencies
below are BSD-3-Clause, MIT and Apache-2.0, all similarly compatible.

The upstream repository is `gitlab.com/cznic/sqlite`, with an official mirror at
`github.com/modernc-org/sqlite`.

### Build implications

**This is the property that mattered, and it holds.** `CGO_ENABLED=0` builds
still succeed for linux/amd64, arm64 and armv7. `modernc.org/libc` is C
transpiled to Go rather than bound through it, so no C toolchain is required and
ADR-0001 and ADR-0009 are both intact. That was verified before this ADR was
written, not assumed.

`go.sum` now exists, so CI can cache modules again — `cache: false` in the
workflow existed only because `setup-go` keys its cache on a `go.sum` that had
no reason to exist.

The driver requires Go 1.25 or later, which forced the move to Go 1.27. That was
overdue on its own terms: Go 1.22 had been out of support since around the 1.24
release and was receiving no security fixes.

**Accepted cost:** nine modules in `go.mod` where there were none.

| Module | Role |
|---|---|
| `modernc.org/sqlite` | the driver |
| `modernc.org/libc` | transpiled C runtime it sits on |
| `modernc.org/mathutil`, `modernc.org/memory` | support for the above |
| `golang.org/x/sys` | syscall wrappers |
| `github.com/google/uuid`, `github.com/dustin/go-humanize`, `github.com/mattn/go-isatty`, `github.com/ncruces/go-strftime`, `github.com/remyoudompheng/bigfft` | indirect |

`go mod graph` shows considerably more than nine — `modernc.org/cc/v4`,
`modernc.org/ccgo/v4`, `golang.org/x/tools` and `github.com/google/pprof` among
them. Those are the driver's own build-time tooling, needed to compute the
module graph but **not linked into the binary**. `go list -deps ./cmd/qsp`
reports 8 modernc packages, which is the number that matters for what ships.

**Measured size cost.** The stripped armv7 binary went from 8.4 MB to 12 MB, a
3.6 MB increase. That is the price of carrying a C runtime as Go source, and it
is comfortable on a Pi. Recorded here so nobody has to discover it on the
target.

The supply-chain surface is no longer "the Go standard library". Reviewing a
dependency bump is no longer trivial. `go.sum` must now be part of review.

### Maintenance status

Actively maintained, with releases through 2026 and a `retract` directive
history in `go.mod` showing that broken releases are withdrawn rather than left
in place — a better signal than release frequency alone.

## Consequences

- ADR-0004's decision line is superseded in fact. It is amended rather than
  rewritten, because it records a decision that was correct when taken and is
  still correct as a default.
- The claim to defend was never "no dependencies" for its own sake. It was a
  binary that builds anywhere, needs no C toolchain, and has a surface small
  enough to audit. **That survives**, and QSP's own code remains standard
  library only.
- Documentation saying "zero dependencies" is now false and is corrected. The
  accurate statement is "one direct dependency, pure Go, no cgo".
- If persistence is ever unwanted, `cmd/qsp/driver_sqlite.go` is one file and
  `internal/database` needs no change — which is precisely what ADR-0005 bought.

## Alternatives considered

**Stay on Go 1.22 and pin the driver at v1.34.5**, the newest release compatible
with it. Rejected: it preserves an unsupported toolchain receiving no security
patches, in order to avoid an upgrade that turned out to require no source
changes at all.

**`mattn/go-sqlite3`.** Rejected by ADR-0005 for cgo, and nothing has changed.

**Write a storage backend over `encoding/gob` or flat files.** Rejected: it
trades ten audited modules for a durable-storage implementation of our own, in
the one part of the system where correctness under crash and concurrent access
is hardest to get right.
