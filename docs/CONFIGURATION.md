# Configuring QSP by hand

**Most operators never need this page.** Everything below can be done from the
console, which writes the same configuration file and keeps a version history
of it. This is the file format, for anybody who prefers a text editor, keeps
configuration in version control, or is building from source without the
Docker install's first-run settings.

On the Docker install the file is `/var/lib/qsp/qsp.json` inside the `qsp-data`
volume, and `deploy/docker/README.md` shows how to read and edit it. A source
build writes one with `./qsp -print-config > qsp.json`.

## Accepting peers

The DMR listener is **off by default**. To enable it:

```sh
printf 'your-shared-password' > /etc/qsp/peer.pass
chmod 600 /etc/qsp/peer.pass
```

Then in the configuration:

```json
"dmr": {
  "enabled": true,
  "listen_address": "0.0.0.0:62031",
  "password_file": "/etc/qsp/peer.pass",
  "access": {
    "registration": {"mode": "deny", "ids": []}
  }
}
```

The password is a **file path, never a value** — configuration is versioned,
exported and diffed, and a secret in it would land in all three. See
[ADR-0012](adr/ADR-0012-peer-password-file.md).

### Access control

**A listener on an address reachable from beyond this host must have an
`access` block, or QSP refuses to start.** The block above is the permissive
one: deny nobody, so everything is permitted. It exists so that permitting
everything is something an operator wrote down rather than something that
happened, and so it appears in a diff.

Four lists decide who is carried. Each has a `mode` of `permit`, which refuses
anything not named, or `deny`, which allows anything not named. An entry is an
ID or an inclusive range.

```json
"access": {
  "registration": {"mode": "permit", "ids": ["312100", "312100101"]},
  "subscribers":  {"mode": "deny",   "ids": []},
  "talkgroups": {
    "timeslot_1": {"mode": "permit", "ids": ["3100-3199"]},
    "timeslot_2": {"mode": "permit", "ids": ["9", "91"]}
  }
}
```

`registration` names repeater IDs permitted to log in — six digits for a
repeater, nine for a hotspot using an operator's ID and a two-digit suffix.
`subscribers` is a **ban list only**: `mode` must be `"deny"`, and it names
the radios refused. Every other radio may transmit. Refusing one does not
disconnect the hotspot carrying it. An allow-only (`"permit"`) subscriber list
is refused on save, and one already in a configuration file is opened when QSP
loads it, with a startup advisory saying how many radios it had named (0448). `talkgroups` names what is carried on each
timeslot, checked both when a frame arrives and again for each peer it would
reach, so that traffic from a bridge or a link is subject to the same list.

**QSP ships no network's talkgroup numbers.** They differ between networks, and
a list copied into this repository would be stale within the week. The lists are
yours to write. See [ADR-0020](adr/ADR-0020-access-control.md).

### Bridging talkgroups

```json
"dmr": {
  "forwarding": true,
  "bridges": [{
    "name": "tuesday-net",
    "enabled": true,
    "endpoints": [
      {"peer": 3132910, "talkgroup": 3148, "timeslot": 1},
      {"peer": 0,       "talkgroup": 91,   "timeslot": 2}
    ]
  }]
}
```

`"peer": 0` means every connected peer. Traffic arriving at one endpoint is
relayed to the others with its talkgroup and timeslot translated; the
originating radio ID is preserved, and a call is never sent back to its source.

**`forwarding` is separate from `enabled`.** With it off QSP relays nothing,
not even between stations on the same talkgroup. It is off in the built-in
defaults and **on in the configuration a first boot writes**; either way it is
the **Forwarding** switch under Network settings, so you can watch stations
connect before anything is relayed.

### Scheduling a net

```json
"schedule": [{
  "bridge":   "tuesday-net",
  "days":     [2],
  "start":    "20:00",
  "duration": "1h",
  "timezone": "America/Chicago",
  "enabled":  true
}]
```

Days are 0 for Sunday through 6 for Saturday. **A bridge named by any window is
controlled entirely by the schedule** — its own `enabled` field is ignored — so
there is never a question of which setting won.

Times are local wall-clock times in the named zone, so a net at 20:00 stays at
20:00 all year. Use an IANA name such as `America/Chicago`, not an abbreviation:
`CST` cannot express "20:00 local all year".

QSP logs the next occurrence of every window at startup, and warns about any
that daylight saving will skip.

### Linking on demand

```json
"triggers": [{
  "bridge":    "on-demand",
  "on":        [{"peer": 3132910, "talkgroup": 3148, "timeslot": 1}],
  "hang_time": "3m",
  "enabled":   true
}]
```

Transmitting on a listed endpoint opens the bridge; it closes three minutes
after the last transmission. **Trigger endpoints are separate from the bridge's
endpoints**, so a local repeater can open a link outward without the wider
network opening it inward.

A bridge may be scheduled, triggered, both, or neither. Either mechanism opening
it is enough, and the frame that opens it is itself relayed — no clipped first
syllable.

## Motorola P25 repeaters

Set from the console's **Network** page; this is the block it writes. **Off by
default.** A Motorola P25 repeater's V.24 card connects to a Cisco router's
serial port, and the router carries its frames to QSP over TCP (`stun route all
tcp` and this server's address). **QSP opens the repeater's link, keeps it
alive, and reads each call as far as who is talking and on which talkgroup; it
does not carry those calls anywhere yet.** The Overview's P25 row counts the
voice frames and names the repeater, its link and the last radio heard.

**Proven on a Quantar.** A GTR 8000 has the same V.24 interface and is expected
to work; none has been tried.

```json
"p25_repeaters": {
  "enabled":         true,
  "listen_address":  "0.0.0.0:1994",
  "allowed_routers": ["192.0.2.4"],
  "record_dir":      "/var/lib/qsp/repeaters",
  "site":            2,
  "present_as":      "repeater"
}
```

`allowed_routers` are router addresses; empty accepts any router that can reach
the port. `record_dir` is optional: when set, every frame in both directions is
written there as text, one file per connection, **voice included, so it grows
by about a megabyte for every five minutes of talking**. `site` is the site
number QSP introduces itself with, 1 to 127, and it must not be the repeater's
own. `present_as` is what QSP tells the repeater it is: `"repeater"` (the
default, a second repeater, site 2 unless set) or `"console"` (a Motorola
console interface, site 13 unless set). A repeater set for repeater-to-repeater
linking expects a repeater, and that is what the Quantar here took. A
connection silent for 30 seconds is closed, and a new connection from the same
router replaces the old. Changing any of these needs a restart.

The section was named `quantar` in 0.1.303 to 0.1.305. A configuration saying
that still loads, and is written back as `p25_repeaters`.

## Weather alerts

Set from the console's **Weather** page; this is the block it writes. **Off by
default.** Without `transmit` it previews: alerts that pass every check are
shown on the page and logged. With it they go out as a group text, **to this
server's own hotspots and Motorola repeaters only** — never to linked servers
or bridged networks, because weather is local.

```json
"weather": {
  "enabled":   true,
  "transmit":  true,
  "zones":     ["TXC121", "TXZ103"],
  "events":    ["* Warning", "* Watch"],
  "talkgroup": 2,
  "timeslot":  2,
  "sender_id": 9990,
  "contact":   "you@example.org"
}
```

`zones` are NWS county codes (a C, such as `TXC121`) and forecast-zone codes (a
Z, such as `TXZ103`) — the same codes SkywarnPlus uses; alerts.weather.gov lists
them under your state. `events` are classes — `"* Warning"` is every warning, `"* Watch"` every
watch, `"*"` everything — or NWS alert names exactly as NWS writes them.
`contact` is sent to the National Weather Service, which requires one; empty
uses `dmr.callsigns.contact`. Changes take effect on save, with no restart.
