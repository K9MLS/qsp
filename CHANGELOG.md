# Changelog

All notable changes to QSP. Dates are UTC.

## [Unreleased]

### Added
- **[ADR-0020](docs/adr/ADR-0020-access-control.md) decides access control**,
  the layer 2 gap ADR-0019 named. Four lists in HBlink's vocabulary; a pure
  `internal/access` package that neither `peers` nor `routing` has to own; the
  talkgroup list checked on egress as well as ingress, because an ingress-only
  check permits bridged and upstream traffic while stopping a club's own
  members; and a zero value that permits everything, so upgrading does not
  disconnect a running club.

  A listener reachable from beyond the host, with no access block written at
  all, now refuses to start. Saying `{"mode": "deny", "ids": []}` — deny nobody
  — starts without complaint. A startup warning would have been read once by
  whoever was watching the journal, which is a weak mitigation for the moment
  UDP 62031 is forwarded at the router.

- **`internal/access`, the lists themselves.** Parsing, merging and evaluation,
  with no I/O and no state, so `peers` and `routing` can both depend on it
  without `routing` acquiring an edge to `peers`. Twenty tests and a fuzz
  target.

  **The three lists do not share a ceiling, and this was nearly a field bug.** A
  talkgroup and a subscriber ID travel in 24 bits, but a repeater ID travels in
  32, and a hotspot registers with its owner's seven-digit ID plus a two-digit
  suffix. One shared ceiling would have refused every hotspot on the network.

  Registration entries of seven or eight digits produce an advisory rather than
  an error, because the registry's numbering is a convention and not a rule of
  the protocol.

  Not yet enforced: `peers` and `routing` do not consult the lists yet.

- **The `access` block in the configuration**, with validation that names the
  exact field — including which timeslot — so an error points at the line to
  edit rather than at the block.

- **[`docs/CAPABILITIES.md`](docs/CAPABILITIES.md)**, which states
  a commercial DMR server's architecture in a commercial DMR server's own vocabulary, maps it onto QSP's
  layers, and says where the line is today. QSP is ahead on scheduling, on the
  repeat model and on being free; behind on access control, per-peer
  subscription, and being administrable without SSH.

  It also names three things a commercial DMR server parity does not cover: outbound peer
  mode, which is the single largest gap by reach and is what would let QSP
  dial XLX, DMR+ or IPSC2 rather than only accept connections; IPSC, which is
  what makes QSP a drop-in for clubs running Motorola repeaters rather than a
  reason to replace them; and data calls, which sit outside the layer model
  entirely because the layers describe where a frame goes and data is a
  question of what a frame is.

- **Access control is enforced at the master.** The registration list is checked
  at login, before the password lookup, so a refused ID never reaches the
  credential path and the log says which of the two refused it — "wrong
  password" and "not permitted here" being very different messages to an
  operator whose hotspot will not connect. The subscriber list is checked per
  frame.

  **A refused subscriber does not disconnect its peer.** On DMR a hotspot is
  shared infrastructure and the offending party is a radio, so the peer is still
  heard from and its timeout still resets.

  **A refused transmission is announced once, not once per frame.** Thirty
  seconds of a held key is roughly five hundred frames. The opening frame
  explains itself and the rest are counted — never silently, since a caller
  counting drops still sees every one.

  Ten tests. Talkgroup lists are deliberately not consulted here: refusing at
  the master would drop a frame before any destination was known, including
  destinations the list would have allowed.

- **QSP now knows where radios are**, the prerequisite
  [ADR-0021](docs/adr/ADR-0021-private-calls-and-data.md) named for private
  calls and radio-to-radio text. A private call's destination is a radio rather
  than a talkgroup, and a radio's whereabouts is a property of where somebody is
  standing, so it is learned from traffic and never configured.

  The newest sighting wins with no confirmation step: a radio moving between
  hotspots is somebody driving, and preferring the older record would send calls
  to the hotspot they have just left. `dmr.subscriber_timeout` defaults to two
  hours, far longer than `peer_timeout`, because a peer that stops sending
  keepalives is gone while a radio that stops transmitting is merely quiet.

  A radio refused by the subscriber access list is not recorded, so it does not
  become reachable as a private call destination — one list governing both, as
  ADR-0021 asked. A radio whose peer has since disconnected is listed but not
  routable, since routing to a departed peer would be silence with no
  explanation.

  Ten tests. **Nothing routes on this yet**; private call routing is next.

### Fixed
- **A master restart cost a minute of dead network.** QSP dropped keepalives
  from a peer it no longer had a registration for, in silence, so the peer only
  discovered it had been forgotten when its own timeout fired. Observed on the
  soak VM: a restart at 13:37:36, the peer back at 13:38:37, and 25 datagrams
  dropped in between.

  A stale keepalive is now answered with MSTNAK, which is what
  `docs/architecture/hbp-protocol.md` has always said the message is for — it
  both refuses a login and tells a stale peer to log in again, and only the
  first half was used.

  Voice frames from a stale peer are still dropped silently. They arrive every
  60 ms, so answering each would put hundreds of datagrams on the wire for one
  transmission; a keepalive arrives every ten seconds and is the peer's own
  liveness check.

- **The console told operators forwarding was off while it was relaying.** The
  notice was static markup with no condition on it, so it rendered
  unconditionally — a soak instance that had been repeating for eleven hours
  displayed it the whole time. `/api/peers` now reports `forwarding`, and the
  notice and the Last heard caption follow it.

  The Overview text was stale in the same way: it described QSP as relaying
  "between bridged talkgroups on a schedule", which was the whole model before
  ADR-0019 and has not been since. It now leads with peers on a talkgroup
  hearing each other, and treats bridges as the additional thing they are.

  The documentation accuracy gate scans Markdown, so it could catch none of
  this. Three stale claims in HTML and JavaScript, all describing a QSP that
  stopped existing when the master learned to repeat.

- **`internal/peers` claimed two things were absent that are built.** Its
  package doc said RPTCL could not be parsed and that a refused peer was
  dropped rather than answered — both true when it was written, both false
  since ADR-0008's specification pass. `handleClose` removes a cleanly
  disconnecting peer, and `reject` answers with MSTNAK.

  The documentation accuracy gate scans Markdown, not Go doc comments, so CI
  could not have caught this. A live log did: a WPSD hotspot disconnected on
  2026-08-28 and the master recorded `peer disconnected cleanly`, which is
  the path the doc said did not exist. `docs/architecture/hbp-protocol.md` now
  records RPTCL as observed rather than merely implemented; MSTNAK is still
  unverified and still wants a wrong-password capture.

- **Talkgroup access control is enforced in the routing core**, which completes
  layer 2. The list is checked twice, which is ADR-0020's substantive decision:
  once when a frame arrives, and once per destination it would reach.

  The second check is not redundant. A bridge translates, so a frame arriving on
  TG 9 and leaving on TG 91 is tested against two different entries — and
  traffic arriving over a bridge or an OpenBridge link never crossed the first
  check at all, which is precisely the traffic an operator can least vouch for.
  Exports to a link are subject to the same lists, so a talkgroup this instance
  does not carry is not handed to somebody else's network.

  A refused destination becomes a `Drop` with a reason, which the console
  already renders, and is **not reserved** — reserving it would make it look
  busy to the next transmission, quietly turning an access list into a denial of
  service on everybody else.

  `SetAccess` takes effect on the next frame rather than the next transmission,
  deliberately unlike `SetTable`. Ten tests, and every routing test that
  predates this passes unchanged.

- **[ADR-0021](docs/adr/ADR-0021-private-calls-and-data.md): private calls and
  data are in scope, and share one missing thing.** Private calls do not work at
  all — repeat fires only on a group call, and nothing routes a call whose
  target is a radio. That is layer 1 work that was missed in the same way repeat
  was missed, not a feature sitting above the model.

  Both need **subscriber location**: which peer a radio was last heard through.
  It cannot be configured, because it changes when somebody drives to work, so
  it is learned from traffic and aged out. Nothing tracks it today.

  Data splits into two jobs of very different size. A text message to a
  talkgroup is a group call carrying data bursts and may already work, which is
  a hardware question rather than a design one. A text message to another radio
  is a private call and needs everything above.

### Changed
- **A DMR listener on an address reachable from beyond its host now refuses to
  start without an `access` block.** This is a breaking change for any instance
  bound to `0.0.0.0` or a LAN address, which is most of them.

  The fix is one line, and the validation error contains it: an empty `"access":
  {}`, or the `{"registration": {"mode": "deny", "ids": []}}` the message
  suggests, both mean deny nobody and permit everything — exactly the behaviour
  of 0.1.9. What changes is that permitting everything is now something an
  operator wrote down, in a document that is versioned and diffed, rather than
  something that happened silently. A listener on loopback is unaffected, and so
  is a disabled one.

## [0.1.9] — 2026-08-27

The master repeats. QSP does the thing a DMR network is for.


### Fixed
- **Successful polls are logged at debug rather than info.** The join page polls
  `/api/join` every three seconds per open browser — one member watching
  overnight is roughly 28,000 lines. A club's worth during a net would rotate a
  500 MB journal past the evidence an operator needs, which during a fourteen-day
  soak is the entire record of whether it passed. A poll that *fails* still logs
  at warning or error, because that is the case worth seeing.
- `docs/SOAK.md` explains `start-limit-hit`. Five restarts in five minutes trips
  systemd's rate limiter, which is correct behaviour for an unattended run and
  reads exactly like a crash. `systemctl reset-failed` is the answer and is not
  obvious. The pass criterion is also corrected to no *unexplained* restarts: a
  restart after a configuration change counts, and a number with no note beside
  it cannot be told from a crash at day fourteen.


### Added
- **The master repeats.** A group call on a talkgroup now reaches every other
  peer on that talkgroup and timeslot, with no bridge involved. Four hotspots on
  TG 9 hearing each other — the thing a DMR network is for — had no
  configuration in QSP until now. See
  [ADR-0019](docs/adr/ADR-0019-master-repeats.md).

  Repeat is on by default and switched off with `NoRepeat`, because a master
  that does not repeat is inert and nobody wants one by accident.

  Ten tests, including a hundred hotspots on one talkgroup, contention between
  two simultaneous talkers, one copy per peer when a talkgroup is also bridged,
  and that private calls are not broadcast.

### Fixed
- **A contention hole found while building repeat.** Deduplicating a delivery
  also skipped its reservation, so a destination reached by both a bridge and
  repeat looked free to the next transmission and two people's audio could
  interleave on it. The reservation is now taken whether or not a second copy is
  sent.
- **`TestUnbridgedTrafficIsNotRelayed` asserted the bug.** It expected a
  talkgroup no bridge covers to reach nobody. It is now
  `TestUnbridgedTrafficIsRepeatedToOtherPeers` and checks the opposite over real
  sockets.

### Changed
- Four routing tests had expectations that the new model supersedes, each
  updated with the reason stated: reservation counts include repeat, a nil table
  still repeats, and a disabled bridge stops traffic crossing to another
  talkgroup without stopping peers hearing each other.
- The schedule and PTT gating tests now build their core with `NoRepeat`,
  because they measure bridge gating and repeat would deliver regardless —
  correctly, since a closed window closes a bridge and not a talkgroup.

## [0.1.8] — 2026-08-27

OpenBridge, end to end.

### Changed
- **ADR-0008 amended for IPSC, and one of its own claims corrected.** It
  recorded DMRlink as CC BY-SA 3.0; the source files carry a GNU GPL v3-or-later
  header. That correction changes the picture, because GPL-3.0 into GPL-3.0 is
  the arrangement that licence exists to permit rather than a conflict to
  reconcile.

  The larger finding is that IPSC has **no published specification** at all, so
  the "protocol documents" route HBP used does not exist for it. But the interim
  rules already permit implementing from **captured traffic**, and the club runs
  the exact repeaters IPSC is wanted for — so the preferred route needed no
  amendment. Reading DMRlink where captures fall short is now permitted
  explicitly and narrowly, at the cost of attribution and a derivative-work
  notice that cannot be undone later.

  The CC BY-NC-SA question remains open. IPSC does not touch it.

### Added
- **OpenBridge is wired end to end.** `upstream.Set` holds the links and routes
  sends by name; the listener gains `Upstreams` for outbound and
  `DeliverFromUpstream` for inbound, since it owns the socket peers are
  reachable on. Each link registers its own health check rather than one
  aggregate, because an operator with two links needs to know which is quiet.

  A stale link reports **degraded**, not unhealthy: QSP does not know it is
  broken, and claiming a fault it cannot confirm teaches an operator to ignore
  the report. Each degraded state carries an actionable fix — check the
  passphrase is byte-identical, confirm the far end has this address, or raise
  `stale_after` if the talkgroup really is quiet.

  A bridge naming a link that is not configured is refused with the name, rather
  than appearing to work while carrying nothing.
- **`internal/upstream`** — the link itself. One UDP socket per configured
  upstream, signing frames outbound and verifying them inbound, making no
  routing decisions of its own.

  The clock is injected, so a four-hour staleness threshold is tested in
  microseconds. A test that had to wait four hours would never have been
  written and the threshold would have gone unverified.

  `Status` distinguishes three cases an operator would otherwise conflate.
  Nothing ever received, with datagrams rejected, means both ends are
  configured and disagree about the passphrase — the one fault QSP can name
  precisely, and the one that otherwise costs an evening. Nothing ever
  received, with no rejections, means traffic is not arriving at all: check the
  far end has this address. And received-but-not-lately means it stopped, which
  is a different place to look. Rejections are logged for the first five only,
  because a misconfigured sender can produce them as fast as the network allows.

  Verified over real loopback sockets, twenty consecutive runs and five under
  the race detector.
- **Upstreams route through the existing core.** `routing.Endpoint` gains an
  `Upstream` field, `Result` gains `Upstreams`, and `Core.RouteFromUpstream`
  handles traffic arriving over a link. Contention, talkgroup translation and
  the drop accounting all apply unchanged — the point of routing through the
  core rather than beside it.

  `Route` keeps its signature, so the existing routing tests are untouched and
  become the regression check. All of them still pass.

  Two subtleties found while writing it. `Endpoint.Matches` had to learn that a
  link is not a peer: an upstream endpoint carries `AnyPeer` by default,
  `AnyPeer` matches everything, and the table concluded the link *was* the peer
  that had just transmitted — so it declined to send the frame there, on the
  grounds that a call is never sent back where it came from. The bridge would
  have appeared configured and carried nothing. And `sourceKey` gains the link
  name, because two networks choose stream IDs independently: a frame from
  BrandMeister sharing a stream ID with a local transmission would otherwise
  look like a continuation of it, and two people's audio would interleave.
- **`dmr.upstreams` configuration**, with validation. Named links, each with a
  far-end address, a local listen address, a network ID, a passphrase file, and
  separate `export` and `import` lists naming **local** talkgroups — QSP applies
  the TS1 rule rather than leaving an administrator to remember it.

  Two validation decisions worth naming. A **disabled** link is checked only for
  its name, because an administrator writes the configuration down before
  BrandMeister grants the bridge and has neither passphrase nor address yet;
  requiring them would mean the only way to record the intent is not to. And an
  enabled link carrying **neither** export nor import is refused: it connects,
  authenticates and does nothing, which looks identical to a broken link and
  which the far end eventually removes for showing no traffic.
- **`internal/protocol/openbridge`** — the wire format. Sign, verify, parse and
  encode; no sockets, no retries, no forwarding decisions, for the same reason
  ADR-0013 keeps routing pure.

  Thirteen tests, of which the ones worth naming: the signature is checked
  against an independently computed HMAC-SHA1 rather than only round-tripping,
  because Sign and Verify sharing a mistake would pass a round-trip and fail
  against the far end. Nine tamper cases cover every field a mischievous sender
  would want to alter. An empty passphrase is refused outright — it produces a
  signature anyone else with an empty passphrase can forge, which is worse than
  no authentication because it looks like authentication. And `Encode` forces
  TS1 and stamps the network ID, both of which are the protocol's rules rather
  than an administrator's to remember.
- **[ADR-0018](docs/adr/ADR-0018-openbridge.md): OpenBridge for linking to other
  networks.**

  QSP will not log into a BrandMeister master as a homebrew peer. That is not a
  preference: BrandMeister requires OpenBridge for interconnecting a network,
  prohibits peer bridging, and asks specifically that nobody build software
  without an onboard radio that impersonates Homebrew or MMDVM. QSP is exactly
  what they are describing.

  The ADR settles four things worth arguing with before there is code. Upstreams
  are named blocks with separate `export` and `import` lists, because a club may
  send its net up while accepting a nationwide talkgroup down. A frame that
  arrived from an upstream is never sent to an upstream — blunt rather than
  clever, because the failure mode of a hop count is a broadcast storm on
  somebody else's network. Upstreams route through the existing core rather than
  beside it, so contention and translation apply unchanged and the 297 existing
  tests become the regression check. And because OpenBridge has no keep-alive,
  QSP cannot tell a quiet talkgroup from a dead link, so the health summary says
  exactly that rather than guessing.
- **Evidence for the link-health decision**, added after a research pass. A
  silently dead OpenBridge link is the documented top failure — the BrandMeister
  FAQ leads with it, and an address change breaks a link with no local signal.
  BrandMeister states plainly that alerting operators to down connections is not
  their responsibility, and that bridges showing no traffic for 60 days may be
  removed without notice. So a link that quietly died is a link that will
  quietly be taken away. Staleness reporting is the most valuable thing this
  feature offers, not a nicety.
- Also records that proper OpenBridge passes all traffic on **TS1** with the
  slot bit clear, so club talkgroups on TS2 must be translated on the way out
  and back on the way in. That is a routing rule, not an option.

### Fixed
- ADR-0017 was never added to `docs/adr/README.md`.

## [0.1.7] — 2026-08-26

Member onboarding shipped, and the network's direction settled and written down
so it stops being rediscovered.

### Changed
- **`BLUEPRINT-v1.md` rewritten.** It now opens with the rule that governs every
  other decision: QSP is built for the amateur radio community, not one club, so
  talkgroups, masters, repeater IDs and passwords are administrator
  configuration rather than design-time questions. That mistake was made three
  times in one session.
- **How QSP relates to the existing networks is recorded.** Its routing model is
  a commercial DMR server's — always-on, scheduled, on-demand — which is `enabled`, `schedule`
  and `triggers`, built before anyone checked. BrandMeister's subscription model
  is documented as a difference rather than a defect, with the one real gap
  named: QSP's PTT trigger opens a bridge network-wide, where a dynamic
  talkgroup should attach to a single peer.
- **BrandMeister linking is OpenBridge.** Not a preference: BrandMeister forbids
  peer bridging and asks that nobody build software impersonating Homebrew or
  MMDVM without an onboard radio. OpenBridge is DMRD-only, no handshake, no
  keepalive.
- **IPSC is scheduled as its own phase**, master and peer modes both, for the
  Motorola repeaters club sites actually run. Blocked on ADR-0008, with the
  likely resolution recorded: DMRlink and HBlink3 are GPL-3.0 and so is QSP,
  while ADR-0008's restrictive limb concerns CC BY-NC-SA non-commercial terms.
  Amending an ADR to record reasoning is right; editing it to say "accepted"
  because something is wanted is not.
- **Deployment targets a server or VM**, with the Pi kept as the proven minimum.
- Phases reordered: OpenBridge before admin setup, IPSC after.
- `PROJECT_MEMORY.md` §7a carries the same summary, so a new session reads it
  before proposing work.
- A live node map is recorded as near-term feasible: hotspots already send
  latitude, longitude, height and location in `RPTC`, and QSP discards them.

### Also in this release

### Added
- **`dmr.join` configuration**, feeding `/api/join`. It is configuration rather
  than something QSP derives, and the reason is the whole difficulty of
  onboarding: the number a member dials is rewritten by their own hotspot
  before QSP ever sees it. QSP knows only the arriving talkgroup; only the
  admin, who has read the `TGRewrite` lines in `/etc/dmrgateway`, knows both.
- **Validation catches a talkgroup no bridge carries.** An admin who mistypes
  `arrives` sends every member to a destination that goes nowhere — they hear
  silence, conclude QSP is broken, and the admin cannot reproduce it without a
  second radio. Startup now refuses, naming the entry and pointing at the
  hotspot's DMRGateway configuration. Timeslot is checked too, because TG 9 on
  TS1 is not TG 9 on TS2. Duplicate arrivals are refused for the same reason:
  once rewritten they are indistinguishable.
- **`GET /api/join` — member onboarding.** A club network is one admin and fifty
  to a hundred members, each of whom must point a hotspot at it. Getting the
  first one connected took two sessions, and QSP was never at fault: the
  obstacles were `Enabled=0` in `/etc/dmrgateway` while the WPSD dashboard said
  otherwise, and a rewrite meaning the number dialled was not the number that
  arrived. Told fifty times, that becomes the product's reputation.

  The endpoint returns the address, port and — critically — **both** talkgroup
  numbers, dialled and arriving. It reports whether the listener is even
  enabled, and identifies the caller's own hotspot by source address, so the
  machine confirms it worked rather than the member wondering.

  It deliberately carries **no credential**, which is what makes it safe to show
  a club's members when the rest of the console is not. A test greps the raw
  response for the password rather than trusting the struct to lack a field for
  it.

- **`/api/join` reports the member's own transmission.** The page previously
  ended by telling them silence was normal — true, and useless: they keyed up
  and learned nothing. QSP already knows whether the audio arrived and how many
  frames it carried, so it says so. Matched on the peer's radio ID rather than
  the call's source, because a relayed call keeps the originating radio's ID
  and that member did not send it.
- **The `/join` page**, in `console/static/` — five steps, one
  column, readable on a phone in a shack. The dialled talkgroup number is set
  in the largest type on the page because it is the one thing a member must get
  right, and "arrives as" is shown beside it so the unfamiliar number in their
  hotspot's log does not read as a fault.

  Step 4 is "check it really saved", and exists solely because the WPSD
  dashboard once displayed a network as enabled while the file said otherwise.
  Step 5 watches for the member's own hotspot and turns green when the server
  sees it.

  No build step and no CDN, matching the rest of the console: a hotspot is often
  on a network with no route to the internet. Polling rather than SSE, because
  this page is read by people simultaneously restarting hotspots and reloading
  dashboards. Every colour comes from `tokens.css`; there are no raw hex values.

  Decided without an approval workflow: HBP uses one shared secret per network,
  a club of fifty knows its own members, and vetting needs admin sessions that
  do not exist. It can be added later — the peer registry already records who
  connected and when.
- **`internal/peers/fanout_test.go` — scale and provenance.** `forward_test.go`
  already relayed between two peers over real sockets; what it used were frames
  this project constructed, and only two peers. This adds a hundred, and adds
  frames a radio actually sent.
- **A captured transmission survives the wire.** 242 frames recorded from a WPSD
  hotspot are relayed through the listener and read off the far end, translated
  to the destination talkgroup and timeslot with the originating radio's ID
  intact and the 33-byte burst byte-identical. Frames are sent and read one at a
  time, at the pace a radio produces them. Constructed frames prove the relay agrees with our idea of a
  transmission; only captured ones can reveal the idea is wrong — the same
  argument `docs/architecture/testing.md` makes against fabricated fixtures.
- **Fan-out is measured rather than estimated.** One transmission to 100 peers
  is **1,650 deliveries per second of speech**; 242 frames to 99 destinations
  costs about 10 ms of CPU. BLUEPRINT-v1's arithmetic said 1,700.
- Registry and `max_peers` behaviour at 100 peers.

### Fixed
- **A correction to a correction.** The previous entry here claimed audio had
  never crossed between two stations and that `README.md` was wrong to say
  otherwise. That was itself wrong: `TestTrafficIsRelayedBetweenTwoRealPeers`
  and `TestWholeTransmissionIsRelayed` had covered it over real sockets all
  along. The genuine gap was hardware — two *physical* hotspots have never been
  connected to one instance — and the documents amended on the false premise are
  corrected here.

## [0.1.6] — 2026-08-26

Persistence, and with it the project's first dependency.

### Added
- **`docs/SOAK.md`**, the Phase 3 procedure, plus a hardened
  `deploy/systemd/qsp.service` and `deploy/soak/qsp.json`. Four windows a day
  gives 112 scheduler transitions over the fortnight rather than 28, so a fault
  surfaces in hours instead of days. The 23:30 window crosses midnight
  deliberately.
- **`modernc.org/sqlite` v1.57.0 is registered**, in `cmd/qsp/driver_sqlite.go`.
  Roughly two hundred lines of migration and storage code had never executed in
  any build, because `database.Open` always returned `ErrDriverNotRegistered`.
  It runs now.
- **[ADR-0017](docs/adr/ADR-0017-first-dependency.md)**, documenting purpose,
  licence, maintenance status and build implications — the terms ADR-0004 set
  for ever taking a dependency. `CGO_ENABLED=0` still builds for amd64, arm64
  and armv7; that was verified before the ADR was written.
- `TestPersistenceIsRealNow` and `TestSchemaSurvivesARestart`. The second is the
  property the two-week soak depends on: a restart on day nine must not lose the
  first nine days.

### Changed
- **Moved to Go 1.27** from 1.22, which left support around the 1.24 release and
  had received no security patches since. The driver requires 1.25, so this was
  forced, but it was overdue independently. No source changes were needed.
- `testConfig` takes a `*testing.T` and redirects the DSN into `t.TempDir()`.
  With a driver registered, the default relative `qsp.db` would otherwise have
  had every test write a real database beside the source and leak state between
  runs.
- `TestBuildSucceedsWithoutADatabaseDriver` now reaches the absent-driver path
  by configuring a driver that cannot exist, which is what an operator pointing
  at postgres would hit.
- `cache: true` in CI. `go.sum` exists now, so the reason for disabling it is
  gone.
- **`staticcheck` bumped from `2024.1.1` to `2026.2.1`.** The old release no
  longer compiles under Go 1.27 — the failure was an invalid array length in
  staticcheck's own source, at the install step rather than the run step. Clean
  across the version jump, on 15,800 lines. The pin did its job: the breakage
  arrived when the toolchain was deliberately changed and someone was watching,
  rather than on an unrelated day.
- ADR-0004 is amended rather than rewritten. It records a decision that was
  correct when taken and remains the default for everything else.

### Fixed
- `build`'s doc comment said the binary registers no SQL driver. It does.
- **ADR-0017's justification was wrong and is corrected in place.** It claimed
  the driver was needed so a restart during the soak would not lose evidence.
  It would not have: the audit trail goes to the log, and nothing writes to the
  database at all. The dependency is still worth taking, for narrower reasons
  now stated accurately.

## [0.1.5] — 2026-08-26

### Added
- **QSP refuses to start if the peer password file is readable beyond its
  owner.** It was documented as mode 0600 and never verified, so a `0644` file
  worked silently — the worst shape a security failure can take, since nothing
  at runtime distinguishes it from a correct setup. `ssh` refuses a loose
  private key for the same reason, and this follows that rather than warning and
  continuing: a warning in a log nobody reads is not a control.
- `config.CheckPeerPasswordMode` takes a `fs.FileMode` rather than a path, so it
  is tested without a filesystem and the platform decision sits with the caller.
  15 cases covering the boundaries, including that type bits are ignored and
  that the error names the fix.

### Not changed
- **The check does nothing on Windows**, in `passwordmode_windows.go`. `os.Stat`
  there does not report an ACL — it synthesises a mode from the read-only
  attribute, so an ordinary file reads as `0666` however tightly it is secured.
  Enforcing the POSIX rule would reject every correctly protected file and teach
  operators to route around a control rather than satisfy it. Split by build tag
  rather than a `runtime.GOOS` branch so the Windows binary carries no check it
  can never apply.

## [0.1.4] — 2026-08-25

The Phase 1 gate closed and the repository went to GitHub. Documentation
regenerated against both.

### Added
- `testdata/hbp/hbp-voice-live.pcap` — the first capture of a live DMR
  transmission reaching QSP, with notes.
- `docs/architecture/hbp-protocol.md` records the 2026-08-25 validation: `DMRD`
  decoded against a live radio, frame timing within 1.5 % of nominal, all 576
  LAN payloads round-tripping byte-for-byte, and both dialects captured
  concurrently.
- A warning, in three places, that Ethernet padding on sub-60-byte frames looks
  exactly like a protocol defect unless the payload is clipped to the UDP length
  field. This cost real debugging time.
- **First staticcheck run**, on the first CI run. `S1011` in
  `internal/peers/fuzz_test.go`: a copy loop replaced with a variadic append.
- CI actions bumped to `checkout@v5` and `setup-go@v6`. Node.js 20 is removed
  from GitHub runners in September 2026, so the previous versions were on a
  deadline rather than merely deprecated.
- `cache: false` in every CI job. `setup-go` keys its cache on `go.sum`, which a
  zero-dependency module does not have.

### Changed
- **`README.md` no longer claims only a handshake was validated.** It states
  what the live run proved, and adds that the two-week unattended soak has not
  started — something the previous banner left a reader free to assume.
- **`PROJECT_MEMORY.md` regenerated.** CI is green rather than never run;
  hardware validation is complete rather than partial; the critical path is now
  the Phase 3 soak.
- **Next steps reordered around the soak**, since a fortnight of wall-clock time
  is the only constraint that cannot be compressed by working harder.
  Registering a SQL driver is promoted to a prerequisite: without persistence, a
  restart at day nine loses nine days of evidence.
- **`docs/HARDWARE-TEST.md` rewritten from a run that happened.** The previous
  version had the operator key up on TG 9990 expecting parrot — a BrandMeister
  service QSP does not implement, in a procedure that requires BrandMeister off.
  It could not have worked. It now derives the talkgroup from the `TGRewrite`
  rule in `/etc/dmrgateway` and treats a climbing frame count as the gate. It
  also adds the step that cost a session: read the config file, not the
  dashboard. Windows, Linux and Pi are covered in one document rather than two
  that would drift.

### Fixed
- Two gap tables claimed a parrot session would close the repeater-ID rewrite.
  It would not, and the 2026-08-25 capture did not: one peer, forwarding off,
  nothing relayed. Closing it needs two peers.

### Not changed
- **`S1016` in `internal/peers/master.go` is suppressed with a reason.**
  staticcheck suggests converting `Ping` to `Pong` rather than naming the field.
  They are distinct wire messages sharing a shape by coincidence; a conversion
  would silently copy any field later added to both. The message is hoisted to a
  local so the `//lint:ignore` sits on the line it suppresses — the directive
  applies to the following line only, and inside a composite literal that is not
  where the diagnostic lands.

### Known gaps
- `password_file` is documented as mode 0600 and never checked. QSP starts on a
  `0644` file silently. Close before anything runs unattended.

## [0.1.3] — 2026-08-25

Documentation regenerated against the code it describes.

### Changed
- **`PROJECT_MEMORY.md` regenerated.** It described 289 tests, 12 commits and a
  seven-subsystem health report, and listed hardware validation without noting
  that the Phase 1 gate it was meant to satisfy remains open.
- **Phase-gate status is now stated explicitly**, in `PROJECT_MEMORY.md` §2 and
  §6. BLUEPRINT §16 requires hardware validation at every gate; the 2026-08-23
  session confirmed registration and keepalives but no voice frame ever reached
  QSP, so Phase 1 is code-complete and gate-open. Recording a passing test suite
  as though it were a gate is the same class of error as stale prose.
- Three gaps added to the known-gaps table that were true but unwritten: no
  authentication on any endpoint, the accuracy gate's blindness to over-claiming,
  and `overall: healthy` while ten of eleven subsystems are unavailable.
- Working conventions now record that documentation accuracy is a CI gate, and
  that two different trees must never carry the same version.

## [0.1.2] — 2026-08-24

Documentation accuracy becomes a gate rather than a habit.

### Added
- **Documentation accuracy is now a CI gate.** `cmd/qsp/docaccuracy_test.go`
  checks documented endpoints against the registered routes, paths named in
  prose against the filesystem, emptiness claims against directory contents, and
  absence claims against the health registry. Every documentation defect found
  in this project so far fails at least one of these checks. Escape hatches
  require a written reason; see `docs/architecture/testing.md`.
- **P25, AllStar, Zello and EchoLink now register health checks.** Constitution
  §3 requires an absent subsystem to report its absence, and four of them were
  reporting nothing at all — `/healthz` was silent about most of the roadmap
  while `README.md` and `PROJECT_MEMORY.md` both claimed each reported
  `unavailable`.

### Fixed
- **Seven places described a build that no longer existed.** 0.1.1 fixed this in
  the console; the same stale prose survived in `cmd/qsp/main.go` and
  `internal/protocol/doc.go` (both package docs, so `go doc` printed them),
  `ARCHITECTURE.md` §1 — which contradicted itself on the peer lifecycle within
  one paragraph — and `docs/architecture/testing.md`, which called the `testdata`
  directories empty while three captures sat committed beside it.
- **`GET /api/peers` was missing from two endpoint inventories**, in
  `SECURITY.md` and in the text an operator sees when no console is embedded. It
  returns callsigns, radio IDs and source addresses, so its absence from the
  security inventory understated what an exposed instance discloses.
- A broken reference to the SQLite driver ADR in `README.md`, found by the new
  path check.

### Changed
- **API routes are declared once**, in `server.apiRoutes`. The handler registers
  from that list and `handleNoConsole` reports from it, so the operator-facing
  endpoint list can no longer fall behind the routes actually served.
- `BLUEPRINT.md` is marked as frozen at v0.4 and no longer reads as a
  description of the current build.
- `staticcheck` is pinned in CI rather than tracking `@latest`, so an upstream
  release cannot fail a commit that changed nothing.

## [0.1.1] — 2026-08-23

First build validated against real hardware.

### Added
- **PTT-triggered bridging.** A bridge opens when somebody transmits on a
  declared endpoint and closes after a hang time. With the scheduler, this
  completes the feature the blueprint names as QSP's reason to exist.
- **Traffic counters on the console.** During hardware testing `/healthz`
  diagnosed in one request what the console could not show at all.
- Canonical GPL-3.0 licence text and a separate `COPYRIGHT` notice.
- `PROJECT_MEMORY.md`.

### Fixed
- **Shutdown hung for 15 seconds with a console tab open.** `Shutdown` waits for
  active connections but does not cancel their request contexts, so a streaming
  handler never returned. Found by an operator on the first real run.
- **The console described a build that no longer existed**, claiming no
  protocol, routing or scheduling code was present.
- **Health summaries quoted the phase plan** rather than the running instance.
- Peer addresses displayed as IPv4-mapped IPv6.

### Validated
- A WPSD hotspot completed the login handshake, registered, and held its session
  with keepalives cycling. Confirmed the auth construction, the `RPTC` field
  offsets, and the keepalive direction chosen against the specification.

## [0.1.0] — 2026-08-23

Initial development build: foundation, HBP codec, peer lifecycle, UDP listener,
call observation, routing, scheduler. 16 ADRs. Zero dependencies.
