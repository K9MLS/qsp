# ADR-0005: SQL driver registered by the binary, not the storage package

**Status:** Accepted

## Context

QSP needs SQLite. The mature driver, `mattn/go-sqlite3`, requires cgo, which
would break static linking and cross-compilation — both load-bearing for ADR-0001.
`modernc.org/sqlite` is a pure-Go implementation and is meaningfully slower.

Separately: if `internal/database` imported a driver directly, that dependency
choice would be embedded in the middle of the program, and every test of the
storage layer would pull it in.

## Decision

`internal/database` uses `database/sql` and **imports no driver**. The driver is
registered by the binary in `cmd/qsp`, and the driver name is configuration.

The intended driver is `modernc.org/sqlite`, chosen for pure Go.

When the configured driver is absent, `Open` returns `ErrDriverNotRegistered`
with the list of drivers that *are* registered and a pointer to this ADR.
Startup continues without persistence and the health check reports the database
as `unavailable` with the reason.

## Consequences

- The storage layer is testable without a database engine.
- Swapping drivers is a configuration change plus one import.
- **This build registers no driver at all** — the module proxy was unreachable
  when the foundation was written (ADR-0004). Adding it is two lines:

  ```go
  import _ "modernc.org/sqlite"   // in cmd/qsp
  ```
  ```sh
  go get modernc.org/sqlite
  ```

  Until then, migration *execution* is untested. The migration *planner* —
  ordering, checksums, gap detection, downgrade refusal — is fully tested
  without a driver.
- The pure-Go performance penalty is acceptable **only while invariant I2 holds**:
  the routing hot path never touches the database. Wanting a database read
  inside a routing decision is the signal to revisit this ADR, not to work
  around it.
