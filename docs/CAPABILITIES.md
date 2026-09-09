# What QSP does, and what is beyond it

**Status:** reference. Not a roadmap — `BLUEPRINT.md` and PROJECT_MEMORY §8 own
the order of work. This answers a different question: *what does QSP do today,
and where is the line?*

"Are we there yet" should be answerable from a table rather than from memory or
opinion.

---

## 1. What is built

| Capability | State |
|---|---|
| Timeslots as independent routing interfaces | **built** — timeslot is part of every endpoint |
| Peers on one talkgroup hear each other | **built**, with no configuration at all — ADR-0019 |
| Talkgroup-to-talkgroup mapping | **built** — bridges with translation |
| Fan-out to many destinations | **built** — tested at a hundred peers |
| Static, always-on routes | **built** |
| Dynamic PTT activation with an inactivity timer | **built** — ADR-0016 |
| Scheduled activation | **built** — ADR-0015 |
| Hold-off between competing transmissions | **built** — ADR-0014, per talkgroup per slot |
| Frames relayed without transcoding | **built** — source, target and sync preserved verbatim |
| Live call telemetry | **built** — console and event bus, no dependency |
| Which talkgroups a repeater may use | **built** — checked on arrival and per destination |
| Which repeaters may register | **built** — checked at login, before the password |
| Per-peer talkgroup attachment | **built** — static, and dynamic by transmitting |
| Motorola repeaters over IPSC | **built and on air** — ADR-0036, ADR-0043 |
| Private calls, radio to radio | **built** — routed to the radio's peer |
| Text messaging, GPS, data | **relayed** — carried like voice and confirmed on air; QSP does not decode them |
| Outbound links, dialling another server | **built and on air** — ADR-0051 |
| A link agreed from a console | **built and on air** — offered, accepted, refused, readdressed |
| A server identifier and a display name | **built** — ADR-0053 |
| Backup and restore | **built**, and never yet used |
| Administration without a text editor | **built** — access, network, bridges, schedule, links, and a version history with restore |

**One design difference is worth stating**, because it is the thing most often
carried in from elsewhere. In a commercial DMR server, peers on the same master
hear each other because a routing rule says so. In QSP they hear each other
because a master repeats, which needs no configuration at all; bridges are
additional and move traffic *between* talkgroups. That is ADR-0019, it is how
every homebrew master behaves, and it means a club with four hotspots on one
talkgroup writes no routing rules whatsoever.

---

## 2. What is not

**Relaying and deduplication across three servers.** Built, unit-tested, and
never exercised — two servers give nothing to relay to. It is waiting on a
third operator's server rather than on code.

**Reciprocal identity in full.** A server says what it is in both directions
(ADR-0052 rule 3), but a radio's callsign still does not cross a link: the
server that hears a radio knows the callsign because the station said so at
login, and discards it at the link.

**Private calls across a link.** They resolve through the subscriber table,
links are never targets for one, and a frame arriving from a link records no
location — so a call crosses in one direction only. Deferred by decision; it
needs a record before code.

**Analog and other modes.** AllStar, EchoLink and Zello have registered health
checks and no implementations, which is deliberate: an absent capability that
says so is better than one that is silently missing. Each needs an external
transcoder with an AMBE dongle, because **QSP does not decode audio and will
not** — it copies vocoder payloads and never inspects them, which is why
DMR-to-DMR needs no codec at all.

**P25.** `internal/protocol/p25` exists with a health check standing by, and the
capture in `testdata/p25/` holds polling traffic only. Not the current focus;
the point is that there is somewhere for it to land. P25-to-P25 relaying would
need no transcoding, for the same reason DMR-to-DMR does not.

---

## 3. What this document is not

It is not a commitment to build everything above, and it is not the order of
work. The order lives in PROJECT_MEMORY §8, and the ordering lesson lives in
ADR-0019: build downward before upward. Reaching for a new connector before the
layer beneath it exists repeats exactly the mistake that record captures — not
because the work would be wrong, but because the sequence would be.
