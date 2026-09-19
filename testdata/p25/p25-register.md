# P25 registration capture, 2026-09-11

`p25-register.pcap` — 1876 packets, md5 `11e507fb43f8cb054bb024983a7cbcc7`,
none dropped. Taken with `p25gateway.timer` disabled first, so the service could
not be restarted underneath the capture.

## The finding: there is no registration

**A gateway announces itself with a poll and the far end returns the identical
datagram.** No login, no acknowledgement of a different shape, no session
establishment. Ninety-six polls out, ninety-six back, every one eleven bytes of
`0xF0` followed by the callsign space-padded to ten.

```
  2.138s  192.168.1.155 -> 198.51.100.222  f04b394d4c532020202020
  2.151s  198.51.100.222 -> 192.168.1.155  f04b394d4c532020202020
```

**Every 5.01 seconds**, with no variance worth naming across all ninety-six.

## Why this mattered more than it sounds

The obvious assumption is that a login exists. A listener built around one that
does not would have been wrong in the direction no test catches: it would work
against itself, and fail against a real gateway in a way that looks like a
network problem.

The two earlier captures could not answer it — both began with the gateway
already running, and the second was restarted by a systemd timer underneath the
capture.

## What else this settles

**The timeout is measured rather than guessed.** Five seconds nominal, so
`internal/p25link` treats three missed polls — fifteen seconds — as a gateway
having gone. The handover records the failure that guards against: an interval
guessed wrong looks right until a gateway drops an hour later and nobody can say
why.

## What it does not settle

**The far end echoes the gateway's own callsign rather than announcing itself.**
So a QSP on the reflector side learns nothing about who it is talking to from
the reply — the poll is an assertion by the gateway and nothing verifies it,
which is ADR-0052 rule 4 in a new place. The allow list is therefore the only
thing between the port and anybody who knows a callsign.

**A second radio.** Every transmission in every capture is the same one, so the
source field is confirmed as a 24-bit identifier matching this radio and not yet
proven to follow a different one.

## Filtered, 2026-09-19

**This capture was taken with no capture filter and held 1448 packets that were
not radio**: mDNS naming household devices and their services, Syncthing local
discovery with a device ID and a public address, Plex discovery, and SSDP with
router UUIDs. They are removed (`scripts/filter-capture.py`, keeping the radio
ports only).

**What remains is byte-identical to what arrived.** The file header and every
kept packet's record header and payload are untouched, so the counts this
document cites still hold: the poll and voice figures were compared before and
after and are the same. `internal/p25link`'s tests pass on the filtered file.
