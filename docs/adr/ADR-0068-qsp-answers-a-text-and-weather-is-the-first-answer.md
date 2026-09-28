# ADR-0068: QSP answers a text, and weather is the first answer

**Status:** Proposed — depends on [ADR-0067](ADR-0067-qsp-originates-a-text-message.md)
**Date:** 2026-09-27
**Relates to:** [ADR-0067](ADR-0067-qsp-originates-a-text-message.md),
[ADR-0020](ADR-0020-access-control.md),
[ADR-0043](ADR-0043-qsp-is-the-master.md),
[ADR-0047](ADR-0047-rate-34-text-blocks.md)

## Context

The operator wants three things: a service talkgroup that replies with the time
and the local temperature, weather alerts pushed to subscribers, and bulletins
from net controllers. Built as three features they are three subsystems. Built
as one dispatcher with three handlers they are one.

`internal/parrot` already proves the shape works: a talkgroup can be a service
endpoint, and radios reach it without anybody configuring anything new.

## Decision

### A command dispatcher, and commands are handlers

**An inbound text to a configured talkgroup or radio ID is parsed as a command
and answered with a text.** A command is a name, an authorisation class and a
formatter. Adding one is a handler, not a subsystem.

```
TIME          →  7:05PM CDT / 0005Z, Sun 28 Sep
WX            →  KDTO 7:05PM CDT: 91F, SE 9mph, Mostly Clear
LH K9MLS      →  K9MLS last heard TG2 TS1, 14 min ago
LINKS         →  3 peers up: AD0MI, KB9TYC, Zello
SUB WX        →  subscribed to weather alerts
HELP          →  TIME WX LH LINKS SUB UNSUB
```

Grammar: first word is the verb, case-insensitive, remainder is arguments, an
unrecognised verb gets the one-line help. No punctuation, no quoting, nothing a
radio keypad makes painful.

**`TIME` is built first and it needs no network at all.** It is the only command
with no external dependency and therefore the only one whose failure can be
nothing but a bug in the dispatcher. `WX` is second.

### Replies go to the asker, privately

**A reply is a private text to the sender, not a message to the talkgroup**,
unless a command is explicitly a broadcast one. Ten people asking `WX` during a
net must not produce ten messages on the net's talkgroup. The service talkgroup
is an address, not a channel.

### Read is open, write is an allow-list

Per §6c: features fail open, security fails closed. `TIME`, `WX`, `LH`, `LINKS`
answer anybody the access lists already admit — they disclose nothing a
dashboard does not. Anything that changes state — subscribing another radio,
sending a bulletin, touching a link — requires the sender's radio ID to be on an
administrator allow-list, and refuses by default with a reply saying so rather
than silently ignoring.

This is an unauthenticated command surface reachable from any linked repeater.
Naming that in the record is the point: the reason read-only is safe is that it
is read-only, not that radio IDs are hard to forge. They are not.

### Weather is the first feed, and NWS is the source

**Source: `api.weather.gov`.** No key, no account, free, and it is the United
States Government's own station — which is also the source Part 97 §97.113(e)
carves out for retransmitting weather forecast information. **The base URL is a
configured field**, because §0 says QSP is built for the amateur radio community
and not for one club, and an operator outside the United States needs their own
feed.

NWS asks for a `User-Agent` carrying contact details and blocks anonymous
traffic. It is a field, not a constant.

### What was measured, 2026-09-27

Against KDTO and TXZ103, from Fedora:

```
station  KDTO          zone  TXZ103        county  TXC121       office  FWD
```

Observation `properties`, verbatim:

```
timestamp         2026-09-28T00:05:00+00:00
textDescription   Mostly Clear
temperature       {unitCode: wmoUnit:degC,            value: 33}
dewpoint          {unitCode: wmoUnit:degC,            value: 19}
windSpeed         {unitCode: wmoUnit:km_h-1,          value: 14.832}
windDirection     {unitCode: wmoUnit:degree_(angle),  value: 130}
relativeHumidity  {unitCode: wmoUnit:percent,         value: 43.645157807318}
```

**Every numeric field carries its own unit and they are all WMO.** Wind arrives
in kilometres per hour and direction in degrees. **Convert by `unitCode`, never
by field name** — that is the difference between reporting 9 mph and 15 mph, and
it is this project's rule about deriving a claim rather than asserting it.

Three further measured facts:

- **There is no observation cadence to rely on.** Two consecutive reads were
  `23:53:00Z` and `00:05:00Z`, twelve minutes apart, because a station emits
  special observations on top of the routine hourly. Judge freshness from
  `timestamp`; cache on a short TTL; assume no schedule. One sample looked like
  a pattern and the second killed it.
- **`relativeHumidity` came back as `43.645157807318`.** Round at format time.
- **The development container cannot reach `api.weather.gov`** — 403 at the
  egress proxy, measured. The live API is reachable only from Fedora or
  production, so **fixtures are the only way to test this in the container**, not
  a convenience.

### The alert filter chain, in this order

An alert's `properties`, as measured:

```
id           urn:oid:2.49.0.1.840.0.84d88cc3...001.1
geocode      {SAME: [048137], UGC: [TXZ184]}
references   []
sent/effective/onset/expires   set          ends   null
status       Actual        messageType  Alert
severity     Moderate      certainty    Observed      urgency  Expected
event        Special Weather Statement
```

1. **`status == "Actual"`.** CAP also defines `Exercise`, `System`, `Test` and
   `Draft`, and NWS emits them. Without this check a required monthly test
   becomes a tornado warning on somebody's radio. This is the filter that is
   easiest to forget and worst to omit.
2. **Zone or county match**, against `geocode.UGC` or `geocode.SAME`. Both are
   present, so the administrator filters in whichever they think in. Note that
   `?area=TX` returns the whole state — the measured sample was Edwards County,
   three hundred miles from the operator.
3. **`event` in the administrator's allow-list.**
4. **`severity` at or above a configured floor.**
5. **Not expired**, by `expires`. `ends` was observed null while `expires` was
   set, so `expires` is the field.
6. **`id` not already sent.**
7. **Not superseded**, via `references`.

### Four rules that are the whole difficulty

**The restart must not blast.** On the first poll after start, record every
active alert as already sent **without transmitting**. Otherwise a restart
during a flood watch sends fourteen texts about weather everyone has known about
since Tuesday. This is the most likely defect in the feature and the reason it
is written down before any code.

**Update and Cancel are not new alerts.** A warning gets reissued as the polygon
moves: several `id`s, one event. Follow `references` and suppress supersedes.

**An outbreak is a rate-limit problem.** Severe weather generates alerts faster
than a talkgroup can carry them. A hard cap per interval, coalescing by event
and zone, and the cap visible and configurable.

**The formatter has about forty characters.** Measured from the real alert, the
shape that fits is abbreviated event, area, expiry:

```
SPS: Edwards Co until 8:00PM CDT
TORNADO WARNING: Denton Co until 7:45PM CDT
```

`headline` is prose, and `description` was twelve lines with embedded newlines.
Neither is usable; both must be dropped rather than truncated. The
event-to-abbreviation map is **administrator configuration with defaults**, not
a hardcoded table, and the real limit comes from
[ADR-0067](ADR-0067-qsp-originates-a-text-message.md) phase 4 rather than from
this document.

### Subscriptions are managed from both ends

A subscription is a radio ID, a feed and a zone, in SQLite. It is created and
removed **from the console and from a radio** — `SUB WX` and `UNSUB WX`. The
console rule is that anything a page creates it must be able to remove; the
radio deserves the same, because the operator who wants to stop receiving alerts
at two in the morning is holding a radio, not a browser.

## Consequences

**Nothing here ships before ADR-0067 phase 3.** A dispatcher that cannot put a
message on a screen is untestable by the only instrument that counts.

**The command service is the honest version of "make the system report its own
state."** `LINKS` and `LH` are §8a's rule extended to the radio, and they would
have been useful during the Quantar bring-up more than once.

**Every dependency is optional and fails quietly.** Weather unreachable means
`WX` answers that it is unavailable and the health page shows the last
successful poll; it does not mean the dispatcher stops answering `TIME`.

**§8a's question gets asked before the patch, not after.** A severity floor that
is validated, documented, defaulted and read by nothing is this project's
single most repeated defect, ten instances and counting. Every configured field
in this ADR gets grepped for its call site.

## Alternatives considered

**A webhook and REST gateway instead, with weather as an external script.**
Genuinely better for integration, and it is where this should eventually go — a
radio text in, an HTTP request out, and the reverse. Deferred rather than
refused: the operator's stated scope is a text messaging service, the gateway is
a larger decision about what QSP is, and the dispatcher built here is what a
gateway would dispatch to anyway. Nothing in this ADR has to be undone to add
it.

**A commercial weather API.** Rejected on two grounds: NWS is the source
§97.113(e) names, and a keyless government endpoint is one field for an operator
to configure instead of an account to create.

**Alerts announced by voice.** Rejected under audio is king, as in
[ADR-0067](ADR-0067-qsp-originates-a-text-message.md).

**Replies to the talkgroup rather than to the asker.** Rejected: it turns every
query into traffic for everybody, and the first busy net would be the last time
anybody used the service.
