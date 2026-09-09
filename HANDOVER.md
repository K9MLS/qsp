# Handover, 2026-09-09 small hours

Read `NEW-SESSION.md`, then **§8a** of `PROJECT_MEMORY.md`, then **ADR-0052**,
the frame everything about linking sits inside, then **ADR-0053** (three names
for a server) and **ADR-0055** (the administration page), which are decided and
unbuilt. **§8o** is the last session.

Version **0.1.145**, patches 0261–0303. **Production and the test server are
both on 0.1.144**, which is everything except 0303, and 0303 is documentation.
`origin/main` is `afd6233` — pushed, verified, and the first copy of this work
off the two machines.

Check a deploy by asking the running process: the `starting` log line on
production, a string unique to the build in the container. And run `cat VERSION`
after every `git am` — a patch file that never reached Fedora passed every gate
and shipped the previous build, and the version was what caught it.

## What the night of 2026-09-08 did

**A link can now be agreed, refused, readdressed and restarted entirely from a
console, and it has been done on air.** That was the whole point of 0287–0302.

- **0287–0290** — the accept form: `QSP-PEER-2.` invitations, the offering side,
  the accept path, the console.
- **0291** — Stop accepting, on inbound links. 0284 had removed the button
  because there was nothing on this side to delete, which stopped being true
  when 0288 began allocating.
- **0292, 0299** — the same wrong port, twice. The first fix went on the
  fallback nothing calls.
- **0294** — both ends of a link say the same kind of thing: per-peer counters,
  a Last-heard that means traffic on both sides, the far end's name as the
  heading, and an editable address.
- **0297** — a server says what it is in *both* directions. A new packet,
  `QSPI`, sent only to a peer that announced itself a QSP link.
- **0298** — ADR-0053 built: a server generates its own identifier.
- **0300** — a restart button, beside the message that asks for one.
- **0301** — **a link whose name was not lowercase could not be sent to.** Audio
  crossed one way and the link reported itself healthy. Found by keying a
  repeater into it.
- **0302** — the accept form suggests a safe name rather than letting one be
  invented.

**Three ADRs, decided and unbuilt**: 0053 (three names for a server, built),
0054 (backup restores a server, not its secrets), 0055 (the administration
page).

**Nine defects were found by running the system and none by reading it.** That
is not a slogan this week, it is the record: the wrong port twice, the
case-sensitive link name, the missing button on inbound links, a nil access
block that would have panicked every server with no access list, the offer form
reading backwards, and six tests that could not fail.

## What the second half of 2026-09-08 did

**The accept form is built, and a link can be agreed entirely from a console.**
Six patches, 0287–0292. Until this, the only way to write a working QSP link was
to hand-edit JSON on a server.

- **0287** — `QSP-PEER-2.` invitations. One token, one direction. A QSP link is
  a peer registration, so the *listening* side offers: it is the side with
  something to allocate. No `Export`, `Import`, `NetworkID`, timeslot or
  `Reply`, and one leg cannot loop, so the reciprocal, the held-passphrase store
  and both defects they produced are gone from this path.
- **0288** — the offering side. It writes a DMR ID into the registration list
  and a password against that ID, both or neither, and settles
  `dmr.peer_passwords` so a link carries its own credential.
- **0289** — the accept path. One upstream, no bridge, no timeslot.
- **0290** — the console. The offer form asks what is at the other end before
  it asks anything else; the accept form reads the kind off the token.
- **0291** — **Stop accepting** on inbound links. 0284 removed the button
  reasoning there was nothing on this side to delete, which stopped being true
  when 0288 began allocating. A server that cannot refuse a neighbour from its
  own console is not sovereign.
- **0292** — the offer proposed the OpenBridge port for a link. Found by using
  the form on two live servers.

**Two defects tonight came from a design that was right when it was written and
was not revisited when its premise changed** — 0284's missing button, and the
address helper reused on a path it did not suit. That is §8a's shape, and it now
has a second form: not only two statements individually true, but one statement
that was true and stopped being so.

## What the first half did

**Two QSP servers hear each other, both directions, and the timeslot survives.**
A hotspot user in Denton heard on a Motorola repeater through a link that is a
peer rather than a bridge, 16:16 UTC:

```
peer connected  peer_id=3132912 callsign=K9MLS from=192.168.1.27:58483
call started    peer_id=3132910 talkgroup=2 timeslot=2
call ended      frames=34 converted=32 delivered=32 duration=1.982s
```

`timeslot=2` is the result. Every previous run read TS1, because OpenBridge
forces it and two instances of the same software were using OpenBridge to reach
each other, so the repeater had been keying on the slot nobody monitors.

The dialling side's whole configuration is one upstream block — name, address,
DMR ID, password file, callsign. `bridges=0`. No listen address, no export
list, no import list, no timeslot, no port forward.

**ADR-0052 is the frame that should have come first.** QSP is a federation of
sovereign servers. Three decisions taken the same day made the sender
responsible for what the receiver gets, and each was found by an operator
clicking through a console rather than by a test.

## Start here

**Build the administration page, ADR-0055.** Decided last night, argued through
with the operator, nothing written. Five blocks — this server, agreement,
services, backup and restore, the callsign lookup toggle — and the rule that
keeps it from becoming a settings dump is in the record in both directions.

The block that earns the page is **agreement**: does the running server match
its configuration, and what differs. QSP already computes that and reports it
only as a sentence beside whichever link was last saved. It cost three round
trips in one evening and each time it was findable only by knowing to look.

**Before writing any of it**, read ADR-0055's design section. The
`ui-ux-pro-max` design system was consulted and its landing-page pattern
rejected — oversized type and `clamp(3rem, 10vw, 12rem)` headlines describe a
marketing page for an operations product. What it does bind is listed there and
the build is checkable against it.

**Then the callsign lookup default.** It is off unless configured, which is why
the test server showed radio IDs where production showed callsigns. Defaulting
`enabled` to true alone would create a setting that says on and does nothing,
because the lookup needs a contact address to identify itself to RadioID —
§7 already forbids a field that means "not applicable" and looks like "not set".
Three parts: default it on, let the bootstrap take a contact from the
environment or the operator identity, and make the gap loud where it exists
rather than silently showing numbers.

**Then remove `Export`, `Import` and the bridge-for-links machinery**, and
narrow OpenBridge to foreign networks. Dead weight in exactly the paths a second
operator will exercise.

**Then backup and restore, ADR-0054.** It has no home until the administration
page exists, which is part of why that comes first.

## Two debts taken deliberately

**The mechanism for this debt is now built and unproved.** 0288 makes an offered
link carry its own credential, so redoing the link through the form pays it. The
paragraph below describes the hand-written link that is still running.

**The link authenticates with the shared hotspot password.** Production has no
per-peer list, so `/var/lib/qsp/peer.pass` is what all three hotspots and now
the link use. ADR-0035 exists precisely so a member can be removed without
changing everybody's password, and this gives that up. Taken to get the on-air
proof and should be paid back when the accept form is built. **The password was
also pasted into a chat**, so it is worth changing when the hotspots can be
reconfigured.

**The test server's peer password was also pasted into a chat**, as production's
was. Both are worth changing when the hotspots can be reconfigured, and the
per-peer mechanism means a link no longer needs either.

**Both servers announce IPSC master ID 3132911.** They do not collide today
because no repeater talks to both. 0277 refuses a *link* carrying that ID, which
is the near neighbour that would have bitten.

## Writing a qsp link by hand

Only the dialling side is configured — the side with no forwarded port. The
listening side needs nothing but the password in place for that ID; the link
arrives as a peer registration on 62031, the port the hotspots already use.

```json
{
  "name": "production",
  "protocol": "qsp",
  "enabled": true,
  "address": "192.168.1.247:62031",
  "repeater_id": 3132912,
  "password_file": "/var/lib/qsp/production.pass",
  "identity": { "callsign": "K9MLS" }
}
```

No `listen_address`, no `network_id`, no `export`, no `import`, and **no
bridge**. `repeater_id` must differ from the far end's own ID and from every
peer it already has: 3132910 is an operator ID and 3132911 is both IPSC
masters. Edit by content, never by line number, and run `sudo qsp -config ...
-check` before restarting.

## Deploying, which is three machines and three mechanisms

This cost most of an afternoon because the documentation described one deploy
and there are three. Commands for the wrong machine were sent three times.

- **FEDORA**, `~/Documents/QSP/qsp`. Source of truth. The only place `git am`
  runs. Patches arrive in `~/Documents/QSP`, not `~/Downloads`. **Fedora runs no
  sshd**, so nothing pulls from it — it pushes.
- **QSP-SERVER**, 192.168.1.247. systemd. `scp` the cross-compiled binary,
  `sudo install -m755`, `systemctl restart`.
- **TEST SERVER**, 192.168.1.27. **Docker Compose, not systemd.** There is no
  `qsp.service` there. Its `origin` is GitHub over HTTPS and cannot
  authenticate, so a bundle goes over `scp` and is fetched from the file. Then
  `docker compose -f docker-compose.yml -f docker-compose.build.yml build` and
  the same override on `up -d` — without it, compose tries to pull
  `ghcr.io/k9mls/qsp:<version>`, gets `denied` because that tag has never been
  published, and silently leaves the old container running.

### Checking a deploy, which took four wrong answers to get right

**`qsp --version` cannot tell you what is running on either server.** On
production it runs the binary on disk, which `install` has already replaced, so
it answers the same before and after a restart. In the container it reads
`development`, because the Dockerfile hardcodes it.

**On production, the check is the `starting` log line.** `cmd/qsp/main.go`
emits it at startup with the same string `--version` prints, and unlike
`--version` it was emitted by the process that is running rather than by a fresh
execution of a file. Take the newest, not the oldest — `grep -m3` stops at the
first three matches and `journalctl` prints oldest first, which answered with
last night's version:

```
sudo journalctl -u qsp -n 5000 --no-pager | grep -i starting | tail -3
```

**In the container, the check is a string only the new build has.** Pick one the
patch introduced; the first attempt matched text that had existed for weeks and
reported success for a container without the patch. `git log -S` on the
candidate string says whether it is unique to the commit.

```
docker cp qsp:/qsp /tmp/qsp-container
grep -ac "<a string only this build has>" /tmp/qsp-container
```

**Do not use an md5 of `/proc/PID/exe` against the file.** It is withdrawn; see
the loose threads below.

## Resolved: the repeater that transmitted silence

**Answered by measurement, above.** It was the timeslot, not the burst shape.
ADR-0041's caveat, corrected in 0268, named the reference that settled it. The
fix is ADR-0051 rather than anything in the encoder.

## Two things that cost the morning, both now understood

**The slot bit.** IPSC audio arrived reporting `timeslot=1` against a bridge on
timeslot 2 and every destination refused it. The XPR8300's codeplug has TG2 on
**TS2**, so QSP was misreading the bit: `ipsc.slot_bit_is_timeslot2` must be
**true** for that repeater. Set it from the Network page (0265), not the file.

**NAT hairpin on the link's far end.** Production's link targeted
`192.168.1.27:62045` and the test server's targeted `qsp.hopto.me:62045`. One
direction crossed the LAN and worked; the other left the LAN for the router's
public address and never came back, so frames were sent and never arrived, with
no rejection anywhere because nothing received them. §7 already recorded this
hazard for the Pi-Star, which points at `192.168.1.247` directly for the same
reason. **Two QSP instances on one LAN must address each other by LAN address.**

**And a console gap it exposed:** a link's far-end address cannot be edited
anywhere in the console. You can create a link and remove one; changing one
character means hand-editing `qsp.json`. That is the same shape as the IPSC gap
0265 closed, and it is worth closing the same way.

## What is proven on a running system

- A QSP server logs into another QSP server as a peer, and audio crosses both
  ways with the talkgroup and timeslot intact — with OpenBridge disabled on
  both sides, so the peer link is carrying it alone.
- The link reconnects on its own: twelve seconds after production restarted,
  with KB9TYC, AD0MI and the Pi-Star all back.
- A frame from a link reaches a Motorola repeater (0271), and the repeater's
  audio reaches the network.
- A clean stop no longer exits 1: `Deactivated successfully`, no
  `shutdown was not clean`.
- Both servers report release and commit. The container reads
  `0.1.125 (v0.1.94-...-398c7d91dffb)` with no `+dirty`.
- A disabled link reads Disabled on both, and a `qsp` link announces its DMR ID.
- Production lists the link that dialled in, with the name the test server gave
  it, and the three hotspots stay peers.
- `config.Validate` refuses an OpenBridge endpoint on any slot but 1 — which
  caught the shipped `deploy/pair` examples on its first run.
- **Two of 0284's five, read off the Links page on both servers.** Production's
  inbound row prints a dash for Sent, Received and Rejected while the test
  server's outbound row prints `40 / 82 / 0`, so not-measured no longer reads as
  zero; and Remove is absent on production, where the link is in nobody's
  configuration, and present on the test server, where it is a config block.

- **Two of 0284's five, and 0291's control.** Production's inbound row prints a
  dash rather than `0 / 0`, has no Remove, and now offers **Stop accepting**.
- **A link written entirely from a console.** The accept form produced a `qsp`
  upstream on the test server with no bridge and no timeslot — over an
  unreachable address, which 0292 fixed and which is why this is listed here
  rather than under what works.

- **A link agreed entirely from a console, carrying audio both ways.** Offered
  on one server, accepted on the other, restarted, and keyed up on TG2 TS2 in
  both directions. The first one written by nobody's hand.
- **A server generating its own identifier** — production `a2740426`, the test
  server `c4fadeb9`, different, and surviving a restart without regenerating.
- **Production restarting from a clean exit**, after a drop-in changed
  `Restart=on-failure` to `always`. Proved with SIGTERM before the button that
  depends on it was built.
- **The repository pushed to GitHub**, `15cfb37..afd6233`, verified with
  `git rev-list --count origin/main..main` reading 0.

## Not proven

- **Relaying and deduplication, on any machine.** Every test is a unit test and
  a third server has never existed. This is the largest outstanding claim.
- A `qsp` link across the internet rather than a LAN. Both ends are on
  192.168.1.x.
- The other three of 0284: the software string, the absent colour code, and the
  position advice. All three are peer properties and do not appear on the Links
  page; production's Peers page or `/api/peers` shows them, for the row
  announcing 3132912.
- **A refused inbound link.** 0291's Stop accepting has been seen on the page
  and never clicked. The link it would be clicked on authenticates with the
  shared password, so it will report that the ID is refused and that it had no
  password of its own — which is the honest answer and worth seeing once.
- **A per-peer link credential.** `dmr.peer_passwords` is settled by the offer
  form and no link has used one yet.
- **The identity packet against an older QSP.** `QSPI` is meant to be reported
  as an unparseable datagram and ignored, keeping the link. Both servers now
  understand it, so nothing has ever met one that does not.
- **A restart from the console.** The button exists on both machines and has
  never been pressed.
- **A callsign crossing a link.** It does not: see the loose threads.
- The IPSC panel from 0265, never loaded in a browser.
- Published image tag and CI publishing. `docker compose up` without the build
  override tries `ghcr.io/k9mls/qsp:<version>` and is denied.

## Loose threads, recorded rather than guessed at

- **Production logs `colour_code=11` and the test server `colour_code=4`.**
  Different repeaters would explain it; nobody has checked.
- Two bytes of the IPSC voice burst are unexplained: byte 5 reads 1–3 in
  `testdata/ipsc/ipsc-master-voice.pcap` and a constant 4 from QSP, and the last
  byte of the 54-byte header is `0x3b` there and `0x00` from QSP. Neither is
  named in `voice.go`.
- **An md5 of `/proc/PID/exe` disagreed with an md5 of the file it links to.**
  Same inode, same size, same mtime, `cmp` says identical, and `grep` finds the
  new build's string in both — yet the pair printed two different digests, the
  same wrong one twice, across two PIDs, each time in the command immediately
  after a start or restart. Minutes later the same command on the same PID
  agreed. No mechanism is offered; a reproducibly wrong answer is worse than a
  flaky one, because it is acted on. The capture, if it is ever worth taking,
  changes one thing and diffs:

  ```
  sudo systemctl restart qsp; P=$(systemctl show -p MainPID --value qsp); sudo md5sum /proc/$P/exe /usr/local/bin/qsp; sleep 5; sudo md5sum /proc/$P/exe /usr/local/bin/qsp
  ```

- **Fedora has its own `/usr/local/bin/qsp`**, md5 `522578ec…`, which is neither
  any current build nor anything on production. Nothing runs it. It sits at the
  path the deploy documentation names, so a command meant for a server that
  lands on Fedora is answered by a binary of unknown age. Worth deleting.
- **A radio's callsign does not cross a link.** Production knows 3132910 is
  K9MLS because the Pi-Star said so at login — a station stating its own
  callsign, the strongest claim there is. The test server sees a frame with a
  radio ID and `peer_id=0`, and falls back to the RadioID database. So the
  better source is discarded at the link and the worse one is hoped for at the
  far end. `QSPI` is where it would travel, and it already carries unverified
  claims and ignores fields an older build does not know. Not done, because a
  callsign crossing a link is another thing a neighbour asserts and can get
  wrong, and whether a stale claim beats a fresh database is a real question.
- **A disabled bridge sits in production's configuration** from the OpenBridge
  era — `bridges:1, enabled_bridges:0` at every startup. Harmless, and it
  belongs with the `Export`/`Import` removal.
- **A link cannot be renamed from the console**, only readdressed. 0302 makes a
  bad name unlikely rather than repairable; that was the deliberate trade.
- **`defaultLinkAddress` is still the OpenBridge helper**, now beside
  `defaultQSPLinkAddress`. Two functions naming the same idea differently is the
  shape that produced the wrong port; when OpenBridge is narrowed to foreign
  networks, one of them should go.
- **The Links page heading is the wrong name.** Production heads the link
  `production`, which is the local label the *test server* chose for its own
  config block — so an administrator on .247 reads a link to the test server
  under their own server's name. The far end's announced display name,
  `QSP Test Server`, is already in the Network column. See ADR-0052 rule 2,
  amended.
- **`LAST HEARD` measures two different things.** Production reads `0s ago` and
  the test server `44s ago` for the same link at the same moment: one is timing
  keepalives, the other traffic, and the test server's own caption says *last
  traffic*. Both true, and the pair reads as one end having gone deaf.
