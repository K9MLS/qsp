# Handover, 2026-09-08 night

Read `NEW-SESSION.md`, then **§8a** of `PROJECT_MEMORY.md`, then **ADR-0051**,
which decides how linking works from here and is confirmed on air. §8o is this
session.

Version **0.1.126**, patches 0261–0284. Everything through 0273 is deployed to
production and the test server; 0274 to 0276 are on Fedora only.

**0276 changes the container build**, so the test server needs a rebuild rather
than just a restart, and `/qsp --version` should read a real version afterwards
for the first time.

## What happened

**Audio crossed a QSP-to-QSP link in both directions**, 2026-09-08 16:16 UTC.
A hotspot user in Denton, heard on a Motorola repeater, through a link that is
a peer rather than a bridge.

```
peer connected  peer_id=3132912 callsign=K9MLS from=192.168.1.27:58483
call started    peer_id=3132910 talkgroup=2 timeslot=2 stream_id=2948633907
call started    subsystem=ipsc radio_id=999999 destination=2 timeslot=2 slot_bit=true
call ended      frames=34 converted=32 delivered=32 duration=1.982s
```

**`timeslot=2` is the whole thing.** Every previous run read TS1, because
OpenBridge forces it and two instances of the same software were using
OpenBridge to reach each other. The repeater had been keying on the slot nobody
monitors.

`bridges=0`, `qsp_links=1`. The dialling side's entire configuration is one
upstream block: name, address, DMR ID, password file, callsign. No listen
address, no export list, no import list, no timeslot, no port forward.

## Start here: a server says what it is

**An administrator cannot see half their own links.** Production and the test
server carry audio in both directions, and production's Links page reads *No
links are configured* — because the test server dials in, and the listening end
cannot tell a linked network from a hotspot. Same handshake, same port, same
peer table. It appears on the Peers page as 3132912 beside three hotspots.

ADR-0051 called that asymmetry invisible in use. It is not, and it was found
within four minutes of clicking through the console. **ADR-0052 is the frame
that decision should have been made inside**: QSP is a federation of sovereign
servers, and three choices made the same day made the sender responsible for
what the receiver gets.

The work, in ADR-0052's order:

1. **Done in 0283, and never seen in a browser.** Both consoles should now show
   the link, with the same name at each end. Load production's Links page: it
   should list an inbound link named as the test server names it, rather than
   "No links are configured". If it does not, the announcement is arriving and
   nothing reads it, or it is not arriving — the peer's SoftwareID and
   PackageID on the Peers API will say which.
2. **The Links page shows inbound links beside outbound ones**, with the same
   states, counts and last-heard, and the same name at both ends. An
   administrator asks one question and should not have to know which end
   dialled.
3. **A hop count**, bounding relay before deduplication catches it.
4. **The link's own state while retrying** — retrying, last attempt, last
   connected. Reconnection works and is silent to the console.

Then the accept form, which still writes an OpenBridge link with a bridge and a
timeslot box, and then removing `Export`, `Import` and the bridge-for-links
machinery.

**Before writing any of it, settle what a server's identifier is.** ADR-0052
leaves it open on purpose: it is the one choice that cannot be changed once
servers are running, because it is what everybody calls everybody else. A DMR ID
is unique and already issued, and it is issued to an *operator* — a server
outlives the person who registered it, and a club running three instances needs
three from a pool sized for members.

## Not run anywhere, and it is the largest claim outstanding

**Relaying and deduplication have never seen a third server.** Every test is a
unit test. With two servers there is nothing to relay to, so today's on-air
evidence says nothing about either. A third instance — a second container on the
test server on different ports is the cheapest — linked so a frame reaches one
server by two paths. A radio should hear one copy, and the journal should show
the second path refused with `already being carried from ...`.

## Two debts taken deliberately

**The link authenticates with the shared hotspot password.** Production has no
per-peer list, so `/var/lib/qsp/peer.pass` is what all three hotspots and now
the link use. ADR-0035 exists precisely so a member can be removed without
changing everybody's password, and this gives that up. It was taken to get the
on-air proof today and should be paid back when the accept form is built.

**The OpenBridge link between the two servers is disabled, not removed.** Both
running at once would carry every frame twice, and the deduplication that would
make that harmless is not built.

### Writing a qsp link by hand, to test it

Both servers need one. On the dialling side — the test server, which has no
forwarded port — the link names the other server's peer port:

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
bridge**. `repeater_id` must differ from every peer the far end already has and
from the far end's own ID: 3132910 is an operator ID and 3132911 is announced
by both servers' IPSC masters today. The password file holds whatever the far
end has in `dmr.password_file` for that ID.

Only the dialling side gets a `qsp` upstream. The listening side needs nothing
configured at all — the link arrives as a peer registration on 62031, which is
the port the three hotspots already use.

Two unexplained bytes, recorded rather than guessed at: byte 5 reads 1–3 in the
reference and a constant 4 from QSP, and the last byte of the 54-byte header is
`0x3b` in the reference and `0x00` from QSP. Neither is named in `voice.go`.

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

## Deployed

0261 through 0271 are on Fedora, production and the test server. The Links page
lying over working links (0264) and the IPSC console surface (0265) are both
live. Production runs under systemd; the test server runs `qsp:local` built from
the override compose file.

## What was built, and what each one is worth

| Patch | What |
|---|---|
| 0261 | The accept form's one address box fed two opposite fields; `-check` binds |
| 0262 | The page and the remover read different sources; no peering was ever audited |
| 0263 | Four rules enforced somewhere other than where they were written |
| 0264 | The reconcile could not run in the one case it was written for |
| 0265 | IPSC had no console surface at all |

**ADR-0050** records the wire-format change: a reciprocal says so in the token,
so an exchange can end after a restart.

## What is proven on a running system

- A QSP server logs into another QSP server as a peer, and audio crosses both
  ways with the talkgroup and timeslot intact.
- A frame from a link reaches a Motorola repeater (0271), and a frame from the
  repeater reaches the network.
- An IPSC repeater registers with a containerised instance.
- The peering exchange completes across two instances, both directions, and
  reaches the audit trail.
- `config.Validate` refuses a bridge endpoint naming an OpenBridge link on any
  slot but 1 — which caught the shipped `deploy/pair` examples on its first run.

## Not proven

- **Anything with more than two servers.** Relaying, deduplication, and a
  third instance are all untested. Ten servers is the design target and one
  link is the evidence.
- A `qsp` link across the internet rather than a LAN. Both ends of this one are
  on 192.168.1.x.
- A link surviving a far-end restart. Reconnection is specified in ADR-0051 —
  5 s to 120 s capped, forever — and not written.
- The IPSC panel from 0265, never loaded in a browser.
- Published image tag and CI publishing, still not set up. `docker compose up`
  without the build override tries `ghcr.io/k9mls/qsp:<version>` and is denied.
