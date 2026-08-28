# ADR-0027: The file stays the source of truth, and the owner applies the change

**Status:** Proposed

## Context

QSP has an authenticated administrator and no endpoint that changes anything.
The admin interface is the last item the parity document marks against it, and
everything it needs exists in pieces: `configuration_versions` migrates,
`audit_events` has an actor, `Config.Validate` reports every problem at once,
and `SetTable` and `SetAccess` apply a change without a restart.

Three questions have to be answered before any of it is wired together, and none
of them is about forms.

## 1. Where does the configuration actually live?

QSP reads a JSON file named by `-config`. `configuration_versions` stores a
snapshot of every save. Those cannot both be the source of truth.

**The file stays the source of truth, and a save writes it.**

The alternative — the database becoming authoritative after the first save —
means a hand-edited file is silently ignored from then on. That is a trap laid
for exactly the operator this project is written for: somebody comfortable in a
text editor, who will edit the file, restart, and find their change gone with no
error to explain it. The blueprint says configuration is ultimately edited
through the web UI; it does not say the file becomes a decoration.

`configuration_versions` is therefore history rather than state. It answers
"what changed, when, and who did it" and is what a rollback reads *from*, but a
rollback works by writing the file again.

The consequences are worth stating rather than discovering:

- **The configuration file must be writable by the service user.** If it is not,
  saving fails, and it fails with a message naming the file and the user rather
  than a permissions error the operator has to interpret.
- The write is atomic: a temporary file in the same directory, then a rename.
  A QSP that restarts while half a configuration is on disk starts with nothing
  valid, and the operator's next move is guessing.
- Comments and formatting in a hand-edited file are lost on the first save
  through the interface. JSON has no comments, so nothing is being taken away
  that the format offered, but an operator who has arranged their file will see
  it reordered.

## 2. Who applies a change to a running instance?

`routing.Core` is single-writer and owned by the goroutine that reads the
socket, which is ADR-0002. **An HTTP handler calling `SetTable` is a data
race**, and one the race detector would only sometimes catch, because the
handler and the listener are genuinely concurrent.

So a save does not apply anything. It hands the new configuration to the
listener, which applies it at the top of its next sweep — the same place it
already applies schedule and trigger changes. The sweep runs every second, so
"live" means within a second, which is not distinguishable from immediate to a
person clicking a button.

That also gives the reload the same properties the scheduler's already has: a
transmission in flight keeps its reservations, and the new table takes effect at
the next transmission boundary.

## 3. What cannot be applied at all?

Some settings are read once, at construction, and honestly cannot change under a
running process: listen addresses, the peer password file, the database, the
logging format, and the sockets an upstream link holds.

**Those are saved and not applied, and the interface says so.** The alternative
is either refusing to save them, which makes the interface unable to configure
half of QSP, or pretending they took effect, which is worse than either.

A save therefore reports two things: that it was written, and whether it needs a
restart to take effect. A save that changes only bridges says nothing about
restarts; a save that changes the listen address says so plainly and names the
field that requires it.

| Applies live | Needs a restart |
|---|---|
| Bridges, schedule, triggers | `server.listen_address`, `dmr.listen_address` |
| Access lists | `dmr.password_file`, `dmr.enabled` |
| Subscription and static attachments | `database.*`, `logging.*` |
| Join page, map settings | `dmr.upstreams` — the links hold sockets |

## Decision

Validate, then record, then write, then apply — in that order.

**Recording before writing is deliberate.** A version row describing a
configuration that failed to reach the disk is a puzzle an operator can solve; a
configuration on disk that no version records is a change nobody can attribute
or undo. The failure that leaves less evidence is the worse one.

Every save is an audit event naming the administrator, which is what
`audit_events.actor` has been waiting for since it was created.

## Consequences

- The console gains its first destructive capability. A save that is valid but
  wrong, such as a bridge pointed at the wrong talkgroup, is applied within a
  second, and the only thing standing behind it is the version history. That
  history is therefore not a nicety.
- **Rollback is a save.** Restoring version 7 writes version 12 whose content
  matches it, which is what `configuration_versions` was designed for and why
  its rows are never updated or deleted.
- A club running QSP from a read-only file, or from a container image, can use
  the interface to *read* its configuration and not to change it. That is a
  reasonable posture and the interface should say which one it is in rather than
  failing at the moment somebody presses save.
- Nothing here needs the configuration file to be the only input. An operator
  who prefers the text editor keeps it, and their edits survive a restart
  exactly as they do now.
