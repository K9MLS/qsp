# ADR-0025: QSP shows where peers are without shipping a map

**Status:** Proposed

## Context

Hotspots announce a position in `RPTC`, QSP now parses it and the console shows
it as text. PROJECT_MEMORY has wanted a live map for some time, and the obvious
next step is a slippy map with a pin per peer.

The obstacle recorded in §8 is that a map library means a build step or a CDN,
and the console has neither by design. That is real, and it is the smaller half
of the problem.

## The binding constraint is tiles, not the library

A map library draws tiles; it does not produce them. Every slippy map fetches
image or vector tiles from somewhere, and that somewhere is almost always
somebody else's server.

OpenStreetMap's tile servers are the default anyone reaches for and are
explicitly not a free API for applications: they are funded by donations, their
capacity is limited, and access may be blocked without notice when an
application's usage degrades the service. Applications making heavy use are
asked to run their own tile servers, and several that did not have been blocked
outright.

**QSP is the shape of software that gets blocked.** It is self-hosted and
shipped to many clubs. A tile URL compiled into the console is the same request
from every instance, indistinguishable in aggregate from one heavy
application, and a block would land on every QSP install at once, including the
ones running a map for four hotspots. That is a shared blast radius created by
a default, which is the sort of thing §0 exists to refuse.

Vendoring the library into the repository solves the build step and none of
this. A CDN adds a second third party and makes the console stop working on a
LAN with no route out, which is exactly where a club server often lives.

## The other objection: it is member home addresses

A club map is a pin per member's house.

That information is already on `/api/peers`, and this record does not pretend
otherwise. But a table row saying "Denton, TX" and a pin somebody can zoom into
are not the same act, and the endpoint has no authentication. Making member
locations visually browsable to anyone who finds the URL deserves to be a
deliberate choice by the operator rather than a feature that arrives switched
on.

## Decision

**QSP ships no map, no map library, and no tile URL.**

The console links a located peer's coordinates out to a map, so one click gets
an operator a real map of that station. **No tiles are fetched by QSP and no
request leaves until somebody clicks**, at which point it is an ordinary
browser navigation to a site the person chose to visit.

That gets most of the value. "Where is this station" is a question about one
peer far more often than it is a question about all of them, and the answer is
now one click away without QSP depending on anything.

**The door stays open, as configuration rather than as a default.** A club that
runs its own tile server, or holds a commercial key, can be given a tile URL
setting and a real map built against it — nothing here forecloses that, and
`/api/peers` already carries everything such a map would need. What is refused
is a *default* tile source, because a default is what turns one club's map into
every club's shared risk.

## Consequences

- A club that wants a real map can build one against `/api/peers` today. The
  data is public on the instance and the shape is stable.
- The map stays off the roadmap as a QSP feature until either a tile source is
  configured per instance or somebody self-hosts one. That is a smaller promise
  than PROJECT_MEMORY §8 currently makes, and the honest one.
- Nothing about the console's no-dependency property changes. It remains vanilla
  markup, one stylesheet and one script, servable from a LAN with no route out.
- **If a map is built later, authentication should come first.** A pin map of
  members' homes on an unauthenticated endpoint is a different exposure from a
  column of text, and the admin interface brings the authentication that would
  make it a considered choice.
