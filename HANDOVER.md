# Handover, 2026-09-08 evening

Read `NEW-SESSION.md` for the standing brief, then **§8a** of
`PROJECT_MEMORY.md`, then **ADR-0051**, which decides how linking works from
here. §8b through §8l are superseded and say so.

Version **0.1.115**, patches 0261–0273. Everything through 0271 is on Fedora,
production and the test server. **0272 (the ADR) and 0273 are new.**

## Start here: build ADR-0051

**0271 landed and worked on air.** A frame from an OpenBridge link reached a
Motorola repeater for the first time — `nowhere on the Homebrew side; carried to
the Motorola repeaters only`, and the repeater keyed. Both directions between a
link and a repeater are connected.

**The radio still heard nothing, and the cause is not the codec.** A capture of
what QSP sends to the XPR8300 was diffed against
`testdata/ipsc/ipsc-master-voice.pcap`. The framing is right: same length
distribution in the same 4:1:1 ratio, same 60 ms cadence, same six-burst
superframe, RTP sequence incrementing by 1 and timestamp by 480 per frame in
both. Three bytes differ, and `voice.go` names every one:

| Offset | Constant | Reference | QSP |
|---|---|---|---|
| 17 | `FlagSlot = 0x20` | set | clear |
| 30 | `FrameSlotBit = 0x80` | `0x8a` | `0x0a` |
| 31 (54-byte) | `HeaderSlotBit = 0x80` | `0xc0` | `0x40` |

Three independent fields, all the timeslot, all disagreeing the same way.
**QSP transmitted to the repeater on the slot nobody was listening to**, because
OpenBridge forced the frame to TS1 and the codeplug has TG 2 on TS 2.

So the burst shape is fine and ADR-0041's caveat, corrected in 0268, described
the symptom and named the reference that settled it in an afternoon.

**0273 built the first half of ADR-0051.** `protocol: "qsp"` exists: it dials
out, needs no bridge, needs no port forward on the dialling side, and repeat
reaches it as a peer so every talkgroup crosses with the slot intact.

**It has never carried a frame between two machines.** Everything above is
tests. The next session's first job is to write a `qsp` link into both
configurations by hand, restart, and key a radio.

Then, in order:

1. **Deduplication on source radio ID and stream ID**, which is what makes
   relaying safe and lets ten servers connect as a hub rather than
   forty-five peerings. `TestAFrameFromALinkIsNotRelayedYet` fails when this
   lands, deliberately — replace it, do not delete it.
2. **The accept form.** It still writes an OpenBridge link with a bridge and a
   timeslot box. It should write a `qsp` link and ask for neither.
3. **The ID-collision refusal and the arrived-frames console line.** These are
   what stop the next silent day.
4. Remove `Export`, `Import` and the bridge-for-links machinery.
5. OpenBridge narrowed to foreign networks; existing links re-peered.

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

## What is proven on a running system, and what is not

Proven by using it, this session:

- The peering exchange completes across two instances, both directions.
- `-check` reports in-use for a live configuration and catches an address this
  host does not have, with exit 1.
- `peering.offered` and `peering.accepted` reach the audit trail, with no
  "cannot record" warning. SECURITY.md's claim is true for the first time.
- An IPSC repeater registers with a containerised instance: `peer registered
  radio_id=999999 from=192.168.1.233:50001`. **Open item 5 is closed.**
- OpenBridge carries traffic from production to the test server: 28 frames
  received.

Not proven, and worth doing next:

- Audio from a repeater reaching the network at all — the timeslot question
  above.
- The Links page showing a link honestly, since 0264 is undeployed.
- The IPSC panel from 0265; it has never been loaded in a browser.
- The port-collision refusal from 0263 and the endless-exchange fix, both of
  which have tests and no live run.

## Open, not started

- **The test server's access list permits only `3132910`**, and the startup
  advisory says that is an operator ID rather than a repeater one. It has not
  bitten yet because IPSC has its own `allowed_peers`.
- **`links.cfg` is captured at construction** in `cmd/qsp`, so a link's
  displayed address comes from the startup configuration. A live setting read
  at construction — §8a's recurring shape — latent rather than firing.
- **`applyPending` does not reconcile upstreams.** A link opens and closes only
  at a restart. That is a recorded decision and 0262 makes it honest, but live
  reconciliation is worth an ADR: a restart drops every station, and telling a
  club administrator that adding a link means dropping the network is the
  a commercial DMR server-shaped answer.
- Published image tag and CI publishing, still not set up.
