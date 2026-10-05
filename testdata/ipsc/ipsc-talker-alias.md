# ipsc-talker-alias.pcap

**The capture that showed a stream ID does not hold for a whole transmission.**
A Motorola repeater passing on a Talker Alias rewrites the header of its own
frames for one superframe, and QSP read each key-up as three calls.

SHA-256 `bef68ed942a77f4cdf4c8bca16e422071868e781b0d512bad95a77f7bae9ec12`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-10-05, about 01:11 UTC |
| Master | QSP 0.1.310 on the test server, `192.168.1.27:50000` |
| Peer | XPR8300, radio ID 999999, `192.168.1.233` |
| Radio | K9MLS 3132910, sending the alias "K9MLS Portable" |
| Capture | `tcpdump -i any udp and not port 53` on the test server |
| Packets | 158, every one a voice message from the repeater |

**The file is cut down from the capture.** Only the repeater's voice messages
are kept, re-framed as Ethernet with invented hardware addresses; the test
server's own traffic to another network was in the original and is not here.
The UDP payloads and their timing are as captured.

## What it holds

Two key-ups to TG 2 on timeslot 2. Each is one transmission on the air and
three descriptions of one in the repeater's frame headers:

| Key-up | Frames | Call counter | Source | Destination | Stream | Marks |
|---|---|---|---|---|---|---|
| 1 | 19 | `79` | 3132910 | 2 | `78a1` | three headers, the first flagged `80dd` |
| 1 | 6 | `7a` | 5002016 | 4929869 | `648f` | none |
| 1 | 81 | `7b` | 3132910 | 2 | `1950` | terminator, flagged `805e` |
| 2 | 19 | `7e` | 3132910 | 2 | `3e50` | three headers, the first flagged `80dd` |
| 2 | 6 | `7f` | 5002016 | 4929869 | `4b6c` | none |
| 2 | 27 | `80` | 3132910 | 2 | `6001` | terminator, flagged `805e` |

**5002016 and 4929869 are text.** They are `4C 53 20` and `4B 39 4D`: "LS "
and "K9M". The six frames begin at the one whose trailer carries the Link
Control `04 00 9c 4b 39 4d 4c 53 20`, which is the Talker Alias header block
(FLCO 4) holding "K9MLS ". The repeater has filled bytes 6 to 11 of its frame
header from that block as it would from a group call's Link Control, where the
same six octets are the destination and the source. Byte 17 reads `30` in those
six frames and `20` in the others; what it means is not known.

**Only the first alias block does it.** Later in the first key-up the trailers
carry `05 00 50 6f 72 74 61 62 6c` ("Portabl", FLCO 5) and `06 00 65` ("e",
FLCO 6), and the frame headers around them are the radio's own.

**No frame after the first 19 is marked as a beginning.** Every one carries the
flags of the middle of a transmission, `805d`, until the terminator, and none
is a header frame. That is what QSP now goes by: see
`ipscbridge`'s `continues`.

## What is not known

- Whether an SLR5700 does the same. This is one repeater model.
- Whether the change falls at the same place for a longer or shorter alias, or
  on timeslot 1. Both key-ups here are alike.
