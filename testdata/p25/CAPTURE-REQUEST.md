# P25 capture request: what is still needed

`p25-voice.pcap` answered most of the first request — seven transmissions, no
dropped frames, and the frame table in `p25-voice.md` is built from it.

**Two things remain, and the first is what blocks routing.**

---

## 1. Two talkgroups — **answered 2026-09-11**

`p25-talkgroups.pcap` settled it: fourteen transmissions, four talkgroups, one
returned to after another was used. Frame 0x65 carries the talkgroup and frame
0x66 the source radio; see `p25-talkgroups.md`.

**A second radio is still wanted**, though it no longer blocks anything. Every
transmission so far is the same radio, so the source field is confirmed as a
24-bit identifier that matches this one and is not yet proven to follow a
different one. Any second P25 radio on any talkgroup, in the same capture as
this one, closes it.

---

## 2. The registration handshake

Start the capture **before** the gateway, so the first exchange is in it.
Registration happens once; a capture beginning after the gateway is running
shows steady state, which is the part that can be reasoned about, and hides the
part that cannot.

On the hotspot:

```sh
rpi-rw
sudo systemctl stop p25gateway
sudo tcpdump -i any -w /tmp/p25-register.pcap -s 0 udp &
sleep 2
sudo systemctl start p25gateway
```

Then let it sit quiet for thirty seconds — that records the keepalive interval,
and an interval guessed wrong looks right until a gateway drops an hour later —
before transmitting.

---

## What a capture will never answer

**What a P25 radio does with a frame that is wrong.** A capture shows a correct
implementation sending correct frames. The IPSC work found its real defects on
the far side of that line: a repeater that keyed up and transmitted silence, and
a codec that was right against captured bursts and wrong against a radio.

So a capture gets P25 to the point of being relayable. Getting it to the point
of being *trustworthy* needs a radio and somebody to listen.
