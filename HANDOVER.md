# Handover, 2026-09-08 morning

Read `NEW-SESSION.md` for the standing brief, then **§8a** of
`PROJECT_MEMORY.md`, which is the section that matters most, then **§8m** for
this session. §8b through §8l are superseded and say so.

Version **0.1.107**, patches 0261–0265. Every one of them is on Fedora.
**0264 and 0265 are on neither server**, and 0264 matters most — see below.

## Start here: a repeater keys up on network audio and transmits silence

**This is measurable, and a caveat said otherwise for five days.**

The IPSC relay logged, on every start, *"built from inference; no capture of a
master sending voice exists (ADR-0041)"*. That stopped being true on
2026-09-03. `testdata/ipsc/ipsc-master-voice.pcap` is 347 packets of an
XPR8300's own RF, 288 of them voice, captured by K9MLS, and **four tests
already read it** — `internal/ipscbridge/master_test.go`,
`internal/ipsclink/parrot_test.go`, `internal/dmrfec/slottype_test.go` and
`internal/ipscbridge/encode_test.go`.

On 2026-09-08 that stale line was read twice as evidence the direction could not
be verified, while the reference to verify it against was in the tree. It cost
an hour. 0268 corrects it and adds a test that fails if a caveat denies a
fixture the repository contains.

**The work:** compare the voice bursts QSP sends against that capture and diff.
Not a new investigation — the method §7 records as having worked repeatedly, on
a reference that already exists.

Everything else in the path is now proven on air:

- The repeater registers with a containerised instance.
- Its audio is decoded and routed: `frames=46 converted=44 delivered=44`.
- It crosses an OpenBridge link between two QSP instances, both ways.
- A hotspot user can key up and **see the repeater transmit** — the header
  arrives and the repeater acts on it. Only the audio inside is wrong.

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

## Deploy 0264 before anything else

**The Links page has been lying on both servers.** `handleLinks` returned early
when the running link set was nil — and `cmd/qsp` decides that nil *at startup*,
from the startup configuration, so an instance that booted with no upstreams can
never display a link accepted afterwards. Both servers showed *No links are
configured* over the top of working links for a whole morning.

That is every fresh install accepting its first peering. It is fixed in 0264 and
that patch is not deployed.

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
