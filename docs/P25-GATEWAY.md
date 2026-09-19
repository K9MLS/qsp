# Connecting a P25 gateway

For an operator pointing P25Gateway — Pi-Star, WPSD, or a hand-rolled
MMDVM_Bridge chain — at QSP. `P25-NETWORK.md` is the design and its limits;
this is the wiring, and the two traps that are not QSP's and cost an hour on
2026-09-19 to find.

## What QSP does with a P25 gateway today

**A flat reflector.** Every registered gateway hears every transmission,
whatever talkgroup it is on: `voice` reads the talkgroup and does not route on
it yet. That is fine for one server and a couple of hotspots and is not a
network; `P25-NETWORK.md` §2 and §6 say what that costs and why routing comes
first.

**Voice from a gateway that has not polled is dropped**, because a voice frame
carries no callsign and the only thing tying it to a gateway is the address its
poll came from. So a gateway whose poll was refused is silent rather than
half-working.

## The QSP side

From the console's P25 page, which writes the same file the console keeps
history for:

- **Turn the listener on** and give it an address. **41000/udp** is what amateur
  reflectors use and what a gateway tries first.
- **List the gateways you answer.** An empty list answers every gateway that
  knows the address, which is the same hazard as an empty IPSC list.
- **A callsign is a claim, not a credential.** The reflector protocol has no
  login: a gateway asserts a callsign in its poll and QSP believes it. That is
  the protocol, not QSP, and no version of QSP fixes it. The allow list is the
  only thing between the port and anyone who knows it, so it is worth
  maintaining and it is not authentication.

**Reachability.** A gateway on your own network reaches the port directly. One
anywhere else needs 41000/udp forwarded to this machine, and that is what a
stranger's gateway will use.

Confirm QSP is listening before touching the gateway:

```sh
ss -lunp | grep 41000
journalctl -u qsp | grep -i 'subsystem":"p25' | tail -3
```

The startup line names the address, the callsign and how many gateways are
allowed.

## The gateway side, and the two traps

### The local hosts file adds entries; it does not override them

P25Gateway reads two host lists, `HostsFile1` then `HostsFile2` — on Pi-Star
`/usr/local/etc/P25Hosts.txt` then `/usr/local/etc/P25HostsLocal.txt`. **The
first file wins.** If your talkgroup number already appears in the downloaded
`P25Hosts.txt`, an entry for it in `P25HostsLocal.txt` is ignored, and the
gateway keeps using the published address.

Check both before believing either:

```sh
grep -n '^<your talkgroup>' /usr/local/etc/P25Hosts.txt /usr/local/etc/P25HostsLocal.txt
```

Two entries means the local one is not in use. Pi-Star refreshes
`P25Hosts.txt` on a schedule, so deleting the line there is a repair and not a
fix: the published entry comes back and wins again.

### A hostname from inside your own network

If the published entry is a hostname pointing at your public address, a gateway
**on your own LAN** resolves it, sends out to your router, and usually gets
nothing back: returning to your own public address from inside needs hairpin
NAT, which many routers do not do, and it still needs the port forwarded.

The durable arrangement is to make the name answer correctly from both sides:
local DNS resolving it to the server's LAN address, or a router that hairpins.
Then a LAN gateway and a stranger's gateway both work from the same published
entry, and no host file needs editing.

### A file with no trailing newline

`echo ... >> P25HostsLocal.txt` on a file that does not end in a newline
appends onto the last line and silently corrupts the entry before yours. This
finds it:

```sh
awk 'NF && $1 !~ /^#/ && NF != 3 {print "BAD: " NR ": " $0}' /usr/local/etc/P25HostsLocal.txt
```

### An edit is not enough: restart, then relink

P25Gateway resolves a reflector's address when the link is made and keeps it.
So after any host file change: restart the gateway, then **drop the link and
make it again from the radio** — select the unlink talkgroup, wait a few
seconds, reselect yours. A restart alone leaves a live link pointed where it
was.

## Confirming it worked

On the QSP machine, with the gateway linked:

```sh
sudo timeout 60 tcpdump -n -i any udp port 41000 -c 10
journalctl -u qsp --since '5 min ago' | grep -i p25
```

What each tells you:

- **Nothing captured at all**: the gateway is not sending here. Capture on the
  gateway instead and read the destination address in its own packets — that is
  what found both traps above. An idle gateway also sends nothing, so key up or
  relink while capturing.
- **Packets captured, nothing in QSP's log**: QSP is receiving and refusing.
  The likely cause is a callsign that is not on the allow list.
- **`p25 gateway registered`** with the callsign and address: it is linked.

The console's P25 check says `no gateways have linked yet` until one does, and
that wording is accurate rather than pessimistic — it was right throughout the
hour it took to find that the gateway was sending to the wrong machine.

**A gateway polls every 5 seconds**, and QSP forgets one after three missed
polls. On a capture, an 11-byte datagram at that interval is a poll; a burst of
22, 14, then 17-byte datagrams is a transmission.

## What has actually been done

**One gateway, on a LAN, registered and polling: K9MLS from a Pi-Star at
192.168.1.155, on production, 2026-09-19.** Nothing beyond that. No second
gateway, no gateway from outside the network, no P25 audio confirmed on air
through QSP, and no talkgroup routing. `CAPABILITIES.md` is the list that stays
current; this paragraph is here so the guide cannot read as more proven than it
is.
