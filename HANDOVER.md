# Handover, 2026-09-08 night

Read `NEW-SESSION.md`, then **§8a** of `PROJECT_MEMORY.md`, then **ADR-0052**,
which is the frame everything about linking now sits inside, then **ADR-0051**,
which is confirmed on air. **§8o** is this session.

Version **0.1.126**, patches 0261–0284. **Everything through 0283 is deployed
to production and the test server; 0284 is on Fedora only** and needs both
machines — production for the Links page, the test server for a software string
that fits its field.

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
relay to, so nothing on air says anything about either. A second container on
the test server on different ports is the cheapest third instance. Link it so a
frame reaches one server by two paths, key the Pi-Star, and confirm a radio
hears **one** copy and the journal shows the second path refused with
`already being carried from ...`.

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

**`qsp --version` cannot tell you what is running on either server.** On
production it runs the binary on disk, which `install` has already replaced, so
it answers the same before and after a restart. In the container it reads
`development`. Until both are fixed, ask the process:

```
sudo md5sum /proc/$(systemctl show -p MainPID --value qsp)/exe /usr/local/bin/qsp
docker cp qsp:/qsp /tmp/qsp-container && grep -c "<a string only the new build has>" /tmp/qsp-container
```


**Both directions between an OpenBridge link and a Motorola repeater are
built, tested and never run on air.** Everything below this line is a claim
about code, not about a radio.

0271 fixed the two things that stood between QSP and audio in both directions,
and the diagnosis in the previous handover was wrong about the first one.

**`DeliverFromUpstream` never called `sendToIPSC` at all.** The gate named in
that handover — `res.Reason != "" && !res.NoHomebrewDestination` — was a real
second defect and would have turned the frame back, but it was never reached.
Three ingress paths, two call sites: `forward` and `DeliverFromIPSC` both end
in `sendToIPSC` and this one did not. §8a's "declared and read by nothing",
now ten times. It read as a routing refusal because the result carried a
reason at the same moment, and the reason was true of something else.

The second defect was that reason. `route` wrote `every destination refused the
frame` whenever nothing was delivered, including when nothing had judged the
frame — the link target skipped by the loop rule, the repeat target resolved to
no peers because there are no hotspots. `Drop.NotAJudgement` now separates a
rule about links from a verdict on a transmission, and false is the blocking
answer on both types so an unclassified drop keeps a refused frame off the
repeaters.

Each half was proved to fail on its own: with the call site removed the test
fails, and with the call site wired and the classification reverted it fails
differently.

**The work:** deploy, check `qsp --version` reads 0.1.113, and key up on the
Pi-Star. A hotspot user on production should be heard on the test server's
repeater. The reverse already works.



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

## Not proven

- **Relaying and deduplication, on any machine.** Every test is a unit test and
  a third server has never existed. This is the largest outstanding claim.
- A `qsp` link across the internet rather than a LAN. Both ends are on
  192.168.1.x.
- 0284's five fixes, which are on Fedora only.
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
