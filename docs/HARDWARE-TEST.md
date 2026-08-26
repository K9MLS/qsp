# Hardware test: connecting a hotspot to QSP

**Time: about 30 minutes. Fully reversible.**

Rewritten 2026-08-25 after running it for real. The earlier version had you key
up on TG 9990 expecting parrot, which cannot work; see the end of this document.

**Forwarding stays off throughout.** QSP accepts the connection and observes. It
puts audio on nothing.

Host may be Windows, Linux or a Pi. Platform-specific steps are marked.

---

## Step 1 — Back up the gateway config

On the hotspot, before anything else:

```sh
rpi-rw
sudo cp /etc/dmrgateway /etc/dmrgateway.bak
```

Everything below is undone by restoring that file. Do this first.

---

## Step 2 — Read the config, not the dashboard

This step exists because skipping it cost a session. The WPSD dashboard showed
the custom DMR network as **enabled** while `/etc/dmrgateway` had `Enabled=0`.
DMRGateway reads the file. The hotspot registered with QSP, held a session and
sent keepalives for ten minutes, while routing every transmission to
BrandMeister — because the network it would have routed to was never loaded.

```sh
grep -n 'Enabled' /etc/dmrgateway
grep -n '^\[' /etc/dmrgateway
```

Note which line belongs to which block. You need two: BrandMeister, and
`[DMR Network Custom]`.

---

## Step 3 — Find the talkgroup that reaches QSP

**The number you dial is not the number that arrives.** WPSD's automatic rewrite
rules give each network a talkgroup prefix, and the mapping is in the file:

```sh
grep -A20 'DMR Network Custom' /etc/dmrgateway | grep TGRewrite
```

A line like `TGRewrite0=2,11,2,9,1` reads **slot 2, TG 11 -> slot 2, TG 9**. So
you dial **TG 11 on TS2** and it arrives at QSP as **TG 9**.

Write down your own numbers. They depend on which slot WPSD assigned. Do not
assume 11.

---

## Step 4 — Switch the networks over

BrandMeister must be **off**, not merely deprioritised. Its block carries
`PassAllTG=1` and `PassAllTG=2` — every talkgroup on both slots — and
`Primary=1` puts it first. Left enabled, it takes the traffic before QSP is
considered.

Using the line numbers from step 2:

```sh
sudo sed -i '<bm-line>s/Enabled=1/Enabled=0/' /etc/dmrgateway
sudo sed -i '<custom-line>s/Enabled=0/Enabled=1/' /etc/dmrgateway
sed -n '<bm-line>p;<custom-line>p' /etc/dmrgateway
```

That must print `Enabled=0` then `Enabled=1`. If not, stop.

The dashboard works too, but **verify the file afterwards** either way. That is
the lesson of step 2.

---

## Step 5 — Start QSP

**Windows:**

```powershell
cd C:\path\to\qsp
.\qsp.exe -config qsp.json
```

Windows prompts about the firewall. **Allow it on private networks.**

**Linux:**

```sh
./qsp -config qsp.json
sudo firewall-cmd --add-port=62031/udp     # firewalld hosts, or packets vanish
```

Expected:

```
level=INFO msg=starting version=0.1.5
level=WARN msg="running without persistence" ...
level=INFO msg="forwarding disabled; traffic is observed and not relayed"
level=INFO msg="console listening" subsystem=server address=127.0.0.1:8080
level=INFO msg="listening for peers" subsystem=network address=0.0.0.0:62031
```

The persistence warning is expected: no SQL driver is compiled in, so nothing
survives a restart. Peers, calls and counters work, held in memory.

Get the host's LAN address — **not** `127.0.0.1`, which means "whoever is
asking":

```powershell
ipconfig | Select-String IPv4                              # Windows
```
```sh
ip -4 addr show scope global | grep -oP 'inet \K[\d.]+'    # Linux
```

---

## Step 6 — Point the hotspot at QSP

Dashboard -> Admin -> Configuration -> DMR Configuration -> **Custom DMR Network
Settings**:

| Field | Value |
|---|---|
| Server Name | `QSP` |
| Server Address | the host's LAN address |
| Port | `62031` |
| Password | the contents of `peer.pass` |
| ESSID | **None** |

Leave ESSID as None; it appends a suffix to your repeater ID, and QSP registers
you under the plain one.

Apply Changes, then confirm DMRGateway actually loaded QSP:

```sh
sudo systemctl restart dmrgateway
sleep 15
sudo tail -40 /var/log/pi-star/DMRGateway-$(date +%Y-%m-%d).log
```

**You must see a block naming QSP**, with its rewrite rules:

```
I: DMR Network N Parameters
I:     Name: QSP
I:     Rewrite RF: 2:TG11 -> 2:TG9
```

If QSP is absent from that log, DMRGateway is not connected to it and no
talkgroup will reach it. Return to step 2.

---

## Step 7 — Watch it register

Within about 30 seconds:

```
level=INFO msg="peer connected" subsystem=peers peer_id=3132910 callsign=K9MLS
```

Leave it five minutes. `Idle` should cycle between 0 and about 10 seconds. A
peer that connects and vanishes after 60 seconds means keepalives are not being
answered, which is a different and more informative failure.

**Registration is not the gate.** The console will show datagrams arriving with
`voice frames: 0` and say so in a banner. That is keepalives only.

---

## Step 8 — The gate: key up

Radio to the talkgroup from step 3 — the one on the **left** of the rewrite rule
— on **TS2**. Transmit for ten seconds. Talk rather than keying and releasing,
so there is a run of voice frames.

Success is the call under **Last heard**:

| Check | Expected |
|---|---|
| Target | the talkgroup on the **right** of the rewrite rule |
| Slot | TS2 |
| Frames | climbing, **~16.7 per second** |
| Dropped | 0 |

The frame rate is the real test. DMR sends one frame per 60 ms, so ten seconds
is roughly 167 frames. Landing within a couple of percent means the codec tracks
the radio's timing exactly.

**You will not hear anything back.** Forwarding is off and there is one peer.
Frames arriving is the win.

If nothing appears, key up once then:

```sh
sudo tail -30 /var/log/pi-star/DMRGateway-$(date +%Y-%m-%d).log
```

That names the network each call went to.

---

## Step 9 — Capture it

Worth more than the test result. Start it *before* keying up.

```sh
sudo tcpdump -i any -w ~/qsp-voice-$(date +%Y%m%d).pcap 'udp port 62031'
```

The `promiscuous mode not supported` warning is harmless. `-i any` records both
legs — the master link and the MMDVMHost/DMRGateway loopback — documenting both
protocol dialects in one file.

Windows host with Wireshark installed:

```powershell
& "C:\Program Files\Wireshark\dumpcap.exe" -i 1 -f "udp port 62031" -w qsp.pcapng
```

Copy it off with `scp` run **from the receiving machine**:

```
scp pi-star@pi-star.local:~/qsp-voice-*.pcap .
```

**Sanitise if the capture contains a login.** The `RPTK` handshake carries
`SHA-256(salt || password)`; with the salt in the same file that is an offline
brute-force target. A capture started mid-session has no handshake and needs
nothing. Use a throwaway password regardless. Redaction procedure is in
`testdata/hbp/hbp-login-session.md`.

---

## Putting it back

```sh
sudo cp /etc/dmrgateway.bak /etc/dmrgateway
sudo systemctl restart dmrgateway
```

Then `Ctrl-C` QSP, and on a firewalld host:

```sh
sudo firewall-cmd --remove-port=62031/udp
```

---

## Two things the first attempt got wrong

**Parrot is not achievable.** The gate was written as "a real hotspot keys up
and hears itself through parrot." Parrot is a BrandMeister service; QSP does not
implement it, and this test requires BrandMeister off. There is nothing to echo
you back. The substance of the gate — a live transmission reaching the codec and
decoding — is what steps 8 and 9 verify.

**The dashboard is not the configuration.** See step 2.

---

## When it does not work

| Symptom | Cause |
|---|---|
| No packets arrive at all | Host firewall — step 5 |
| Peer registers, `voice frames: 0` | DMRGateway has not loaded QSP, or wrong talkgroup — steps 2 and 3 |
| QSP absent from the DMRGateway log | `Enabled=0` in the file — step 2 |
| Calls reach BrandMeister instead | BM still enabled; `PassAllTG` claims everything — step 4 |
| `bind: address already in use` | something else holds 62031 |
| Peer connects then drops after 60 s | keepalives unanswered — capture and report it |
| Call appears, frame count stays 0 | headers parse, voice frames do not |
| Login rejected | password mismatch; check for a trailing newline on the WPSD side |

### Reading a capture

Frames below the 60-byte Ethernet minimum are zero-padded, and the padding is
recorded. An 11-byte `MSTPONG` yields a 39-byte IP datagram padded by 7 bytes,
so slicing from the end of the UDP header to the end of the record gives an
18-byte message no parser accepts — and it looks exactly like a protocol defect.
**Clip to the UDP length field.** `internal/peers/pcap_test.go` is the
reference.

---

## The password file's permissions

On Linux, macOS and the Pi, **QSP refuses to start if `peer.pass` is readable
beyond its owner**:

```
qsp: the peer password file is mode 0644, readable beyond its owner;
run chmod 600 on it
```

`chmod 600 peer.pass` and start it again. `ssh` behaves the same way about
private keys, and for the same reason: a shared secret every account on the host
can read is not a secret, and nothing about that failure is visible at runtime.

**On Windows the check does nothing.** `os.Stat` there does not report an ACL —
it synthesises a mode from the read-only attribute, so an ordinary file reads as
`0666` however tightly it is actually secured. Enforcing the POSIX rule would
reject every correctly protected file. Secure it with an ACL granting only the
account QSP runs as.
