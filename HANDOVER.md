# Handover, 2026-09-08 morning

Read `NEW-SESSION.md` for the standing brief, then **§8a** of
`PROJECT_MEMORY.md`, which is the section that matters most, then **§8m** for
this session. §8b through §8l are superseded and say so.

Version **0.1.107**, patches 0261–0265. Every one of them is on Fedora.
**0264 and 0265 are on neither server**, and 0264 matters most — see below.

## Start here: sendToIPSC drops frames that arrive over a link

**A frame from an OpenBridge link never reaches a Motorola repeater on a server
whose only local station is that repeater.** This is the last thing between QSP
and audio in both directions, and it is a code fix.

`sendToIPSC` in `internal/peers/listener.go` returns early when routing set a
reason:

```go
if res.Reason != "" && !res.NoHomebrewDestination {
    return
}
```

Its own comment describes the case it is meant to survive — *"a network of
Motorola repeaters and no hotspots has nowhere on the Homebrew side to deliver
and the frame should still reach the other repeaters"* — but the escape hatch is
`NoHomebrewDestination`, and a frame arriving **from an upstream** does not get
it. Routing reports the generic `every destination refused the frame`, and the
repeater never sees it.

Observed on 2026-09-08, test server, one repeater and no hotspots:

```
call started  subsystem=network peer_id=3132910 talkgroup=2 timeslot=1 stream_id=225593410
transmission not carried  subsystem=network peer_id=0 reason="every destination refused the frame"
```

The repeater's own transmission 3 seconds later carried normally, because that
direction does not pass through this gate.

**The work:** read `routing.Result` and find where `NoHomebrewDestination` is
set; decide whether an upstream-sourced frame with no local Homebrew peers is
the same case; build the test from the log lines above. Do not widen the
condition without knowing what `Reason` values reach it — a frame refused by
access control must still be refused.

## Then: every peering the accept form creates has the wrong timeslot

**OpenBridge forces timeslot 1.** `internal/protocol/openbridge/openbridge.go`
says so: *"The timeslot is forced to 1. Proper OpenBridge passes all traffic on
TS1."* Every frame crossing an OpenBridge link arrives as TS1, by protocol.

The accept handler writes a bridge whose **upstream** endpoint carries the
timeslot the operator chose — 2 by default, from the form. So nothing arriving
from that link can ever match it, in either direction:

- Pi-Star to production on TS2, across the link as TS1, refused by the far
  end's TS2 bridge.
- Repeater to the test server on TS2, across the link as TS1, refused by
  production's TS2 bridge.

Both ends were configured, both links reported healthy, and **no audio crossed
in either direction for a day**. The counters said `Sent 50 / Received 0` on one
side and `Received 28 / Sent 0` on the other, which reads like a network fault
and is not one.

Corrected by hand on both servers on 2026-09-08: the endpoint naming an upstream
is timeslot 1, the local peer endpoint keeps the operator's slot. **After that
change, a Motorola repeater was heard by a hotspot user across the link.**

**This was necessary and was not the whole story.** It is why a link-sourced
frame reaches routing at all; `sendToIPSC` above is why it goes no further.

**The fix belongs in `handleAcceptPeering`** in `internal/server/peering.go`,
where the bridge is built: an endpoint naming an OpenBridge upstream takes
timeslot 1 regardless of what the form asked for, and `config.Validate` should
refuse any other value so a hand-edited document cannot recreate it. Neither is
built.

## Then: a repeater keys up on network audio and transmits silence

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
