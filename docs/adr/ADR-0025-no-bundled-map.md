# ADR-0025: QSP does not draw a map

**Status:** Proposed
**Amended twice.** On 2026-08-28, before anything was built on the first
version: two of its three arguments did not survive contact with the details.
On 2026-08-29, after the map was built, shipped, and withdrawn.

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

**The first version of this record claimed QSP was the shape of software that
gets blocked**, on the grounds that a tile URL compiled into self-hosted
software is one application in aggregate and a block would land on every
install at once.

That was overstated. Tiles are fetched by the *browser*, carrying each
instance's own `Referer`, so a tile server sees many distinct low-volume sites
rather than one application. The blocked cases are mobile and desktop
applications sharing one identity across millions of users. A club console
loaded a few times a day by a few dozen people is ordinary small-site traffic,
which is what these servers are for.

The real constraint is narrower and still worth respecting: **a large instance
should not point at a donated tile server by default**, and attribution is a
licence condition rather than a courtesy.

## The privacy objection, withdrawn

The first version argued that a club map is a pin per member's house and should
therefore wait for authentication.

**That was not QSP's call to make.** The same positions are published by the
networks these operators already use, every station chooses what coordinates its
hotspot announces, and an operator who would rather not be pinned can move or
omit them — which is the arrangement amateur radio has had for as long as
callbooks have existed. Declining to draw data QSP already serves as text, on
behalf of people who did not ask for the protection, is paternalism rather than
privacy.

It remains true that the endpoint has no authentication, and that is worth
fixing for its own reasons. It is not a reason to withhold the map.

## Withdrawn: QSP does not draw a map

*2026-08-29.* The map was built, deployed, and could not be made to work on the
one instance running it. It is removed.

**What was established.** The tile arithmetic is correct: run against a
simulated DOM at frame widths from 1520 down to 10, `map.js` emitted 21 to 24
tiles every time, positioned across the frame: `-74, 181, 437, 693, 949, 1205`
for a 1920-wide viewport, with the marker at the centre. The instance served the
current file; a `curl` through the proxy confirmed it. The rendered page showed
one tile in a corner and the marker beside it.

**So the positions computed are not the positions rendered, and nothing
available from the source side can discover why.** Six attempts were made, each
reasoning about which measurement was at fault, and each produced the same
screenshot. The seventh added a caption reporting the map's own measurements,
which is what established the above, and established that the fault lies
somewhere the code cannot see.

**The decision is to stop.** A feature that cannot be made to work by the person
maintaining it is not a feature, and continuing to guess spends an operator's
time on something they can already do with one click. The peers table shows each
peer's announced location and links the coordinates out to a map, which answers
"where is this station" — the question actually being asked most of the time.

`/api/peers` carries `latitude`, `longitude` and `height`, so a club that wants
a map can build one and will be able to see what it is doing. The tile-origin
allowance in the content security policy is kept for exactly that.

What follows is the reasoning from when a map was going to be drawn. It is kept
because the conclusions about tiles, licensing and privacy remain right, and
because a decision reversed is worth being able to read.

## Superseded: the design of the map that was withdrawn

**QSP has a map, and it vendors nothing to get one.**

The library objection dissolves rather than being accepted. A slippy map is
Web Mercator arithmetic, a grid of image elements, and a drag handler — about a
hundred and fifty lines. Leaflet is a fine library and most of it is features
this does not need. Writing the arithmetic keeps the console what it
is: markup, one stylesheet, one script, no build step, servable from a LAN.

**The tile source is configuration with a default.** `console.map.tile_url`
points at OpenStreetMap out of the box, because a map needing setup before it
shows anything is a map most operators never see. An instance large enough to
matter, or one that would rather not depend on a donated service, changes one
field.

**Attribution is not optional and is not configurable away.** It renders
whenever the map does. That is a licence condition of the data rather than a
courtesy, and an operator supplying their own tile URL supplies its attribution
with it.

**The map draws nothing until it is opened.** No tiles are fetched for an
operator watching the peers table, which keeps the common case free of requests
to anybody.

## Consequences

- Nothing about the console's no-dependency property changes. It remains
  vanilla markup, one stylesheet and one script, servable from a LAN with no
  route out, and with the tile URL cleared the map still draws its pins.
- **QSP owns a map implementation**, which is a hundred and fifty lines to
  maintain and a class of bug it did not have. That is the price of not
  vendoring, and it is paid deliberately rather than discovered.
- A station with no coordinates, or coordinates that did not parse, is absent
  from the map rather than placed somewhere. ADR-0021's rule applies here too:
  a pin in the wrong place is believed.
- **This record's first version was wrong twice**, and is kept rather than
  replaced so that both are visible. Overstating a risk to avoid work is a
  failure mode worth being able to recognise later.
