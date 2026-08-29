# ADR-0030: Radio IDs are looked up one at a time, and QSP says who is asking

**Status:** Proposed

## Context

"Last heard" shows radio IDs. QSP already names the ones that match a connected
peer's own registration. That covers a hotspot owner's radio and nothing else:
a member transmitting through somebody else's hotspot, or a second radio on a
different ID, stays a seven-digit number.

RadioID.net holds the amateur DMR registry, and BrandMeister and every dashboard
of consequence resolves against it. A club expects to see names, and asking
members to build alias lists in every radio is a worse answer than looking them
up once.

## What their policy actually says

This is the constraint that shapes the design, more than the endpoint does.

- **Normal lookup use is allowed.** Bulk, mirrored, scraped or public-facing
  redistribution requires approval, and competing directory, search, mapping or
  export services require written permission.
- **Automated clients must identify themselves** with a clear User-Agent and a
  contact address.
- They may rate-limit, require authentication, revoke access or block abusive
  clients at any time, and they ask plainly to be gentle.

QSP is self-hosted and shipped to many clubs, which is the same shape that made
a default tile server a shared risk in [ADR-0025](ADR-0025-no-bundled-map.md).
The difference is that here the operator of the service has written down what
they want, so the question is not what is defensible but what they asked for.

## Decision

**One lookup per unknown ID, cached, and QSP says who is asking.**

### No bulk download

The whole registry is a large file and downloading it would be mirroring, which
their policy puts behind approval. A club has a few dozen members and will look
up a few dozen IDs. Fetching hundreds of thousands of records to answer that is
both against what they asked and worse engineering — the file is stale the day
after it lands, and a Raspberry Pi does not want it.

### The operator supplies a contact address, or there are no lookups

`dmr.callsigns.contact` is required when lookups are enabled, and QSP sends it
in the User-Agent along with its version. **There is no default and no
fallback.** An anonymous automated client is what their policy asks people not
to be, and QSP has no business inventing a contact for somebody else. It is the
operator who is making the requests and who will be contacted if something is
wrong.

That makes the requirement visible rather than buried: an operator turning this
on is told why an address is wanted.

### Nothing blocks on a lookup

A lookup happens away from the frame path entirely. A radio ID seen in a
transmission is queued, resolved later, and appears the next time the console
polls. **A network call has no business anywhere near a routing decision**, and
a registry that is slow or down must cost nothing but the name.

### Results are cached, including the absences

A resolved name is kept indefinitely: callsigns do not change often, and a stale
name is a smaller fault than a repeated request. An ID that the registry does
not know is cached too, for a shorter time. Otherwise every unregistered radio
on the network becomes a request on every transmission, which is exactly the
"excessive requests" their policy warns about.

The cache lives in the database, so a restart does not re-ask for everything
QSP already knew.

### It is off unless configured

Not because it is dangerous, but because it makes an outbound request to a third
party that the operator did not obviously ask for. A club that wants names turns
it on and supplies a contact; a club on an isolated network is not quietly
trying to reach the internet.

## What this does not resolve

**Whether a public console showing resolved names is "public-facing
redistribution"** under their policy. The reading here is that displaying a name
beside a transmission is normal lookup use and what the registry exists for, and
that a redistribution is republishing the data as data: an export, a mirror, a
competing directory. That reading is offered rather than assumed, and an
operator running a public instance who wants certainty should ask them.

QSP does not offer the cache for download, does not expose a search over it, and
does not serve names for IDs that have not been heard on the instance. Those
would be the things that turn a lookup into a directory.

## Consequences

- A club sees names without maintaining alias lists in every radio, which is the
  thing that was asked for.
- **QSP makes outbound requests it did not before**, to one host, only for IDs
  heard on the instance, only when configured, and identified. That is a real
  change in what the software does and belongs in `SECURITY.md`.
- The exact-match resolution against connected peers stays. It needs no network,
  no cache and no policy, and it is right more often than a registry for the
  case it covers.
- A name is what the registry says, not what QSP knows. It is shown as such, and
  a resolution that turns out to be wrong is the registry's to correct.
