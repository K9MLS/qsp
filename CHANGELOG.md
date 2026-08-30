# Changelog

All notable changes to QSP. Dates are UTC.

## [Unreleased]

### Added
- **A peering can be agreed from the console.** `internal/peering` had the
  invitation format and no page used it; a link was still a configuration edit,
  which is how one took a live network down.

  **Offer** generates an invitation and the passphrase behind it. They travel
  separately and the page says so where somebody about to send both in one email
  will read it: the invitation carries only a fingerprint, and the passphrase is
  shown once and never readable from the console again.

  **Accept** takes an invitation and the passphrase, and is two steps
  deliberately. The first reads the token and shows who is asking; the second
  writes the link, a bridge to carry it, and the passphrase file. `confirm` is a
  required flag on the request, so a peering cannot be created by a call made in
  passing — ADR-0032 says a peering is agreed by two people, and one click is
  not agreement.

  The bridge is written alongside the link because a link with nothing routing
  to it opens, authenticates, and carries nothing. That is the failure this page
  exists after.

  The reply carries the agreed passphrase's fingerprint rather than a new
  secret. OpenBridge authenticates every datagram against one shared passphrase,
  and a second would produce a link that works one way while both ends report
  healthy.

  An audit event names the far end's callsign and address on both success and
  failure — a failed acceptance is worth having later, and its absence would
  suggest nobody tried. The passphrase is written beside `dmr.password_file` at
  mode 0600; configuration is versioned, stored in the database, and shown in a
  console, so a secret in it is a secret in all three.

  Neither endpoint checks that a callsign belongs to whoever sent the
  invitation. It is a claim, checkable against RadioID.net by a person, and an
  operator agreeing to peer has already decided who they are dealing with.

- **A Links page, because nothing showed a link anywhere.** One opened,
  authenticated, and carried audio between two servers on two machines, and the
  only report of any of it was a line in `/healthz`. An operator asked twice
  where to see it and the honest answer both times was that there was nowhere.
  A working link and a dead one looked identical from a console, which cost an
  afternoon of deciding which it was.

  Each link shows its far end, protocol, announced network ID, and **frames
  sent, received and rejected separately**. One direction is not evidence of the
  other: a link that has sent thousands and received none is working perfectly
  on a quiet network, or is unauthenticated at the far end, and no single number
  tells those apart. Rejected is the one that names a passphrase the two ends
  disagree about, which is otherwise indistinguishable from silence.

  `GET /api/links` **requires a session, unlike `/api/peers`.** A peer list
  describes stations whose operators chose to join this network. A link names
  somebody else's server, its address, and whether their passphrase is
  verifying — theirs to disclose rather than this instance's.

  ADR-0032 names a console view of every link as required rather than optional,
  and this is that half of it. Creating a link from the console — the paste-an-
  invitation flow `internal/peering` exists for — is not built yet; a link is
  still added by editing configuration.

- **`qsp -config <file> -check` validates a configuration and exits**, binding
  nothing, opening no database, dropping no member. It exists because a
  configuration edit was verified by restarting the service, an invalid file
  then stopped a live network, and systemd gave up after five attempts. The
  validation was always there; the only thing missing was a way to ask for it
  without a restart. Exit status is non-zero and the reason names the field.

### Fixed
- **Two validation rules that did not know about each other stopped a live
  network.** One required an enabled link to carry `export` or `import`. The
  other, added the same afternoon, required a bridge to name it. A
  configuration with a bridge and no export satisfied one and failed the
  other — and the failure was a service that would not start, on a network with
  a member connected.

  **The export rule is gone rather than reconciled.** Those lists route
  nothing: no code reads them to move a frame, which is why bridges gained the
  ability to name a link in the first place. Requiring a field that does
  nothing, alongside a rule requiring the field that does, is worse than either
  alone. `Upstream.Export` and `Upstream.Import` stay in the schema — removing a
  documented field would refuse configurations already written against it — and
  now say plainly in their own documentation that they carry nothing yet.

  `TestUpstreamCarryingNothingIsRefused` asserted the removed rule and is
  replaced by one that checks what actually carries traffic, plus
  `TestTheConfigurationThatStoppedALiveNetwork`, which is the exact shape of the
  file that broke.

  The claim that `Export` and `Import` are read by nothing was made earlier the
  same day and was half wrong: no *router* reads them, and a *validator* did.
  It was arrived at by grepping for readers of the field, and the rule that
  required them does not mention them by name.

- **`internal/peering` makes a link between two instances something two
  administrators agree to.** Two instances were run on one machine and linked
  without either being asked to confirm anything. The link *was* consented to —
  OpenBridge has no connection, so it exists only because both sides hold a
  passphrase agreed out of band — but a script wrote both halves, which made
  real consent look automatic. Nothing in a console showed the far end, nothing
  recorded who agreed, and `pair-test-passphrase` was accepted without
  complaint.

  Invisible consent is worth very little. Between two instances on one desk that
  is untidy; when club #2 is a different person's server it is the difference
  between a network and an open relay.

  **Nothing changes on the wire.** A QSP-only handshake would mean QSP peers
  with QSP and nothing else, which is the opposite of the point — ADR-0018 chose
  OpenBridge for interoperability and that is not reopened. The out-of-band
  exchange becomes an artefact instead: one line of text an administrator sends
  by whatever channel they already trust, pasted into the other console, which
  shows who is asking and what is proposed before anything is written.

  **The passphrase does not travel with the invitation.** The token carries a
  fingerprint and never the secret. `internal/hotspot` already refuses to put a
  peer password in generated configuration on the grounds that it is the one
  thing that must not travel by email, and applying that to a club member but
  not to a peering carrying the whole network's audio would be incoherent. A
  mistyped passphrase then fails at the paste rather than as silence on a link
  that reports itself configured.

  QSP generates the passphrase — 256 bits from `crypto/rand` — and refuses one
  under 24 characters even when it matches the fingerprint. Invitations expire
  after fourteen days, and expiry is reported before a mismatch so an
  administrator is told to ask for a new one rather than sent hunting for a
  secret that would not have worked. The reply carries the agreed secret rather
  than a new one, since a second passphrase produces a link that works one way
  while both ends report healthy.

  A truncated paste says so: the token carries a CRC, because half a token in an
  email otherwise decodes into a plausible invitation with a wrong address.

  Recorded in [ADR-0032](docs/adr/ADR-0032-peering-is-agreed.md), which also
  writes down what this does not do — it does not verify that a callsign belongs
  to whoever sent it, and OpenBridge authenticates without encrypting and has no
  replay protection.

  Pure, no I/O. Not yet wired to the console.

- **`deploy/pair` and `scripts/pair.sh` run two instances peered to each
  other.** Every upstream path in this project is code that has never met a far
  end: OpenBridge written from its specification, outbound peer mode from
  ADR-0024, both exercised only by tests that supply their own other side. A
  test that provides both halves of a conversation proves the halves agree, not
  that either is right.

  Two processes on loopback, ports nothing else uses, everything under `/tmp`,
  and nothing touching the production instance. `docs/FEDERATION-TEST.md` says
  what to watch and — more usefully — what it does not prove: no NAT, no MTU
  limit, no jitter, and nothing at all about whether a radio opens its squelch
  at the far end.

  The pair exports and imports the same talkgroup on purpose. That is the
  ordinary club configuration and the one that would loop, so it is the one
  worth watching not loop with a real socket in between.

- **`TestShippedExamplesAreValid` loads every configuration under `deploy/`.**
  A broken example is worse than none: somebody following it has no reason to
  doubt a file the project ships, so a typo is debugged as a fault in QSP.
  Nothing read these files before. It found two faults in the pair
  configurations on its first run.

  **`TestThePairFacesItself`** reads both halves together, because OpenBridge
  has no connection establishment and each end sends to an address agreed in
  advance. A mismatched port pair produces a link reporting itself healthy while
  carrying nothing in one direction.

### Changed
- **[ADR-0031](docs/adr/ADR-0031-loop-prevention.md) is amended before it was
  ever implemented, because it overstated the hazard it was written for.** It
  argued that loops were open and that no link should carry traffic until a
  fingerprint scheme existed.

  `routing.Core.route` already refuses to send a frame that arrived on a link to
  any link, and `RouteFromUpstream` already documents why — a club exporting and
  importing one talkgroup relays every frame from BrandMeister straight back to
  BrandMeister — and already rejects the hop count the ADR considered, on the
  grounds that it needs every participant to cooperate. That rule is stronger
  than the one proposed: it does not detect loops, it makes them unformable.

  The record was written after searching for `StreamID` and `sourceKey` and not
  reading `route` past the point that answered them. What survives is duplicate
  suppression — two links to one far network, or a far end reaching this
  instance twice — which is real and much less urgent. The claim that peering
  must wait is withdrawn, and the recommendation that federation stay shallow is
  restated as what the code already enforces rather than as advice.

- **The join page generates the DMRGateway block, and stops assuming a member
  runs nothing else.** Step one told every member to turn BrandMeister off. For
  a quarter of this club that is wrong: they follow it and lose a network they
  wanted, or ignore it and get a talkgroup collision instead — which presents as
  transmitting into silence with every log healthy.

  The page asks first. A single-network member gets the instruction as before. A
  multi-network member gets a generated `[DMR Network N]` block that puts QSP
  behind a leading digit and leaves their other networks alone.

  **Both choices belong to the member.** Which leading digit and which network
  slot are free is answerable only from `/etc/dmrgateway` on their own hotspot,
  and the rewrite happens there before anything reaches QSP — so one member
  choosing 7 and another choosing 3 affects neither the network nor each other.
  That is why they are controls on the page rather than settings an
  administrator fills in once.

- **`GET /api/join/config`**, which renders the block. It returns nothing
  `/api/join` does not already return, arranged as configuration, with the
  password left as a placeholder that is never substituted.

  **The radio ID is observed, never derived.** QSP has seen the ID of every
  radio that transmitted through a peer, and `CallView.Source` is the radio
  rather than the hotspot. Stripping a peer ID's two-digit suffix would be
  arithmetic on a convention the access work already established is not a rule
  of the protocol, and a private call rule naming the wrong radio sends a
  member's texts somewhere they will never look. Unknown produces no private
  call rules and says so on the page.

  `JoinSettings` carries the parrot talkgroup now, so the generated block can
  convert it from a group call to a private one with `TypeRewrite` on the
  hotspot — which is how a member keeps the parrot already in their codeplug
  while QSP still never rewrites a Link Control (ADR-0028).

### Fixed
- **Nothing could send traffic to a link. The fields that were supposed to were
  read by no code at all.** Two instances peered over OpenBridge: both sockets
  opened, the far end authenticated, keepalives flowed both ways for an hour,
  and six transmissions on the bridged talkgroup were routed to local peers and
  to nothing else.

  `routing.Endpoint` has carried an `Upstream` since links existed, and the only
  place it was ever set was `RouteFromUpstream`, on the inbound side. So traffic
  could arrive from another network and never leave for one. `config.Endpoint` —
  what a bridge is built from — had `Peer`, `Talkgroup` and `Timeslot` and no
  way to name a link, so **outbound was unreachable from any configuration a
  person could write.**

  `Upstream.Export` and `Upstream.Import` were validated, stored, documented as
  what crosses in each direction, and consulted by nothing. A search of every
  `.go` file finds the field declarations and no reader.

  A bridge endpoint can name a link now, which makes links first-class in the
  mechanism that already handles scheduling, triggers and enabling rather than
  a second parallel one.

  **And a link nothing routes to is refused at startup.** That is the shape of
  this fault: a socket that opens, authenticates, reports no traffic, and sends
  an operator to check somebody else's address. It is a configuration error and
  it now says so before the process runs.

  Recorded honestly: this was asserted the wrong way round earlier today. When
  a bridge endpoint naming an `upstream` was rejected as an unknown field, that
  was read as using the wrong mechanism and written into a commit message as
  though it were a finding. The error was reporting that the feature did not
  exist.

- **The `hidden` attribute did nothing on any element whose class set a display
  mode.** `[hidden] { display: none }` comes from the user-agent stylesheet, and
  any author rule with a class selector outranks it — so `.empty { display:
  flex }` left the access page's "Loading — reading this instance's
  configuration" panel on screen above a form that had already finished loading
  and rendered underneath it.

  The scripts were correct. Every `hide()` set the attribute exactly as
  intended, and the attribute was ignored. **The access page had never worked**,
  and an operator said so; nothing in the suite had ever noticed, because the
  suite reads the script and the script is right.

  `tokens.css` now declares `[hidden] { display: none !important }`. This is the
  one place `!important` is the correct tool: `hidden` is not a suggestion, and
  the alternative is remembering to re-hide in every component that sets
  `display`, which is the arrangement that produced the fault.

  `TestHiddenMeansHidden` also had to be corrected before it could fail: its
  first version searched for the phrase `[hidden] {`, found it inside the
  comment that quotes the user-agent rule to explain the bug, and measured the
  comment. That is the same fault as the wrapping check earlier today —
  asserting something adjacent to the thing that matters. It anchors to the
  start of a line now.

- **The pair harness used a talkgroup nobody had programmed.** It carried TG 9
  because the soak configuration does; this network runs TG 2. A test rig on a
  talkgroup that is not in the radio costs a codeplug edit every time it is
  used, which is a reliable way to ensure it never gets used. Both
  configurations now carry TG 2 TS2, and `docs/FEDERATION-TEST.md` says to
  change `export`, `import` and `join.talkgroups` together for a club that runs
  something else.

- **The pair harness could not receive a frame from anything.** Both DMR
  listeners bound `127.0.0.1`, which is tidy and made the whole rig useless: no
  hotspot on the LAN could reach either instance, so the only traffic either
  ever saw was the console polling itself. Watching bravo's log during the first
  run showed `/healthz` and `/api/peers` and nothing else, which reads exactly
  like a broken link and was a harness with no way in.

  The listeners bind `0.0.0.0` now. Both configurations already carry an
  explicit permit-everything `access` block, which is the deliberate statement
  ADR-0020 asks for. `TestThePairFacesItself` fails if either listener returns
  to loopback, and compares bind ports rather than addresses — `0.0.0.0:62041`
  and `127.0.0.1:62041` are different strings and the same socket, so comparing
  addresses would pass a pair that cannot both start.

  `docs/FEDERATION-TEST.md` also records what a capture cannot do: the login
  handshake answers a challenge whose salt differs every time, so a recorded
  session from `testdata/hbp` will not authenticate. Traffic without a radio
  needs a tool that speaks the client side.

- **A link reported open, blamed the far end, and could never have carried
  anything.** Two instances peered over OpenBridge with `dmr.forwarding` off:
  each logged `link open` with its target and listening addresses, bound its
  socket, and then reported degraded with advice to confirm the far end's public
  address and that UDP was reaching the port. The routing table is built only
  inside `if cfg.DMR.Forwarding`, so no frame was ever offered to either link.

  Three statements, each true, together sending an operator to debug somebody
  else's network over a fault three lines above in their own log. A link now
  says so itself: open, forwarding off, nothing can reach it.

  **Found by running two processes.** The suite passed on both configurations,
  including `TestShippedExamplesAreValid`, written the same day to catch exactly
  this class of thing.

- **The configuration validator refused the ordinary club network.**
  `dmr.forwarding` with no bridges was rejected as relaying nothing — true while
  bridging was the whole routing model, false since ADR-0019 made a repeating
  master the ordinary case. The health check was corrected for ADR-0019 and the
  validator was not, so the `Forwarding` field's own documentation described a
  configuration its validator refused, twenty lines apart in one file.

  Inverting the rule was the second mistake and survived one test run: off with
  no bridges is the observation mode the field exists to provide — running as a
  master and watching peers connect before putting audio on anybody's repeater.

  **Both states are legitimate, so the rule is gone rather than reversed.** It
  was a judgement about what an operator probably meant, and that belongs in the
  health report, which already says plainly that forwarding is off and peers
  cannot hear each other. A validator refuses what cannot work; it does not
  guess at intent. `TestForwardingWithoutBridgesIsRejected` is replaced by two
  tests asserting each state is valid.

  The pair configurations carried `forwarding: false` only to satisfy that rule,
  which is why the harness shipped unable to relay in either direction.

- **A test that could not fail, found while checking that it could.**
  `TestTheGeneratedBlockIsNotWrapped` asserts the generated configuration uses
  `white-space: pre`, because a DMRGateway rule broken across two lines is one a
  member pastes as two. The first version tested for the substring `white-space:
  pre` — which `pre-wrap` contains — so it passed against the exact value it
  exists to reject. It now requires the terminating semicolon and names the
  wrapping values explicitly. Confirmed by setting `pre-wrap` and watching both
  assertions fire.

- **`internal/hotspot` generates the network block a member pastes into their
  own hotspot.** The join page describes the settings; describing them is what
  costs the evening, because a member reading prose and typing rewrite rules is
  a member making one of the two mistakes that took the first hotspot two days
  to connect.

  **The syntax is taken from a working `/etc/dmrgateway`, not from
  documentation.** Five networks in that file — BrandMeister, DMR+ IPSC2,
  HBLink and SystemX among them — and three of them implement the same prefix
  scheme with a different leading digit. That is the pattern generated here: a
  blanket seven-digit rule so a talkgroup added later is reachable without
  editing anything, shortcuts for the talkgroups the club publishes, private
  call rules in both directions, and source rules so a reply displays the
  number that was dialled.

  **The prefix belongs to the member, not to the club.** The rewrite happens on
  their hotspot before anything reaches QSP, so two members may choose different
  digits with no effect on each other or on the network — and QSP cannot choose
  for them, because the only file that says which digits are free is the one on
  their own Pi.

  Zero prefix means QSP is the only network, which is most members: no rewrite
  rules at all, just `PassAllTG` and `PassAllPC` on both slots. Every rule is a
  thing that can be wrong, and rules serving no purpose are maintenance.

  `Enabled=1` is the first setting in the block, because a dashboard reporting a
  network as enabled while the file said otherwise is what cost the two days.
  **No radio ID means no private call rules** rather than a guess: the two-digit
  suffix is a convention and not a rule of the protocol, and a rule naming the
  wrong radio sends a member's texts somewhere they will never look. The parrot
  is converted from a group call to a private one by `TypeRewrite` on the
  hotspot, which is how a member keeps the parrot they already have programmed
  without QSP ever rewriting a Link Control (ADR-0028).

  The password is never generated. Warnings cover what the generator cannot
  check: whether the prefix collides, whether the block number is free, and that
  `/etc/dmrgateway` is edited by searching for a line and never by its number.

  Pure, no I/O, no state. Not yet wired to the join page.

- **[ADR-0031](docs/adr/ADR-0031-loop-prevention.md) decides how a looped
  transmission is recognised, before any link can create one.** Every instance
  is a leaf today, so nothing loops because nothing connects — and that is
  exactly why this is written now. A protocol behaviour is a commitment to every
  instance already speaking it, and once clubs are federated the rule cannot be
  changed without changing all of them at once.

  **The field naming where a frame came from is rewritten in transit; the field
  naming which transmission it belongs to is not.** `openbridge.Encode` assigns
  the local network ID into `RepeaterID` on every frame it sends, so a relayed
  frame carries the last server's identity and no trace of the first. `StreamID`
  and `SourceID` survive — the codec's own comment records the voice fixture
  showing one StreamID on both links as a gateway relays.

  So a transmission is fingerprinted as `(SourceID, StreamID)` and the first
  ingress path to present it owns it for the life of the stream. `sourceKey`
  cannot be reused: it keys on `RepeaterID`, which OpenBridge overwrites, and on
  the arriving link, which is the thing that differs between the original and
  the looped copy. Keyed that way a returning frame looks like a new
  transmission from a different sender.

  No field is added to the wire. A hop count would fit in the bytes QSP already
  preserves as `Trailing`, and putting one there would produce frames that
  behave differently depending on who relays them, failing at the far end of
  somebody else's network. The rule lives entirely in the receiver, so a QSP
  peering with a a commercial DMR server is protected by it too.

  Also recorded: parrot must take a fresh StreamID, or a replay is dropped as a
  loop of what it is replaying; federation stays shallow, because each hop adds
  a jitter buffer and a 60 ms cadence does not forgive four of them; and the
  first federated link is a scheduled point-to-point one between two
  administrators who can telephone each other, because no unit test can show
  that a real relay preserves StreamID across a real peering.

- **`console/static/nav.js`, and the administration nav says when its links lead
  nowhere.** Signed out, the console offered four administration pages that can
  only ever answer with a sign-in notice. Nothing behind them leaks — every
  admin page keeps its form hidden until `/api/config` answers, and
  `/api/config` is behind `requireSession` — but a dead end presented as a
  destination is its own defect. The group carries one line saying so.

  The links stay live. Disabling a control that would in fact respond is a
  different lie from the one being fixed.

  It also ends five copies of the same `/api/session` fetch, of which **only one
  could sign out**: the overview page grew a sign-out button and the other four
  did not, which is drift rather than a decision anybody made. Sign-out now
  works from every page with a nav. A failed fetch asserts nothing rather than
  reporting a signed-out session it cannot vouch for.

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

- **Table rows alternate, and highlight under the pointer.** Following one row
  across a wide table is exactly what gets hard when there is enough traffic for
  it to matter. Both shades darken rather than lighten, which takes text
  contrast up rather than down, and the contrast test now measures a striped and
  a hovered row as surfaces in their own right.

- **Panels stay one column at every width.** Two columns were tried and
  reverted: connected peers and health both carry columns that need width, and
  halving the page put health's detail column back to being cut off — the exact
  defect the full-width tables were meant to fix. A tall page that can be read
  beats a compact one that cannot.

- **Wide tables scroll instead of clipping.** `.table` was `width: 100%`, so a
  table squeezed itself into whatever space it was given and the cells
  overflowed instead — the scroll container never engaged and the panel's
  `overflow: hidden` cut the text off. A peer's address rendered as `192.168.`
  with no way to see the rest. Each scroll region is now reachable by keyboard
  and named for a screen reader.

  Columns holding prose wrap; columns holding identifiers, times and statuses do
  not. `nowrap` on every cell is right for a radio ID, where a break mid-value is
  worse than a wider table, and wrong for the health summaries, which are
  sentences and pushed that table past its panel.

- **The page description collapses.** It is read once and scrolled past
  forever, and it was occupying the top of a page an operator is monitoring. It
  stays in the markup and in the accessibility tree; it just no longer takes the
  best space on the screen by default. A chevron marks it as openable, since
  laying the summary out with flex suppresses the browser's own marker and it
  rendered as plain text with nothing to say it could be clicked.

- **The console's panels are separated from the page.** Surface and background
  differed by 1.09:1, which is not a difference anyone can see, so a column of
  panels read as one continuous area with hairlines drawn on it — reported by an
  operator looking at the screen, which is becoming a theme.

  The page is darker, the surface lighter, and panels finally use
  `--shadow-raised`, which had been defined in `tokens.css` and applied nowhere.
  Most of the perceptual separation comes from the shadow: there is very little
  room above 1.27:1 before subtle text on a panel breaks the 4.5:1 floor.

  A panel's header band now darkens rather than lightens, because lightening it
  by the same amount put subtle text at 4.39:1. Panels space themselves in CSS
  instead of each carrying an inline margin that two of them disagreed about.
  An empty state inside a panel drops its dashed border, since it was drawing a
  box inside a box.

- **The console has a health page.** The Health link went to `/healthz` and
  showed the operator raw JSON. The report was already being fetched every few
  seconds for the status pill, with everything else thrown away; it now renders
  as a table of subsystems, each with its own verdict, and an unavailable one
  names the phase that brings it — a roadmap rather than a fault.

- **The configuration endpoints.** `GET /api/config` reads the running
  configuration, `POST /api/config` saves a new one, and
  `GET /api/config/versions` lists the history. All three require a logged-in
  administrator and refuse a cross-origin write. **QSP can now be reconfigured
  without SSH**, which is the last thing the parity document marks against it.

  A save validates, records a version, writes the file, and queues the change
  for the goroutine that owns the routing core, in that order. A bridge added
  from a browser is live within a second and nobody mid-transmission is cut off.

  The response says what changed and names any setting that was saved and cannot
  take effect until a restart — a bare "restart required" would tell an operator
  to interrupt their network without saying what for. An invalid configuration
  reports every problem at once with a suggested fix for each, because somebody
  fixing a form should see all of it rather than discovering the next one on
  each attempt. A read-only instance says so when the console asks, before a
  form is filled in, rather than at the moment somebody presses save.

  Every save is an audit event naming the administrator, **including a save that
  failed** — an operator who could not save is a fact worth having, and no
  record would look as though nobody tried. That is what `audit_events.actor`
  has been waiting for since it was created.

  Saving an unchanged configuration records nothing, so a form saved without an
  edit does not fill the history with identical versions.

- **The reload handoff and the version store.** A configuration change is
  queued with `Listener.Apply` and installed by the listener's own goroutine at
  the top of its next sweep — because `routing.Core` is single-writer and owned
  by that goroutine, so an HTTP handler calling `SetTable` is a data race the
  detector would only sometimes catch.

  A second save before the first is applied replaces it: two saves a half-second
  apart should leave the instance running the later one. Access lists are
  applied unconditionally, since their zero value permits everything and is a
  setting rather than an absence — skipping it when empty would make "remove
  every restriction" impossible to save. A static attachment that is no longer
  configured becomes dynamic rather than disappearing, so a member using it does
  not lose it because an administrator saved something unrelated.

  `SQLVersionStore` is three statements, checked against the migration the same
  way the auth SQL is. That check first examined five of twenty-seven column
  references, because its identifier pattern required an underscore and most of
  these columns do not have one.

- **The configuration writer and the restart check**, the first code
  [ADR-0027](docs/adr/ADR-0027-configuration-writes.md) calls for.

  A save is a temporary file in the same directory and a rename, so an instance
  that restarts mid-write starts with a whole configuration or the old one, and
  never with half of either. The file's mode is preserved — a configuration that
  was 0600 must not become world-readable because somebody pressed save — and an
  invalid configuration is never written at all, since one that cannot be loaded
  again strands the operator at the next restart.

  `Writable` reports whether a save could succeed without attempting one, so the
  console can say it is read-only up front rather than after a form has been
  filled in. It checks the directory, not just the file: the write is a rename,
  so a writable file in a read-only directory still cannot be saved.

  `NeedsRestart` returns the fields that changed and cannot take effect live,
  rather than a boolean. "Restart required" tells an operator to interrupt their
  network without saying what for, and they will reasonably want to know whether
  it can wait until the net is over. Upstreams are compared by encoding rather
  than field by field, so a field added to them later is noticed without anybody
  remembering to update the check.

- **[ADR-0027](docs/adr/ADR-0027-configuration-writes.md) decides the
  configuration write path**, which is what the admin interface needs before any
  of its forms are worth building. Three questions, none of them about forms.

  **The file stays the source of truth and a save writes it.** The database
  becoming authoritative after the first save would silently ignore a
  hand-edited file from then on — a trap laid for exactly the operator this
  project is written for, who would edit, restart, and find the change gone with
  no error. `configuration_versions` is history, and a rollback works by writing
  the file again.

  **An HTTP handler cannot apply a change.** `routing.Core` is single-writer and
  owned by the listener's goroutine ([ADR-0002](docs/adr/ADR-0002-single-writer-routing-core.md)),
  so calling `SetTable` from a handler is a data race the detector would only
  sometimes catch. A save hands the configuration over and the listener applies
  it on its next sweep, within a second.

  **Some settings cannot apply at all** — listen addresses, the password file,
  the database, the sockets an upstream holds. Those are saved and not applied,
  and the save says so and names the field. Refusing to save them would make the
  interface unable to configure half of QSP; pretending they took effect would
  be worse than either.

- **A sign-in page.** The login API existed with no way to use it — an
  omission spotted by looking at the console rather than at the code. `/signin`
  has a form, and the console's topbar shows who is signed in with a link to
  sign in or out.

  **The console still needs no account**, and saying "Sign in" rather than
  demanding it is the difference: everything it shows is readable without one,
  and this is a way in for the administrator rather than a gate in front of
  everybody. An instance with no accounts says so and gives the `adduser`
  command, because a form that cannot succeed is worse than no form.

  The password field is cleared on every outcome, not only on success, since a
  failed attempt otherwise leaves it in a form on a screen somebody may walk
  away from. A disabled button is muted rather than translucent: an opacity
  there would drop its label under the contrast floor, which is a mistake this
  project has already made once.

- **`qsp unlock <username>` clears a lockout**, and expired sessions are now
  swept hourly. Both were gaps noticed while the login was being tested by hand:
  five wrong passwords meant a real fifteen-minute wait with no way out, and
  `SweepSessions` existed with nothing calling it, so the table grew one row per
  login for the life of the instance.

  Unlock clears attempts and nothing else — an operator running it must not find
  their passphrase reset. The sweep is hourly because nothing depends on it
  being prompt: an expired session is already refused and deleted on sight, so
  this only reclaims rows.

### Fixed
- **A hint opened beside its title instead of beneath it, and had done since it
  was written.** `.panel__head` is `display: flex; justify-content:
  space-between` and the disclosure paragraph was a sibling of the title inside
  it. Closed, the button was distributed into the dead centre of the header
  band, touching nothing and explaining nothing. Opened, the paragraph became a
  fourth item on the same row — beside the button, jammed against the count,
  pulled up by the row's baseline alignment.

  Neither rule was wrong. A `space-between` header is right and a disclosure
  that positions nothing is right; the pair was wrong, and no amount of reading
  either one would have found it.

  **The existing test passed throughout.**
  `TestHintsAreDisclosuresRatherThanTooltips` asserted the mechanism — hints.js
  never calls `getBoundingClientRect`, always sets `aria-expanded`, wires
  idempotently — every word of which was true while the thing rendered wrongly.
  `TestAHintOpensBeneathItsHeading` asserts the outcome instead: it walks div
  nesting to find each header band and fails if anything expandable is inside
  one. A regex would stop at the first `</div>` — the one closing the heading
  group — and report success for exactly the markup it is meant to catch.

  Every disclosure now sits in a `.panel__disclosure` band between the header
  and the content; the title and its button are grouped in `.panel__heading`, so
  `space-between` distributes two items rather than three.

- **`.hint` was two rules six hundred lines apart.** A block paragraph with
  padding and a top border, and a 20px circular button. Same class, same
  specificity, so the later won — and the overview page's "no voice frames yet,
  only keepalives" note was being drawn as a 20px circle with its text spilling
  out of it. The note is `.inline-note` now and a test fails if `.hint {` opens
  more than once.

- **The hint button's outline failed WCAG 1.4.11 at 1.64:1.** It used
  `--color-border-strong`, which is a colour for separating two surfaces rather
  than for the visible edge of something a user has to find and click. New
  `--color-border-control` measures 3.77:1 on a panel header band and 3.40:1 on
  a panel surface — both, because the same button appears in both places.

  The contrast tests measured text and nothing else, so the one part of this
  control that failed was the one part nothing checked.
  `TestControlBoundariesMeetTheNonTextFloor` covers non-text boundaries at the
  3:1 floor, and fails if the token is defined and referenced by nothing.

- **The hint button was a 20px target against a declared 44px minimum.**
  `--target-min` existed in `tokens.css` and was referenced by nothing. The
  circle is 28px and the target is 44px, via a centred overlay: padding would
  have grown the circle with it and punched a hole in the header band.

- **Parrot answers a group call, and the private-call replay never worked.**
  Five replays went out at correct DMR timing to a radio that played none of
  them.

  **A DMR voice header carries the call's addressing inside the 33-byte burst**,
  in the Link Control, under its own error correction — and a radio believes the
  Link Control rather than the wrapper around it. Swapping the source and target
  in the wrapper, which is what the previous patch did, only made the two
  disagree: the frames arrived saying "private call to 9990" and were muted.

  Rewriting the Link Control means decoding and re-encoding a DMR burst, which
  is exactly what QSP does not do and what lets parrot exist without a vocoder.
  The swap is removed, and the network settings page says to program parrot as a
  group call — replayed unchanged, the Link Control still says "group call to
  this talkgroup" and a radio with it in the receive list un-mutes with nothing
  rewritten anywhere.

  A private parrot stays possible and is a different piece of work: the first
  place QSP would have to understand a burst rather than carry it.

- **The console reported "Cannot reach QSP" on a working instance.** Removing
  the map deleted a neighbouring function, `renderRefused`, and left its call
  site — so every poll threw, the catch reported the instance unreachable, and
  the peer list and traffic counters went blank while the server was perfectly
  healthy.

  A test now reads every console script and fails if it calls a function nothing
  in that file defines. JavaScript has no compiler to notice, and nothing else
  here read the files for consistency. Confirmed by deleting the same function
  again and watching it fail.

- **A hotspot behind a rebinding router lost its session every eight to nine
  minutes.** QSP requires a peer to authenticate again when its source address
  changes ([ADR-0011](docs/adr/ADR-0011-nat-rebind.md)) and dropped the
  mismatched keepalives **in silence** — so the peer only recovered when its own
  timeout fired, and the network was dead for that member until it did.

  Measured on a live instance: `Login to the master has failed, retrying login`
  on that cycle all day, from the moment the hotspot first connected, with
  nobody noticing because reconnection worked.

  A mismatched keepalive is answered with `MSTNAK` now, which is what an
  *unregistered* keepalive already received and for the same reason. **The rule
  is unchanged** — the peer still completes the whole handshake from its new
  address before passing traffic. The cost is a reflection vector, and a test
  asserts the answer stays smaller than the request so it cannot quietly become
  an amplification one.

  ADR-0011 asked for exactly this field validation and is amended with it. The
  rule was right; the silence was not.

- **Radio ID lookups never ran.** The console's view source captures the
  resolver by value and was built before the resolver was assigned, so the view
  held nil: no ID was ever queued, the cache stayed empty, and the instance
  logged `radio ID lookups enabled` throughout.

  Nothing failed. The feature was simply never reached, which is why an
  operator's live instance sat with lookups on, a migrated `callsigns` table,
  and not one row in it. Found by checking the table rather than by trusting the
  startup line.

  The resolver is now built immediately after the migrations, before anything
  that could capture it. A test reads `app.go` and fails if a reader appears
  first — ordering inside one function is not something the compiler checks and
  not something a unit test can reach.

- **Hints on every admin section, including the ones drawn by script.** The
  four access lists each explain themselves now: that an empty deny list carries
  everything, that the two timeslots are independent paths, that registration is
  checked before a password so the log can say which refused a station, and that
  a refused subscriber is silenced without disconnecting the hotspot carrying
  it. The history page explains that a restore is itself a save.

  `hints.js` exposes a wiring function, because a form drawn after the script
  runs would otherwise have buttons that do nothing — worse than no buttons.
  Wiring is idempotent, since a page that re-renders on a revert would otherwise
  double up the handlers and produce a hint that never opens.

- **Hints on the admin pages.** A button beside each panel heading reveals an
  explanation: what a talkgroup list mode actually does, why registration and
  subscriber checks differ, what dialled and arrives mean, why parrot wants a
  group call, and that a scheduled bridge is off outside its windows whatever
  its own setting says.

  **They are disclosures, not floating tooltips.** Anything floating has to be
  positioned, and positioning against a measured box is the class of bug that
  cost this project a day. Hover is also unavailable on a touch screen and
  unreachable from a keyboard, so a button is both the accessible answer and the
  robust one — nothing about a hint is positioned at all.

  The text is in the markup rather than the script, so a page without the script
  still carries its explanations and somebody reading the HTML can see them.

- **The map works, and the cause was QSP's own content security policy.**
  `style-src 'self'` forbids inline style attributes, so every
  `style="left:…"` on a tile or pin was silently refused and all of them stacked
  at their container's origin — in a corner while the plane was at the corner,
  at the centre once it was centred. Both screenshots were one fault seen twice.

  Positions are set through `element.style` now, which is CSSOM and which the
  policy permits, so the policy is unchanged. **This is the third feature QSP's
  own headers broke**: `img-src` blocked the tiles, `Referrer-Policy` made the
  tile server refuse them, and `style-src` stopped them being placed.

  Verified against a DOM that throws on a style attribute exactly as the policy
  does: 27 tiles spanning the frame and two pins straddling the centre, with no
  attribute written anywhere. A test now fails if any console script writes one,
  by either route.

- **The map is back, and the bug was in the design.** Every position was
  computed from a measured width, and the measurement was wrong: the caption
  added to make the map report itself showed a frame of 1920 against a canvas of
  1550, because the size routine took the largest width in the element's
  ancestry and found the viewport.

  **Positions are offsets from the map's centre now.** A tile goes at
  `tx * 256 - centreX`, the plane is centred by CSS, and no width appears in the
  arithmetic. Verified against a simulated DOM at frame sizes from 1550×360 down
  to 50×50: a single peer lands at offset zero every time, two peers straddle
  the centre. A measurement cannot misplace what it is not used to place.

  Six attempts argued about which measurement to trust. None asked why a
  measurement was being trusted at all.

### Removed
- **The peer map.** It was built, deployed, and could not be made to work on the
  instance running it. [ADR-0025](docs/adr/ADR-0025-no-bundled-map.md) records
  the withdrawal alongside the design, because a decision reversed is worth
  being able to read.

  What was established: the arithmetic is correct — run against a simulated DOM
  at frame widths from 1520 down to 10, it emitted 21 to 24 tiles every time,
  positioned across the frame with the marker at the centre — and the instance
  served that exact file, confirmed through the proxy. The rendered page showed
  one tile in a corner regardless. **The positions computed are not the
  positions rendered, and nothing available from the source side can find out
  why.**

  Six attempts were made, each reasoning about which measurement was at fault,
  each producing the same screenshot. A feature that cannot be made to work by
  the person maintaining it is not a feature.

  Peer positions remain on `/api/peers`, and the peers table still shows each
  peer's location with a link out to a map — which answers "where is this
  station", the question actually being asked. A club that wants a map can build
  one against the API and will be able to see what it is doing.

- **The map reports what it measured and drew**, in a line beneath it: tile
  count, frame size, canvas size, zoom.

  This exists because the map has been wrong five times and every fix was a
  guess about which measurement was at fault. Running the real `map.js` against
  a simulated DOM settles the question from this side — **at frame sizes from
  1520×360 down to 10×10 it emits 21 to 24 tiles every time**, so the code
  cannot produce the single tile that keeps appearing. Whatever is executing in
  the browser is not this file, and nothing in the source can discover that from
  the inside.

- **Radio IDs resolve to names in the console.** `dmr.callsigns.enabled` with a
  contact address turns it on; Last heard then shows a callsign and given name
  beside the number for radios the registry knows.

  **A hotspot's own registration still wins.** That is the station describing
  itself, and it is right more often than a registry for the case it covers — a
  reassigned or misregistered ID is somebody else's record and the station in
  front of you is not.

  Resolution happens on a background goroutine and never on a request path. The
  fetch is made outside the lock the console reads under, so a slow registry
  cannot stall a page. A registry that is down is logged once as it starts
  rather than once a minute.

- **The registry client and the cache table.** `migrations/0004_callsigns.sql`
  holds resolved names so a restart does not re-ask for everything QSP already
  knew, and the HTTP client identifies itself with QSP's version and the
  operator's contact address.

  **A rate-limit answer is an error, not an absence.** The registry may
  rate-limit at any time, and recording that as "this ID does not exist" would
  cache their refusal as a fact about somebody's own members. An empty result
  is an absence; HTTP 429 is a temporary failure worth retrying later.

  The response is read bounded and only the displayed fields are kept, so the
  cache does not drift towards being a copy of the registry — which is the thing
  their policy puts behind approval. The endpoint is a constant rather than a
  setting: QSP is a client of the amateur DMR registry, not a general client of
  whatever somebody points it at.

- **[ADR-0030](docs/adr/ADR-0030-radio-id-lookup.md) decides how radio IDs are
  resolved**, and `internal/callsigns` implements the part that needs no
  network. A club should see names without maintaining alias lists in every
  radio.

  The design is shaped by the registry's data use policy more than by its
  endpoint. **No bulk download**, because mirroring is behind approval and a
  club needs a few dozen records rather than hundreds of thousands. **The
  operator supplies a contact address or there are no lookups** — automated
  clients are asked to identify themselves, and QSP has no business inventing an
  address for somebody else.

  **Nothing blocks on a lookup.** An ID seen in a transmission is queued and
  resolved later; a network call has no business near a routing decision, and a
  registry that is slow costs nothing but the name. One transmission queues one
  request however many frames it carries.

  Absences are cached too, or every unregistered radio becomes a request on
  every transmission. A *failed* request is not an absence: a registry briefly
  unreachable has said nothing, and remembering that would hide a real name for
  a day.

  Thirteen tests. **The HTTP fetcher and the cache table are not written yet**,
  so nothing is resolved from the registry.

- **A text message is one entry in Last heard, not thirty.** Each data burst
  carries its own stream ID and completed as its own call, so fifty of them
  pushed every voice transmission out of a history that holds fifty — seen on a
  live network while two members exchanged messages.

  Consecutive single-frame bursts from the same radio to the same target within
  five seconds now merge into one entry with a frame count. Merging happens in
  the tracker rather than the console, because the eviction is what does the
  damage: by the time a page renders, the voice is already gone.

  **Only a single-frame burst merges.** A stream with structure is a
  transmission that carried no audio rather than a burst of data, and hiding
  those would lose something worth seeing. Two messages a minute apart stay two
  entries, because they are two things that happened.

- **A configuration history page**, at `/history`, with restore. The admin
  pages can change a running network within a second and there was no undo:
  `configuration_versions` had been recording every save since the first
  migration and nothing read it back.

  **A restore is a save.** It writes a new version whose contents match an older
  one, so the history is never rewritten and a restore can itself be undone —
  which is what [ADR-0027](docs/adr/ADR-0027-configuration-writes.md) decided
  and why those rows are never updated or deleted.

  Restoring shows exactly what it would change before it does it. A restore
  applies within a second, so that is the only chance an operator gets, and
  "restore version 4" means nothing without knowing what version 4 said. The
  newest version offers no button, because restoring what is already running
  would do nothing and a button that does nothing is worse than a sentence.

  `GET /api/config/versions/{number}` returns one version's document with its
  difference from what is running. The list still omits documents: fifty
  versions carrying fifty configurations is a response nobody reads.

- **A bridges and schedule page**, at `/bridges`. The last configuration area
  with no interface at all: bridges, their endpoints, and the windows that turn
  them on for a net.

  **A bridge named by any window is controlled entirely by the schedule**, so it
  is off outside its windows whatever its own setting says. The page says so
  beside that setting, because the alternative is an operator discovering it
  when their net does not open. Renaming a bridge or repointing a window redraws
  those notes, since either changes which bridges the schedule owns.

  A new bridge starts with two endpoints, because validation refuses fewer and
  an operator should not have to learn that by pressing save. A blank peer means
  every peer carrying the talkgroup, which the model expresses by the field
  being absent rather than zero.

  The timezone must be an IANA name, and the page shows one: an abbreviation
  cannot express "20:00 local all year" across a daylight-saving change, which
  is the sort of thing worth saying before somebody types CST.

- **Last heard shows callsigns where QSP knows them.** A hotspot announces its
  own callsign when it registers, and on most hotspots the operator's radio
  carries the same DMR ID — so "3155413" is KB9TYC and QSP can say so without
  anybody's database.

  **Only an exact match counts.** A radio behind a hotspot with a different ID
  stays a number: QSP knows which hotspot carried it and nothing about whose
  radio it is, and labelling somebody else's transmission with the hotspot
  owner's callsign would be worse than the number. Resolving those needs a
  registry, which is a §0 decision rather than a lookup.

  A group call's target is never given a callsign, because a talkgroup number is
  not a radio ID and looking one up finds whichever radio happens to share it.
  The number stays beside the callsign, since the number is what somebody
  programmed and what they will search for.

- **Panels on the admin pages had nothing between them.** `.main` is a grid and
  its gap separates its own children, so a page that wraps its panels — as the
  admin pages do, to hide the whole form until the configuration loads — got the
  gap once around the wrapper and the panels inside it touched. A test fails if
  a page stacks panels in a wrapper with nothing to space them.

- **Health reports blocked sources and parrot activity.** An address being
  refused for repeated failed logins is reported as degraded rather than
  healthy: QSP is working exactly as intended, and it is also the state where a
  member cannot get on the network and nobody has told the operator. It clears
  itself when the lockout lifts, so nobody is left with a permanent warning
  about somebody who fixed their password an hour ago.

- **A network settings page**, at `/network`. The network's name, the address
  members point at, the talkgroups shown on the join page, and parrot — all of
  which were previously a matter of hand-editing JSON or posting it with curl.

  **The talkgroup form separates what is dialled from what arrives**, because a
  hotspot may rewrite a number on its way out and the two are easy to configure
  apart and very hard to diagnose from either end. A morning went into exactly
  that this week.

  A save names the settings that need a restart rather than warning without
  saying what for, which is what parrot needs and what the page says up front.

- **The map draws a minimum grid.** The tile arithmetic has been correct
  throughout and the measurement wrong three times, so the grid now has a floor
  of seven columns by three rows widened around the centre. A map drawn against
  a bad measurement is off-centre rather than a single tile in a corner, and the
  worst case is a few tiles nobody sees rather than a map nobody can use.

- **Text messages were being dropped by contention, and now are not.** A DMR
  text is a sequence of short data bursts, each carrying its own stream ID, and
  the contention key included the stream — so every burst looked like a
  different station keying up. The first reserved the destination and the rest
  were refused until it lapsed two seconds later.

  Seen on a live network as seventeen frames offered and two delivered, which is
  why a message needed endless retries and usually failed. **Contention now
  compares the station rather than the stream** for data: one radio's successive
  bursts are one station, not thirty.

  Voice is untouched. Two people keying up still contend, and a data burst still
  cannot take a destination that is carrying somebody's voice — both have tests,
  because the rule this relaxes is the one that stops audio interleaving into
  something nobody can understand.

- **A text message no longer looks like fifty failed transmissions.** A text is
  a handful of one-frame data bursts, each with its own stream ID, so each
  became its own entry in Last heard — and every one was marked "no terminator",
  which is a false alarm: a single burst has no terminator and is not meant to
  have one. Fifty of them buried the voice traffic the panel exists to show.

  Calls now record whether any voice frame arrived. Data is labelled as data, in
  muted type rather than amber, because it is a label and not a warning — and
  "no terminator" is kept for voice, where it means something.

- **A parrot change now says it needs a restart.** The recorder is built once at
  startup and handed to the listener, so enabling parrot on a running instance
  saved the setting and changed nothing — while the save reported no restart was
  needed. That is exactly the quiet lie `NeedsRestart` exists to prevent, and it
  was found by enabling parrot on a live server and watching nothing happen.

- **Parrot answers a private call**, which is how most networks do it and how
  most operators program their radios — it lets somebody test without the whole
  club hearing them. `Handles` matched only group calls, so a private call to
  the parrot number fell straight through to routing.

  **A private replay is addressed back to the radio that made it.** A radio
  un-mutes a private call only when the target is its own ID, so replaying one
  with the original addressing would produce frames it receives and refuses to
  play — parrot appearing to work and sounding like nothing. A group replay
  keeps its addressing, because the talkgroup is what the radio is listening to.

  A private call is matched on either timeslot: it is addressed to a number
  rather than carried on a talkgroup, so requiring one would refuse the
  commonest way parrot is used. A test asserted the opposite of all this and had
  encoded the assumption that parrot is a group service.

- **Repeated failed logins stop being answered, say why, and are visible.**
  Three faults, one story: forty refusals from one address in six minutes is
  indistinguishable from somebody guessing the peer password, QSP answered every
  one, the log said only "authentication failed", and the operator found out
  because the member messaged them.

  A source address that fails six times in fifteen minutes is ignored for five
  minutes — including the challenge, which is where a guesser would otherwise
  collect a fresh salt on every attempt. Throttling is per address rather than
  per repeater ID, since an ID is whatever the caller claims. A successful login
  clears the history, so a member who fixes their password is not held to the
  attempts before they did, and the lockout lifts on its own.

  Every refusal now names its reason — wrong password, unknown ID, answered from
  a different address, or no challenge outstanding — because those need
  different things done about them. **A run is reported once**, not once per
  attempt: a hotspot retrying every ten seconds produced forty identical lines,
  which is how an operator learns to skim past the one that matters.

  `/api/peers` carries what is being refused and the console shows it, hidden
  when there is nothing to say. It is reported even when no peer is connected,
  which is exactly the case where nothing else on the page explains the silence.

  Sixteen tests.

- **The join page is in the navigation**, and the access page shows the link to
  send members with a button to copy it. The page an operator hands to their
  club existed with no link from anywhere — findable only by somebody who
  already knew the URL, which is not the person who needs it.

  The link is built from the address the browser is already using rather than
  from configuration, because an operator reading the page arrived by the same
  route their members will: through a proxy, on a hostname, or on whatever port
  is actually reachable. A configured value would be wrong for most of those.

  Copying falls back to an instruction when the browser refuses clipboard access
  — which it does on an insecure origin, and a club on a LAN over plain HTTP is
  the ordinary case rather than an error.

- **The map can be dragged, and draws more than one tile.** Three separate
  faults, all present at once.

  Pressing a tile started the browser's own drag-and-drop, which takes the
  pointer and stops the map dead after a few pixels. Tiles also captured the
  pointer from the canvas that handles dragging. And every `pointermove`
  rebuilt the whole tile grid — far more often than the screen refreshes —
  which is what made dragging feel broken rather than merely imperfect. Redraws
  are now coalesced to one per animation frame.

  Tiles are no longer lazily loaded: a tile is wanted the moment it is drawn,
  and deferring it leaves a map filling in as somebody scrolls, or not at all
  for tiles the browser judges far away.

  **The frame is measured by taking the largest answer anything offers** — the
  element, its bounding box, and its ancestors. Three attempts to fix this by
  reasoning about which measurement was correct all failed, and a map that draws
  a few tiles more than it needs is a far smaller fault than one that draws a
  corner. What it measured is recorded on the element as `data-measured`, so the
  inspector answers the question rather than another round of guessing.

- **Map and admin page adjustments.** The map frame is explicitly full width
  and falls back to its container's width when it measures narrower, which is
  what left a full-width panel showing one tile in the corner. The tile
  arithmetic was verified against a known size and is correct — it emits a grid
  that covers the frame and centres a single point — so the element's own sizing
  was the thing to pin down.

  The talkgroup boxes on the access page are capped at a sensible width: a
  column of numbers does not need the width of a monitor, and a box that wide
  invites a paragraph. Panels gained a little breathing room at the bottom.

- **Parrot replays**, and the transport that does it. A recording is sent back
  at DMR timing by a goroutine — **the only part of QSP that writes to the peer
  socket without being the listener**, which is a deliberate exception to
  [ADR-0002](docs/adr/ADR-0002-single-writer-routing-core.md) named rather than
  left to be discovered. It exists because frames must leave 60 milliseconds
  apart and the sweep runs every second, sixteen times too slow for one frame.

  Frames are paced by a ticker rather than by sleeping, so a long recording does
  not drift slower against the radio as the time spent writing accumulates. One
  dropped packet does not end a replay: UDP to a hotspot on a domestic
  connection loses some, and abandoning a recording over one would make parrot
  look broken when it is not. Keying up stops a replay in progress, and
  shutdown stops every one.

  Ten more tests, including that frames leave paced rather than as fast as the
  socket will take them — a replay a radio cannot decode would look like parrot
  working and sound like nothing.

- **Parrot records and replays**, so a member can prove their whole path works
  with nobody else awake. [ADR-0028](docs/adr/ADR-0028-parrot.md). That case is
  the ordinary one rather than the unlucky one: a club has a handful of active
  operators, and somebody keying up into silence cannot tell a broken radio from
  an empty channel.

  **QSP replays bytes it never understood.** A transmission is frames carrying
  DMR bursts and nothing here decodes one, which is why this arrives long before
  the vocoder — parrot is a buffer, not an audio feature.

  A recording goes back to the peer that sent it and to no one else, carrying a
  new stream ID because a repeated one is a duplicate to a radio and is
  discarded as one. Source and target are preserved, so the member's display
  shows what it showed when they transmitted. **A recording never enters the
  routing core**, so it cannot face access control or contention on the way back
  and cannot leak onto a bridged network.

  A transmission ends in silence rather than in a distinguishable frame: the
  header and the terminator share a frame type and only position tells them
  apart, which `internal/calls` discovered first. Keying up again starts over.
  Two hotspots may test at once.

  There is **no default talkgroup** — 9990 is conventional on some networks and
  9998 on others, and shipping one network's number is what §0 refused for
  talkgroup lists and tile servers. Fourteen tests. **The playback transport is
  not written yet**, so nothing replays.

- **Peer coordinates are a pill rather than four decimal places.** Latitude and
  longitude next to a place name made a row that no longer scanned — digits
  nobody reads, in a table meant to be followed across the page. The numbers
  moved to the link's title, where somebody who wants them can still find them.

- **An access control page**, at `/access`. Talkgroups per timeslot,
  registration and subscribers, saved through the write path so a change applies
  within a second and is recorded as a version with the administrator's name on
  it.

  **Each list says in words what it does**, and that is the reason the page
  exists rather than a nicety: `{"mode": "deny", "ids": []}` is correct and
  tells an operator nothing. It now reads "Everything is allowed. Nothing is
  blocked." — and the line updates as the form is edited, so the consequence of
  a change is visible before it is saved rather than after.

  A refused save lists every problem with its suggested fix. A read-only
  instance says so and disables the button rather than letting somebody fill in
  a form that cannot be submitted.

  The page is served to anyone and shows a sign-in prompt when nobody is signed
  in; the endpoints behind it are what require a session.

- **OpenStreetMap refused every tile with a 403.** QSP sends
  `Referrer-Policy: no-referrer` and their tile policy requires a `Referer`
  identifying the site, so the map drew a picture saying access was blocked. The
  tile images now carry `referrerpolicy="origin"` — scheme and host, for those
  requests only, with every other request QSP makes staying anonymous. Relaxing
  the site-wide header would have been the larger change for the smaller reason.

- **Console assets were cached with no way to tell they had changed.** Embedded
  files carry no modification time, so neither `Last-Modified` nor `ETag` was
  sent and browsers cached them heuristically. **An operator who upgraded got
  the new server and the old console**, indefinitely, with no way to know why a
  fix had not arrived. They now carry `Cache-Control: no-cache`, which means
  revalidate rather than do not store.

- **The content security policy blocked every map tile.** `img-src 'self' data:`
  and tiles come from somebody else's server by definition, so the browser
  refused all of them silently and the map drew an empty frame. The policy now
  allows the configured tile origin — scheme and host, derived from
  `server.map.tile_url`, and nothing else. Clearing the tile URL restores the
  original policy exactly.

  This is the failure the map could never have worked through, and no amount of
  drawing arithmetic would have fixed it.

- **The map measured itself once, before it had a size.** A panel not laid out
  when `show()` ran measured zero, fell back to 600×320 inside a frame nearly
  three times wider, and placed every tile and pin relative to a viewport that
  did not exist — which is what put the only pin in the top-left corner. It now
  measures at draw time and redraws when the element's size changes, so a map
  created before layout draws correctly the moment there is one. A resize
  redraws without refitting: somebody who has panned away should not be yanked
  back because the window changed width.

- **`.muted` was used and never defined**, so the coordinate link in the peers
  table fell through to the browser's default blue — underlined and unreadable
  on a dark panel. A new test fails when any class the scripts use has no rule
  in any stylesheet, which is the general form of this: a class name is a string
  in one file and a selector in another, and nothing connected them.

- **A saved join or map setting applied to nothing.** Both are derived from the
  configuration and were captured once when the server was constructed, so a
  save changed the file and the routing table and left the join page serving the
  old network name — while the response reported that no restart was needed.
  Two individually reasonable statements that together were a lie: the operator
  was told the change was live, and it was not.

  Found by changing the network name through the API on a live instance and
  watching the join page keep the old one. The server now picks up the settings
  a running instance can change, through atomics rather than a mutex the
  handlers would take on every request.

- **A live configuration change logged "console" as its author.** The version
  row could attribute it and the log line could not; the author now travels with
  the change.

- **`Writable` called a writable instance read-only.** It checked the
  configuration file's permission bits, which do not gate the operation: saving
  replaces the file by renaming a temporary one over it, and `rename(2)` needs
  write permission on the *directory*. A root-owned `0444` file in a writable
  directory is replaced without complaint.

  Found on a live server, where the check happened to pass and the reasoning
  behind it turned out to be wrong anyway — the comment claimed it caught an
  immutable attribute, which does not appear in the permission bits at all.

- **A flaky test that CI caught and no local run did.**
  `TestUnbridgedTrafficIsRepeatedToOtherPeers` read the forwarded counter the
  instant the receiving peer had the frame. The counter deliberately lags: the
  listener writes to the socket and increments afterwards, so a write that
  failed is not counted — which leaves a window the race detector's slowdown
  widened enough to lose.

  Two tests in that file already polled before asserting and two did not, so the
  loop is now a named helper the next one cannot forget.

- **Login, logout and session endpoints**, with the middleware that will guard
  every write endpoint that follows. `POST /api/login` exchanges credentials for
  a session cookie, `POST /api/logout` ends it, and `GET /api/session` reports
  who the caller is — answering "nobody" with a 200 rather than an error, so a
  console loading normally does not produce one in the browser.

  `Secure` on the cookie follows `server.behind_proxy`. Setting it
  unconditionally would silently break a club on plain HTTP over a LAN: the
  browser drops the cookie and the login appears to succeed and do nothing.

  A wrong password and an unknown username give identical responses, byte for
  byte, and neither sets a cookie. A locked account says so plainly, because an
  operator told only "incorrect" keeps trying and extends a lockout they cannot
  see. Logout answers the same with or without a session, since "you were not
  logged in" tells whoever sent it something about a cookie they may not own.

  **`requireSession` also enforces the origin check**, because the two questions
  are asked of exactly the same requests and separating them is how one gets
  forgotten on a new endpoint. `SameSite=Lax` is honoured by browsers rather
  than guaranteed by them; this is the second lock on the same door. A request
  with no `Origin` passes, since curl and scripts are not what CSRF protects
  against.

  Nineteen tests. `SECURITY.md` gains the three endpoints, which the
  documentation gate requires.

- **`qsp adduser` checks the name before asking for a password.** Found on the
  first real run: it prompted twice and then said the name was taken, which is
  the wrong order to discover that in. `CreateAccount` still refuses a
  duplicate, since two of these running at once would both pass the early check
  and the folded unique index is what actually decides — the new check exists to
  fail early and politely, not to fail correctly.

- **`qsp adduser` creates an administrator**, and per
  [ADR-0026](docs/adr/ADR-0026-authentication.md) it is the only thing that
  does. It reads the same configuration the server does, so it writes to the
  same database, runs the migrations so a fresh install needs no separate step,
  and refuses rather than falling back to somewhere writable — an account in an
  unexpected database is one the server never sees.

  The password is prompted twice and never echoed. **Echo is turned off with
  `stty` rather than a library**, because the standard library exposes no way to
  do it and `golang.org/x/term` would be QSP's second direct dependency; ADR-0004
  and ADR-0017 are careful about that count, and a dependency is a poor trade
  for a program already required to run on a Unix host with a terminal attached.

  `stty` doubles as the terminal check, and is a better one than inspecting the
  mode of standard input: `/dev/null` is a character device too, so a password
  piped from it would have looked like somebody typing.

- **The SQL behind the login flow**, `auth.SQLRepository`. Six statements and
  nothing else: every decision — lockout, timing, expiry, revocation — is in
  `Service` and tested without a database, so the part that cannot be executed
  in a container with no SQL driver is also the part with the least in it.

  **It is checked against the schema rather than run.** A column named
  `last_login` where the migration says `last_login_at` compiles, lints, and
  fails at the first login; so does an `INSERT` whose value list is a different
  length from its column list. Two tests read the statements and the migration
  and compare them, and both were confirmed by introducing exactly those errors.

  Times are stored as RFC 3339 in UTC because the expiry sweep compares text: a
  format where `2026-9-30` sorts after `2026-10-01` would leave expired sessions
  alive and delete live ones, quietly, and only around a month boundary. The
  zero time stores as empty, since never-locked and never-logged-in are the
  common cases and a timestamp in year one would read as a date.

- **The login flow**, in `internal/auth`. Accounts, sessions, lockout and
  revocation, behind a `Repository` interface so the behaviour worth testing is
  testable without a database — the SQL is a thin adapter over six methods and
  none of the interesting parts live in it.

  **An unknown username costs the same as a wrong password.** It is answered
  with the same error, and a decoy hash is verified so it takes the same time;
  without that it returns in a microsecond against twelve milliseconds for a
  real account, which turns the login form into a way of asking which callsigns
  hold accounts here. There is a test that fails if the decoy is removed.

  Lockout counts on the account rather than in memory, and the counter resets
  with the lock so an account is not one mistake from relocking forever. A
  success clears it. Session expiry is checked server-side, because a cookie's
  lifetime is a hint the browser may ignore, and a session seen to be dead is
  deleted rather than left for the sweep.

  Seventeen tests. **The SQL adapter is not written**, and is the part that
  cannot be verified in the development container.

- **`docs/architecture/hbp-protocol.md` records that DMRGateway blanks the
  announced position.** A WPSD hotspot with correct coordinates in
  `/etc/mmdvmhost` sends `0.000000` and `00.000000` to QSP, because DMRGateway
  builds its own `RPTC` per upstream rather than forwarding the one MMDVMHost
  produced. Setting `Enabled=1` in its `[Info]` block, with the coordinates
  already there, changed nothing.

  So **the map will be empty for most WPSD users**, since DMRGateway is the
  common configuration, and there is no QSP-side fix. That is worth knowing
  before anyone relies on the map, and it is recorded rather than left looking
  like something a later version might address.

- **The map says why a peer has no pin.** "No peer has announced a position"
  read the same whether a hotspot sent nothing or sent something QSP refused,
  and those need different things done about them: the first is a hotspot nobody
  configured, the second is one configured wrongly, and only its owner can tell
  which if nothing says.

  Found on real hardware. A WPSD hotspot with DMRGateway's `[Info]` block
  disabled announces latitude `0.000000` and longitude `00.000000` — not blanks,
  zeros — so the Null Island rule refused them correctly and the console then
  reported the wrong reason. `/api/peers` now carries the refusal and the map
  shows it, naming the peer and what it announced.

  The rule itself is vindicated rather than changed: an unconfigured hotspot in
  the field sends exactly 0,0, and without that check the pin would have been in
  the Gulf of Guinea.

- **[ADR-0026](docs/adr/ADR-0026-authentication.md) decides authentication**,
  which is what the admin interface has been waiting on, and
  `migrations/0003_users.sql` adds the accounts and sessions it needs.

  **The first administrator is created from the command line and there is no
  other way.** A setup page open until the first account exists is a race an
  instance loses silently — restarted with an empty database and reachable from
  the internet, it belongs to whoever loads it first, and the result looks
  exactly like a working setup. `qsp adduser` needs shell access on the host,
  which whoever installed QSP has and nobody else should. The web surface
  therefore never has an unauthenticated path that writes anything, at any point
  in the instance's life.

  Sessions are rows rather than signed tokens, so removing an administrator
  takes effect on the next request; a self-contained token cannot be revoked
  without a list of revoked ones, which is the sessions table arriving by a
  worse route. `Secure` on the cookie follows `server.behind_proxy`, because
  setting it unconditionally silently breaks a club on plain HTTP over a LAN and
  omitting it leaks a session on a public instance.

  Failed attempts are counted on the account rather than in memory, since a
  restart would otherwise be a free reset for whoever is guessing, and a wrong
  password and an unknown username answer identically — a login that replies
  faster for a name nobody holds is a list of valid callsigns.

  Deliberately not decided: roles, and password reset by email. **No code yet
  beyond the schema.**

- **The console has a map**, and QSP vendors nothing to draw it.
  [ADR-0025](docs/adr/ADR-0025-no-bundled-map.md). A slippy map is Web Mercator
  arithmetic, a grid of image elements and a drag handler — about a hundred and
  fifty lines. Leaflet is a fine library and most of it is features this does
  not need, and writing the arithmetic keeps the console what it is: markup, one
  stylesheet, scripts, no build step, servable from a LAN.

  `server.map.tile_url` chooses the tile source and defaults to OpenStreetMap,
  because a map needing setup before it shows anything is one most operators
  never see. **Clearing it leaves a working map with no background** — the pins
  still draw, which is the right answer on a network with no route out. An
  instance large enough to matter should point it elsewhere: those servers are
  funded by donations.

  Attribution renders whenever tiles do and cannot be configured away, because
  it is a licence condition of the data rather than a courtesy.

  The map is created on first use, so an instance whose peers never announce a
  position fetches no tiles at all. A peer without usable coordinates is absent
  from it rather than placed somewhere, and the count says how many of the
  connected peers are shown so the gap is visible.

  A test fails if any script appears in `static/` that QSP did not write, since
  vendoring a library is how the no-dependency guarantee ends quietly.

### Changed
- **An opened hint is no longer amber.** `--color-primary` and
  `--color-degraded` are the same hue family, so a paragraph somebody chose to
  read wore the colour this console uses to mean "something needs attention" —
  against a note already in `console.css` saying that spending amber on an
  ordinary condition teaches an operator to ignore amber. Open is a filled
  neutral disc.

- **The hint's `?` is drawn rather than typed.** A stroked path at the brand
  mark's 1.6 weight, so it cannot be substituted by whatever font loads and
  scales with the button. `hints.js` is unchanged: the markup carries the text
  and this file only wires the buttons, which is why a structural fault in four
  pages needed no change to the behaviour.

- **[ADR-0025](docs/adr/ADR-0025-no-bundled-map.md) is amended, and its first
  version was wrong twice.** It argued that a compiled-in tile URL made every
  QSP install share one blast radius; tiles are fetched by the browser carrying
  each instance's own `Referer`, so a tile server sees many small sites rather
  than one application. And it withheld the map on privacy grounds — the same
  positions are published by the networks these operators already use, every
  station chooses what its hotspot announces, and declining to draw data QSP
  already serves as text is paternalism rather than privacy. Both arguments are
  kept in the record rather than deleted.

- **Peers report where they say they are**, which is the map's foundation.
  PROJECT_MEMORY §8 says QSP discards the coordinates hotspots send in `RPTC`.
  It does not: they have been parsed and kept on the peer all along and simply
  never exposed. `/api/peers` now carries them and the console shows a Location
  column.

  **Says, not is.** These are fixed-width free text from a station QSP does not
  control, and nothing verifies them. The place name is shown as announced,
  because "Denton, TX" is useful whether or not a pin can be drawn; coordinates
  appear only when they parse and are plausible, because a pin in the wrong
  place is believed while a missing one prompts somebody to ask.

  0,0 is refused. It is a real coordinate in the Gulf of Guinea and is almost
  never where a hotspot is; it is what an unset field looks like. A zero on one
  axis alone is kept, since the equator and the prime meridian are real places —
  and latitude and longitude are pointers in the JSON so that a station on the
  equator is distinguishable from one that announced nothing.

- **Outbound peer mode is wired into startup.** A homebrew upstream now builds
  a `PeerLink`, registers a health check like any other link, and no longer
  refuses startup. It still must not be pointed at BrandMeister; see
  [ADR-0018](docs/adr/ADR-0018-openbridge.md).

  **Health advice now comes from the link rather than the health check.** What
  to check differs entirely between the two protocols: OpenBridge has no
  keep-alive, so its advice is about addresses and quiet talkgroups, while a
  homebrew link has one, so silence is a fault and the advice is about
  credentials. One hardcoded string was wrong for one of them, and wrong advice
  is worse than none.

- **The outbound link's transport**, `upstream.PeerLink`. It drives the
  handshake over a connected UDP socket — connected rather than merely bound,
  because QSP is dialling out and the kernel can then discard datagrams from
  anywhere else before they reach any of this.

  **It owns the state machine's concurrency**, which is the part worth stating.
  `homebrew.Link` is deliberately single-writer in the style of
  [ADR-0002](docs/adr/ADR-0002-single-writer-routing-core.md), and three things
  want to touch it: the socket reader, the ticker, and whichever goroutine is
  routing a frame outward. Serialising them here is what lets the state machine
  stay a pure function and be tested without any of this.

  `upstream.Connection` lets one `Set` hold both link kinds. OpenBridge and a
  homebrew peer differ entirely in how they reach the far end and not at all in
  what a caller wants from them: a name, a lifecycle, somewhere to put a frame,
  and something honest to say about themselves.

  Unlike OpenBridge, a homebrew link has a keep-alive, so silence is a fault
  rather than the ambiguity between a quiet talkgroup and a dead link that
  OpenBridge leaves. The status says so.

  Nine tests against a fake master over real sockets, race clean.

- **The outbound link state machine**, `internal/protocol/homebrew`. QSP has
  spoken the master side of this handshake since phase 1; this is the same
  conversation from the other end, which is what reaches XLX, DMR+, IPSC2 and
  another QSP.

  It is pure and clock-injected like `peers.Master`, so reconnection, backoff
  and every timeout are testable without a network — and the events that matter
  most are the ones that happen when *nothing* arrives, which a test driven only
  by incoming datagrams never reaches.

  **A link that has never connected and a link that has dropped are different
  things** and read differently, because the first is usually a wrong password
  and the second is usually the network. Backoff doubles and is bounded, and
  resets only on a completed handshake — resetting it earlier would let a link
  that authenticates and then fails at the configuration step retry at the
  minimum delay forever. Frames are dropped rather than queued while
  disconnected: one arriving after the transmission it belonged to had ended is
  worse than one that never arrives.

  Eighteen tests and a fuzz target; 1.3 million executions found nothing. **The
  transport that drives it is not written**, so nothing connects anywhere yet.

- **[ADR-0024](docs/adr/ADR-0024-outbound-peer-mode.md) decides outbound peer
  mode**, the last structural gap in ADR-0019's model, and its configuration is
  accepted now so the admin interface can be built against a schema that will
  not move. **The protocol is not written yet**, and an enabled homebrew link
  refuses startup with a message saying so rather than being handed to the
  OpenBridge builder or silently skipped.

  It is a `protocol` option on the existing `upstreams` block rather than a new
  one, because an upstream is already "a link to another network" and how it is
  carried is not what it is for. An absent protocol means OpenBridge, so every
  existing document keeps meaning what it meant.

  **It must not be pointed at BrandMeister.**
  [ADR-0018](docs/adr/ADR-0018-openbridge.md) records that their operators
  define peer bridging as prohibited and ask that nobody build software without
  an onboard radio that impersonates those protocols. A capability existing is
  not permission to use it where its use has been refused, and this does not
  shorten the wait for a bridge. QSP does not enforce it: detecting one
  network's addresses would mean carrying their hostnames in the codebase, which
  is what §0 refused for talkgroup lists and for the same reasons.

  What it does reach is XLX, DMR+, IPSC2 — and another QSP, which lets two clubs
  link directly without either asking a third party for anything.

- **Peers attach talkgroups, which is layer 3.**
  [ADR-0023](docs/adr/ADR-0023-talkgroup-subscription.md). Repeat sent every
  talkgroup to every peer, so a member sitting on their local talkgroup had a
  statewide net arriving on the same hotspot — and on a simplex hotspot with one
  timeslot they cannot have both anyway.

  **A peer attaches a talkgroup by transmitting on it**, and the attachment
  lapses after fifteen minutes of silence. That is the half that matters: a
  configured-only list would mean asking an administrator to edit a file every
  time somebody wanted to work a talkgroup for ten minutes. Static attachments
  remain for what transmitting cannot serve — a calling channel that must be
  there before anybody speaks.

  The attaching frame is itself delivered. Attaching after routing would clip
  the first syllable of every transmission onto a newly attached talkgroup,
  which is the mistake [ADR-0016](docs/adr/ADR-0016-ptt-triggered-bridging.md)
  records making with PTT triggers.

  **Off by default**, so a club that configures nothing notices no change.
  Bridges ignore attachment, since an operator who bridged a talkgroup to
  somebody has already said it should arrive. Access control runs first, so
  nobody can subscribe their way past a refusal. Twenty tests.

- **Private calls are routed.** Radio-to-radio calling is used constantly on DMR
  and QSP routed none of it: repeat fired only on group calls, and nothing
  handled a call whose target is a radio rather than a talkgroup. A private call
  now resolves the called radio to the one peer it is behind and is delivered
  there and nowhere else — a private conversation on every hotspot would be the
  obvious way to get this wrong.

  The called radio's ID stays in the target field, because that is what opens
  the receiving radio's squelch. Only the timeslot comes from where the radio
  was last heard, since a peer's two slots are independent paths.

  Group and private calls contend identically thanks to
  [ADR-0022](docs/adr/ADR-0022-timeslot-contention.md), with no special case for
  either. A call to a radio not heard recently is refused with a reason naming
  the radio, because silence leaves an operator unable to tell a failure from
  somebody not answering. Eight tests. **Untested on hardware.**

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

- **The traffic hint cried wolf.** A peer connected and sending keepalives with
  no voice frames drew an amber warning saying its transmissions were not
  reaching QSP. But a hotspot sends identical keepalives whether its owner is
  misconfigured or simply not talking, so nothing on this page can tell the two
  apart — and the message picked one and stated it as fact. On a quiet club
  network, and for several minutes after every restart, that is an alarm about a
  fault that does not exist.

  The hint now names both possibilities and asserts neither, and is muted rather
  than amber. Amber is a promise that something needs attention; spending it on
  a condition that is usually fine teaches an operator to ignore amber, which
  costs more than the hint was worth.

- **`tokens.css` promised 4.5:1 and nothing checked it.** The promise had been
  broken twice: once by an opacity applied to muted text, once while widening
  the gap between panels and the page. Both were caught by somebody doing the
  arithmetic by hand at the right moment.

  `console` now measures every text colour against every surface it is drawn
  on, including the composited header band, and fails the build below 4.5:1. It
  found a third violation immediately: lifting the surface had taken
  `--color-unavailable` from 4.70:1 to 4.40:1, so that colour is lightened. It
  also asserts that panels carry a shadow and that no component stylesheet
  contains a raw hex, since a colour outside `tokens.css` is one nothing can
  audit.

- **The routing health check called a working master degraded.** It reported
  degraded whenever no bridges were configured, which was right while bridging
  was the whole routing model and wrong from the moment the master learned to
  repeat. A club whose members all sit on one talkgroup configures no bridges
  and is working exactly as intended; telling their operator the instance is
  degraded sends them hunting a fault that is not there.

  The summaries now lead with what routing mostly does — peers on a talkgroup
  hearing each other — and mention bridges as the additional thing. Switching
  forwarding off says what is lost rather than only that a flag is unset.

- **Two talkgroups could be delivered to one peer's timeslot at the same
  moment.** A DMR timeslot is one TDMA channel and carries one call; sending two
  down it is interleaved audio nobody can understand — exactly what
  [ADR-0014](docs/adr/ADR-0014-contention.md) exists to prevent, missed because
  reservations were keyed on `(peer, talkgroup, timeslot)` and the two
  talkgroups therefore looked like separate destinations.

  A peer destination is now reserved by `(peer, timeslot)`.
  [ADR-0022](docs/adr/ADR-0022-timeslot-contention.md) records it.

  **Repeat made this ordinary rather than exotic.** Before ADR-0019 a peer
  received only the talkgroups a bridge named; a master that repeats sends every
  talkgroup any peer transmits on to every other peer, so several converging on
  one slot is now the normal shape of a busy club network.

  **A link keeps the old key**, deliberately. Contention models a physical
  constraint and an OpenBridge link is an IP socket rather than a radio channel;
  BrandMeister carries several talkgroups over one, and applying the timeslot
  rule there would refuse deliverable traffic.

  Some traffic that used to be delivered is now refused, which is the point — it
  was going to a slot that could not carry it. Refusals name the talkgroup
  already on the slot, since "busy" alone tells an operator nothing. Six tests,
  and every routing test that predates this passes unchanged.

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
