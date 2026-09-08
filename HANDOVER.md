# Handover, 2026-09-08 night

Read `NEW-SESSION.md`, then **§8a** of `PROJECT_MEMORY.md`, then **ADR-0052**,
which is the frame everything about linking now sits inside, then **ADR-0051**,
which is confirmed on air. **§8o** is this session.

Version **0.1.128**, patches 0261–0286. **Everything through 0285 is deployed to
production and the test server**, and both were confirmed by the running process
rather than by a file on disk — production's journal reads
`0.1.127 (v0.1.94-0.20260908181510-465377ac131a)`, and the container's binary
carries a string that no build before 0284 had. 0286 is documentation and needs
no deploy.

## What this session did

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

**A third server.** Relaying and deduplication are the largest untested claim in
the tree: every test is a unit test, and with two servers there is nothing to
relay to, so nothing on air says anything about either.

The wiring was read before the plan was made, so this is a test of something
rather than of nothing. `QSPLinks` is populated in `cmd/qsp/app.go` and read in
`internal/routing/core.go`; the relay branch lets a frame from one QSP link
reach the others; deduplication sits inside `route` itself rather than on an
ingress path, so all three ingress paths reach it — the shape that failed for
`sendToIPSC` is right here; and nothing rewrites `StreamID` or `SourceID` on
relay, so the key survives a hop.

**The topology has to be right or the run is green for the wrong reason.**
`qspLinks` comes from configuration, so only *outbound* links count, and
production has none — it is dialled into by everybody. So: production listens,
the test server dials production, and **the third dials both**. Key the Pi-Star
on production; the third server gets a direct copy and a relayed one, and it is
the third server's journal that must say `already being carried from ...`. A
third that dials production alone relays nothing.

**Run the third as a plain binary on Fedora**, not as a second container. The
container is `network_mode: host` deliberately, so a second one on the test
server collides on every listen address and needs its own volume, name, `.env`
and a config it cannot write before first run. Fedora already has the
cross-compiled binary after every build, is on the LAN at 192.168.1.77 so the
LAN-address rule holds, and dials out only — Fedora running no sshd does not
matter. `deploy/pair/alpha.json` is the template: strip the bridge and the
OpenBridge upstream, two `qsp` upstreams, SQLite under `/tmp`, spare ports.

**And it will fail silently if the ID is not allowed at both ends.**
`repeater_id` 3132913 — 3132910 is the operator ID, 3132911 is both IPSC
masters, 3132912 is the test server's link — and that ID must be in the
registration access list on production *and* on the test server, or it retries
forever with nothing in the log saying why.

**First, though, the three of 0284 that the Links page cannot show.** Production's
Peers page, the row announcing 3132912: a software string that fits 40 bytes, no
colour code where none was announced, and position advice that names the station
rather than a hotspot it does not have. Five minutes, and they are the reason
0284 exists.

**Then settle what a server's identifier is**, before writing any more of
ADR-0052. It is the one choice that cannot be changed once servers are running,
because it is what everybody calls everybody else. Today it is a DMR ID because
that is what the protocol carries — and a DMR ID is issued to an *operator*,
while a server outlives the person who registered it and a club running three
instances needs three from a pool sized for members. BrandMeister numbered
networks separately for that reason.

**Then the accept form.** It still writes an OpenBridge link with a bridge and a
timeslot box, so every peering agreed through the console produces the
configuration ADR-0051 exists to prevent. It is the largest single piece left:
the invitation format carries `Export`, `Import` and `NetworkID`, all
meaningless now, so the invitation, the offering side, the reciprocal builder
and the console JavaScript change together. **Start it with a full context
budget.** A `qsp` link is written by hand meanwhile; the JSON is below.

Then, in ADR-0052's order: a hop count bounding relay before dedup catches it;
the link's own state while retrying, which works and is silent to the console;
subscription instead of flooding, its own record; and the network view
propagated hop by hop, its own record.

Last, remove `Export`, `Import` and the bridge-for-links machinery, and narrow
OpenBridge to foreign networks.

## Two debts taken deliberately

**The link authenticates with the shared hotspot password.** Production has no
per-peer list, so `/var/lib/qsp/peer.pass` is what all three hotspots and now
the link use. ADR-0035 exists precisely so a member can be removed without
changing everybody's password, and this gives that up. Taken to get the on-air
proof and should be paid back when the accept form is built. **The password was
also pasted into a chat**, so it is worth changing when the hotspots can be
reconfigured.

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

## Not proven

- **Relaying and deduplication, on any machine.** Every test is a unit test and
  a third server has never existed. This is the largest outstanding claim.
- A `qsp` link across the internet rather than a LAN. Both ends are on
  192.168.1.x.
- The other three of 0284: the software string, the absent colour code, and the
  position advice. All three are peer properties and do not appear on the Links
  page; production's Peers page or `/api/peers` shows them, for the row
  announcing 3132912.
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
