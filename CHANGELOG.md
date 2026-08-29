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

### Fixed
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

### Fixed
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
