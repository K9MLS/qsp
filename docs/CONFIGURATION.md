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
`subscribers` names radio IDs permitted to transmit, and refusing one does not
disconnect the hotspot carrying it. `talkgroups` names what is carried on each
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
