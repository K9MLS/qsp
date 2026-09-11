# P25 capture request: what is still needed

`p25-voice.pcap` answered most of the first request — seven transmissions, no
dropped frames, and the frame table in `p25-voice.md` is built from it.

**Two things remain, and the first is what blocks routing.**

---

## 1. Two talkgroups and two radios

**The problem in one sentence:** frames `0x66` to `0x69` each carry three bytes
that were identical across all seven captured transmissions, so the talkgroup
and source ID are certainly in there and cannot be located.

A field that never changes is indistinguishable from framing that never changes.
Until something varies, any claim about which byte means what is a guess — and a
guess in a routing field sends a call to the wrong place.

**What to capture:**

- **Two different talkgroups.** Transmit on one, then the other, in the same
  capture. The bytes that change between them are the talkgroup.
- **Two different radios**, if there is a second to hand. The bytes that change
  when the radio changes are the source ID.
- **Several transmissions on each**, so a byte that changes for some other
  reason is not mistaken for one of these.

Two talkgroups alone is enough to make a start. Two radios as well settles both
fields in one capture.

**Why it cannot be worked out any other way:** the P25 air interface is
specified, but the way a gateway packages it into UDP is a convention rather
than a standard, and ADR-0029 forbids reading another implementation to find
out. The Homebrew and IPSC work was built the same way — from captures.

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
