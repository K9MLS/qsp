# Hardware test: connecting a real hotspot to QSP

**Time: about 20 minutes. Fully reversible.**

This is the first time QSP meets a real DMR peer. Everything in it so far is
built on two packet captures and one person's reading of them; this is the check
on whether that reading was right.

**Forwarding is off for this test.** QSP will accept the connection and watch,
and will not put audio on anything.

---

## What you need

- The `qsp.exe` binary (or the Linux one) — no Go install required
- `qsp.json` and `peer.pass` beside it
- Your WPSD hotspot on the same network
- Your PC's LAN IP address

---

## Step 1 — Find your PC's LAN IP

In PowerShell:

```powershell
ipconfig | Select-String IPv4
```

Note the `192.168.1.x` address. **That is not `127.0.0.1`** — the hotspot has to
reach your PC across the network, and `127.0.0.1` means "this machine" to
whoever says it.

---

## Step 2 — Start QSP

Put `qsp.exe`, `qsp.json` and `peer.pass` in one folder, then:

```powershell
cd C:\Users\Mike\qsp
.\qsp.exe -config qsp.json
```

Expected output:

```
level=INFO msg=starting version=v0.1.0-test
level=INFO msg="running without persistence" ...
level=INFO msg="forwarding disabled; traffic is observed and not relayed"
level=INFO msg="listening for peers" subsystem=network address=0.0.0.0:62031
level=INFO msg="console listening" address=127.0.0.1:8080
```

**Windows will ask about the firewall.** Allow it on private networks. Without
that, the hotspot's packets never arrive and everything below looks like a QSP
bug.

The `running without persistence` line is expected — no SQL driver is compiled
in yet.

---

## Step 3 — Open the console

<http://127.0.0.1:8080>

You should see **"No peers are connected"** and a note that nothing is routed.
That is the correct starting state.

---

## Step 4 — Point the hotspot at QSP

WPSD dashboard → **Admin** → **Configuration** → **DMR Configuration**.

In **Custom DMR Network Settings** (the bottom section of the page):

| Field | Value |
|---|---|
| Custom DMR Network Enable | **on** |
| Server Name | `QSP` |
| Server Address | your PC's LAN IP from step 1 |
| Port | `62031` |
| Password | `qsptest123` |
| ESSID | leave as-is |

And in **BrandMeister Network Settings** above it:

| Field | Value |
|---|---|
| BrandMeister Network Enable | **off** |

Turning BrandMeister off matters. Leaving both on puts your hotspot on two
networks at once and makes the result hard to read.

Click **Apply Changes**. WPSD restarts its services.

---

## Step 5 — Watch

Within about 30 seconds, QSP should log:

```
level=INFO msg="peer connected" subsystem=peers peer_id=3132910 callsign=K9MLS from=192.168.1.155:...
```

and the console should show K9MLS in the peer table.

**Leave it for five minutes.** The peer should stay listed, with `Idle` cycling
between 0s and about 10s as keepalives arrive. A peer that connects and then
disappears after 60 seconds means keepalives are not being answered, which is a
different and more interesting failure than not connecting at all.

---

## Step 6 — Key up

Transmit for a few seconds on any talkgroup. It should appear under **Last
heard** with your radio ID, the talkgroup, timeslot and a frame count.

Nothing is forwarded — there is nowhere to forward it to, and forwarding is off.

---

## Putting it back

WPSD → DMR Configuration → Custom DMR Network Enable **off**, BrandMeister
Network Enable **on** → Apply Changes.

Stop QSP with `Ctrl-C`.

Nothing on the hotspot was modified beyond those two switches.

---

## If it does not work

Every outcome below is useful. **A failure here is worth more than another
hundred passing tests**, because it is the first information from outside this
project.

### Nothing at all in the QSP log

Packets are not arriving. Check the Windows firewall, confirm the LAN IP, and
confirm the port matches. From the Pi:

```sh
nc -u -w2 <your-pc-ip> 62031 </dev/null && echo reachable
```

### QSP logs a drop

Run it with more detail:

```powershell
.\qsp.exe -config qsp.json
```

then edit `qsp.json` to set `"level": "debug"` and restart. Every discarded
datagram is logged with a reason. **Send me that reason** — it names the exact
step that failed.

### `authentication failed`

The password in WPSD does not match `peer.pass`. Note that `peer.pass` must
contain the password **with no trailing newline**; if you recreate it, use
`printf` rather than `echo`.

### Connects, then drops after 60 seconds

Keepalives are not being answered as the hotspot expects. This would be a real
finding: it means QSP's `RPTPING`/`MSTPONG` handling differs from what MMDVMHost
wants. Send the log.

### Connects but the callsign or frequencies look wrong

The `RPTC` configuration layout is misread. Send a screenshot of the peer table
— the wrong values will say which field boundary is off.

### It works

Then the handshake, the SHA-256 authentication, the configuration parsing and
the keepalive handling are all confirmed against real software, and the four
layers built on top of them are standing on something solid.
