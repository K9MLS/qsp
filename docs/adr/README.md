# Architecture Decision Records

Each file records one decision: the context, the options, the choice, and the
consequences we accepted.

An ADR is never edited to change its decision. A decision that turns out to be
wrong gets a new ADR that supersedes the old one, and the old one is marked
superseded. The history of why we thought something is as useful as the
conclusion.

| ADR | Decision | Status |
|---|---|---|
| [0001](ADR-0001-go.md) | Go for the core | Accepted |
| [0002](ADR-0002-single-writer-routing-core.md) | Single-writer routing core | Accepted |
| [0003](ADR-0003-event-bus.md) | Sequenced event bus with bounded replay | Accepted |
| [0004](ADR-0004-no-external-dependencies.md) | No external dependencies in the core | Accepted |
| [0005](ADR-0005-sqlite-driver.md) | SQL driver registered by the binary, not the storage package | Accepted |
| [0006](ADR-0006-password-hashing.md) | PBKDF2-HMAC-SHA256 with a self-describing hash format | Accepted |
| [0007](ADR-0007-schema-downgrade.md) | Refuse to run against a newer schema | Accepted |
| [0008](ADR-0008-protocol-licensing.md) | Protocol implementation sources | Open — interim rules in force |
| [0009](ADR-0009-cgo-isolation.md) | cgo connectors ship as separate binaries | Accepted |
| [0010](ADR-0010-protocol-codec-shape.md) | Protocol codecs parse but do not interpret | Accepted |
| [0011](ADR-0011-nat-rebind.md) | A source address change requires re-authentication | Accepted — needs field validation |
| [0012](ADR-0012-peer-password-file.md) | The peer password lives in a file, not the configuration | Accepted |
| [0013](ADR-0013-routing-decision-is-pure.md) | The routing decision is a pure function | Accepted |
| [0014](ADR-0014-contention.md) | A transmission occupies its origin as well as its destinations | Accepted |
| [0015](ADR-0015-level-triggered-scheduler.md) | The scheduler is level-triggered, and stores wall time | Accepted |
| [0016](ADR-0016-ptt-triggered-bridging.md) | PTT-triggered bridging, and how it merges with the schedule | Accepted |
| [0017](ADR-0017-first-dependency.md) | Adopting modernc.org/sqlite, the first dependency | Accepted |
| [0018](ADR-0018-openbridge.md) | OpenBridge for linking to other networks | Proposed |
| [0019](ADR-0019-master-repeats.md) | A master repeats; bridging is a layer on top | Accepted |
| [0020](ADR-0020-access-control.md) | Access control, and why it is checked in two places | Proposed |
| [0021](ADR-0021-private-calls-and-data.md) | Private calls and data are in scope, and share one missing thing | Proposed |
| [0022](ADR-0022-timeslot-contention.md) | Contention belongs to the timeslot, not the talkgroup | Proposed |
