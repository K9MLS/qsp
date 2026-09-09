# Handover, 2026-09-09 afternoon

Read `NEW-SESSION.md`, then **§8a** of `PROJECT_MEMORY.md`, then **ADR-0052**,
the frame everything about linking sits inside, then **ADR-0053** (three names
for a server). **ADR-0055** is the administration page, now built. **§8o** is
the last session.

**The history was rewritten on 2026-09-09** to remove a product name from every
commit, message and path, and force-pushed. **Every commit hash in this document
and in the changelog predates that and no longer resolves** — they are a record
of what happened, not something to look up. `origin/main` is `ad1acc8`. Both
servers and this repository are on the rewritten history; nothing else has a
copy.

Version **0.1.161**, patches 0261–0319. **Both servers should be on 0.1.156** —
check, because four deploys on 2026-09-09 did not take, every one a pasted block
eaten by the `sudo` password prompt. Everything from 0315 is documentation and
tests, so the servers being four versions behind the tree is expected rather
than a fault. `origin/main` is current.

Check a deploy by asking the running process: the `starting` log line on
production, a string unique to the build in the container. Run `cat VERSION`
after every `git am`. And **`sudo -v` before any block containing `sudo`** — the
password prompt eats the next pasted line, which happened four times today and
was caught by the version check every time.

## What 2026-09-09 did

**The administration page and backup exist; the vocoder does not and never
will.**

- **0305–0306** — the administration page (ADR-0055) at `/server`: identity,
  agreement, services, callsign lookup. The restart control is always there,
  not only when something waits.
- **0307** — backup and restore (ADR-0054). An export carries no secret and
  lists the credentials it cannot carry.
- **0308** — a mechanism for retiring a configuration field, and `Export` and
  `Import` retired. **Proved live**: the test server started on 0.1.150 holding
  `export`, and the field left the file on the next save.
- **0310–0311** — the vocoder pool removed. QSP does not decode audio and will
  not; crossing codecs needs a hardware AMBE dongle behind a transcoder, which
  belongs to the connectors that need it.
- **0312–0314** — the version at the foot of every sidebar, and the repair of
  the console chrome it broke on the way.

**Half the session was spent fixing my own mistakes**, and the pattern is worth
naming: three separate defects, all from editing one place while the truth lived
in two. The version went to the endpoint nothing reads; the sidebar had its own
hand-written list of unbuilt subsystems; and four consecutive string edits to
one script deleted a function while leaving its call site.

Each is now gated: `/api/session` is asserted to carry the version, the
console's markup is cross-checked against `unbuiltSubsystems`, and a script must
define every function it calls. **The last of those is the first check of any
kind on ~1,500 lines of JavaScript** — the whole gate chain is Go and read none
of it.

## Zello, researched and decided

**QSP will never contain a vocoder.** It copies AMBE payloads and never inspects
them, which is why DMR-to-DMR needs no codec and why the project has no patent
question. Zello carries Opus; crossing to it means decoding to PCM and back.

**Corrected 2026-09-09: QSP speaks AMBE_AUDIO, not USRP.** An earlier note here
said USRP, and it was wrong in a way worth understanding rather than just
deleting: **USRP carries 8 kHz PCM**, so anything speaking it has already
decoded the audio — the vocoder the paragraph above rules out. It recommended a
protocol that requires the thing it had just refused.

Analog_Bridge has two sides. `[AMBE_AUDIO]` carries TLV frames to and from an
`xx_Bridge`, where xx is MMDVM, Quantar, HB or IPSC. `[USRP]` carries PCM to
AllStar or to another Analog_Bridge. **QSP's side is AMBE_AUDIO; USRP is the far
side and QSP never touches it.**

**And nothing needs building to try it.** One of those bridges is HB —
homebrew — and QSP is a homebrew master, so MMDVM_Bridge registers with it
exactly as a hotspot does:

```
QSP ──homebrew──▶ MMDVM_Bridge ──TLV──▶ Analog_Bridge ──USRP──▶ asl-zello-bridge ──▶ Zello
                                        [DVstick 30]
```

Prove that before writing anything. Writing an AMBE_AUDIO connector first would
be building the second version of something that has never worked once. The
dongle is on order and the work is waiting for it; **the dongle is needed either
way**, since it is the only part of the chain that cannot be replaced by
software this project can legally ship.

What QSP needs meanwhile is two console entries and no code: a DMR ID for
MMDVM_Bridge in the registration list, and a password for it.

P25 Phase 1 uses IMBE and needs DVSI's own far more expensive unit; deferred,
and it costs nothing, because P25-to-P25 relaying needs no transcoding at all.

## What the morning of 2026-09-09 did

**The administration page exists, and a server can be backed up.**

- **0305** — the administration page, ADR-0055 built. `/server`: identity,
  agreement, services, callsign lookup.
- **0306** — the restart control is always on that page, not only when
  something waits. A rule from the Links page had been applied where it did not
  hold, overriding what the operator had asked for.
- **0307** — backup and restore, ADR-0054 built. An export carries no secret and
  lists the credentials it cannot carry; an import is asked twice and asks
  whether this server replaces the one that made the backup.
- **0308** — `Export` and `Import` retired, and the mechanism that makes
  retiring a configuration field possible at all. **Proved on a live server**:
  the test server started on 0.1.150 with `export` still in its file, and the
  field left the file on the next save.

**The most valuable half hour was spent not building something.** Removing two
dead fields would have made every configuration already written unparseable,
because `Load` refuses unknown fields and both were written by every save. The
failure would have arrived at a restart. Checking that first turned a small
deletion into a mechanism the project needed anyway.

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

**Look at the console before anything else.** Three defects today shipped
through a clean gate chain and a correct deploy, and all three were found by an
operator looking at a page. Sign in, walk every page, and treat anything that
reads wrong as real.

**Then Pete's server.** Nothing blocks it. He owes two answers: whether he can
get UDP 62031 reachable without CGNAT, and which DMR ID this server should
present to his. **He listens and this server dials**, which makes him the
offering side — the accept form runs from the direction it never has, across the
internet rather than one LAN, so relaying, deduplication and a real network path
are tested together.

The install path agreed with him: build the image on Fedora, `docker save`, copy,
`docker load`. He compiles nothing and runs the bytes that were tested, and gets
the source as well — GPLv3, and his eyes on it are wanted.

**Then Zello, when the dongle arrives.** Prove the chain through MMDVM_Bridge
first, which needs no QSP code. The connector QSP would eventually write is
**AMBE_AUDIO**, not USRP — see the correction above — and it wants an ADR before
code. One Zello channel to one talkgroup is a connector; several is a routing
question, and the bridge machinery may already be the right shape.

**Deferred by decision, not forgotten:**

- **Private calls across a link.** They resolve through the subscriber table,
  links are never targets for one, and `DeliverFromUpstream` records nothing —
  so production can call a radio behind the test server and not the reverse. It
  needs an ADR: a private call sent to a link may chase a radio that has moved.
- **A radio's callsign does not cross a link.** The server that hears a radio
  knows its callsign because the station said so at login, and discards it at
  the link, leaving the far end to guess from a database.
- **Defaulting the callsign lookup on.** `config.Validate` refuses the lookup
  enabled with no contact address, so a default of on makes a fresh install
  refuse to start. Doing it means relaxing that rule so the state loads and is
  reported instead. The operator decided the toggle on the page is enough.

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

- **A configuration field retired on a live server.** The test server started on
  0.1.150 holding `export`, which the build that removed it no longer knows, and
  the field left the file on the next save.
- **A server backed up.** An export names the credentials it cannot carry.

- **A configuration field retired on a live server**, and gone from the file on
  the next save.
- **The administration page**, in a browser, on production.

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
- **A restore.** The import path is built and no configuration has been
  restored from an export, on any machine. **A backup has not been downloaded
  either** — the button exists and nobody has pressed it.
- **The console's JavaScript, beyond one crude check.** ~1,500 lines, and the
  only thing verifying any of it is that a script defines the functions it
  calls. Three chrome defects today; the gate chain is Go and reads none of it.
- **A restart from the console.** The button is on two pages and has never been
  pressed.
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
