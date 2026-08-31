# ADR-0033: Last heard is a record, and net control is who it is for

**Status:** Accepted
**Relates to:** [ADR-0002](ADR-0002-single-writer-routing-core.md),
[ADR-0005](ADR-0005-sqlite-driver.md), [ADR-0007](ADR-0007-schema-downgrade.md)

## Context

The last-heard list holds fifty completed calls in a ring buffer and loses them
on restart. `calls.DefaultHistory` is a package constant, not configuration.
Confirmed on air on 2026-08-30: at forty-seven entries, six more transmissions
took it to fifty, where it stayed.

For three stations on one talkgroup, fifty is two hours. It is a display and it
works as one.

**Then an operator named the use it was actually being put to.** A net control
station takes check-ins, misses a callsign, and looks at the dashboard to
recover it. That is not a display. That is a record, and it is the only record —
nobody is writing down twenty callsigns in real time as a backup for software
that already saw every one of them.

Three things follow immediately, and none of them is true today:

- It has to hold a whole net. Fifty entries is a net plus the conversation
  either side of it, and the early check-ins are the ones that scroll off.
- It has to survive a restart. QSP is restarted on every deploy, and a net runs
  in the evening.
- Somebody has to be able to look at it tomorrow.

## Decision

**Completed calls are written to the database, and the in-memory ring stays.**

They answer different questions. The ring answers *what is happening now*, at
memory speed, with no query, and it is what the overview polls every few
seconds. The table answers *what happened*, which is a question nobody asks
sixty times a minute.

Writing one row per completed call rather than per frame keeps this cheap: a
busy club evening is a few hundred rows, and SQLite does not notice.

### Retention is by age, and it is configuration

`dmr.calls.retain`, defaulting to **30 days**.

By age rather than by count, because the question a club asks is "what happened
at Tuesday's net", not "what were the last five thousand transmissions". A count
means a busy Saturday silently erases the Tuesday somebody wanted.

Thirty days because it covers a monthly net, a member arguing about something
that happened last week, and an administrator who was away — and because at a
few hundred rows a day it is nothing. A club that wants a year sets a year; the
cost is theirs to weigh and the field says what it costs.

**Zero means keep nothing**, which is the answer for a club that would rather
not hold a log of who transmitted when. That is a real position and the
configuration should be able to express it rather than making everyone keep
thirty days.

### Pruning happens on a schedule, not on every write

Deleting on each insert would make every transmission pay for the retention
policy. A club with a year of history would pay a scan per keyup.

Pruned at startup and then periodically. The window is coarse — a row may
outlive its retention by an hour, which matters to nobody, and the alternative
is arithmetic on every call.

### What is stored, and what is not

Everything the ring holds: who transmitted, what they called, group or private,
when it started and ended, how it finished, how many frames, and whether any
were voice.

**No audio.** QSP carries bursts it never decodes ([ADR-0028](ADR-0028-parrot.md)),
and storing them would need a vocoder to be useful and a policy this project has
not written. A record of who spoke is not a recording of what they said, and the
difference matters to the people being recorded.

## Consequences

- **A club now keeps a log of who transmitted and when, by default.** That is
  the point, and it is also a thing worth being explicit about: it is a record
  of members' activity, retained for a month, readable by any administrator.
  Setting `dmr.calls.retain` to zero turns it off, and the field's documentation
  says what it is for so nobody discovers it by accident.
- **A restart no longer erases the evening.** The ring is rebuilt empty and the
  table is not, so the console must read both — the live view from memory and
  the history from the database.
- **The write happens on the single writer's path** and must not block it
  (ADR-0002). A call ending is not a frame arriving, so this is a few hundred
  writes an evening rather than fifty a second, but the ordering rule still
  holds: the routing core is not waiting on a database.
- **A failed write must not lose the call.** The ring keeps it regardless, so a
  full or locked database costs the record and not the display. It is warned
  about rather than raised, because a member transmitting is not the moment to
  fail loudly at somebody who cannot act on it.
- **Migration 0005 adds one table.** ADR-0007 forbids automatic downgrade, so an
  instance rolled back to an older binary keeps the table and stops writing to
  it, which loses history and breaks nothing.
- **The console has no view of it yet.** This decides where the data lives; the
  page that reads a net back to net control is separate work, and the feature is
  not finished until that exists.
