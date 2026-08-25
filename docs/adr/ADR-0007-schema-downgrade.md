# ADR-0007: Refuse to run against a newer schema

**Status:** Accepted

## Context

Raised in the phase 0 architecture review as D7. An operator upgrades QSP, hits
a bug, and downgrades. The database now carries migrations the older binary does
not know about.

Options: attempt to run anyway (writes to a schema we misunderstand, corrupting
data); support down-migrations (doubles migration authoring effort and is
frequently impossible without data loss); or refuse.

## Decision

Refuse, and say exactly what happened.

`Pending` compares applied versions against those the binary knows. If the
database has a higher version, it returns an error telling the operator that the
database was created by a newer QSP, and to upgrade or restore a backup taken
before the upgrade.

There are no down-migrations.

## Consequences

- A downgrade is a clear, immediate, well-explained failure at startup instead
  of silent corruption discovered later.
- **Backup before upgrade is a documented operational requirement**, not a
  suggestion. The configuration export exists partly for this.
- Migration authoring stays cheap: forward only.
- A migration edited after being applied is caught by the same mechanism, via
  checksum comparison, with instructions to add a new migration instead.
