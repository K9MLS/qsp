# ipsc-text.pcap

**Text messages over IP Site Connect**, the first capture of them in this
project. Every other IPSC fixture is voice or registration.

SHA-256 `80cc0cb86acb6d055e9b6c6d070a3b359d3978f59c788df58c0aa26242e90a93`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-09-03, 18:45 to 18:48 local |
| Repeater | XPR8300, radio ID **999999**, `192.168.1.233`, colour code 4, peer of QSP |
| Master | QSP 0.1.47 at `192.168.1.247:50000` |
| Sending radio | 3132910 (`0x2fcdee`) |
| Capture | `tcpdump -i any -s 0 'host 192.168.1.233'` |
| Link type | Linux cooked v2 (276) |
| Packets | 222, of which 163 are data bursts |

QSP parsed none of it: every burst was logged as an unrecognised datagram. The
capture is therefore of a repeater talking to a master that never answers, which
is what makes the retry behaviour in it visible.

## What it contains

One group text to talkgroup 2 and several private texts to radio 3155373
(`0x3025ad`), sent from a radio through the repeater.

| Type | Count | Meaning |
|---|---|---|
| `0x83` | 44 | group text |
| `0x84` | 119 | private text |

Burst lengths are 34, 54 and 60. Bursts arrive 60 ms apart, the same cadence as
voice.

## What it establishes

- **`0x83` is a group text and `0x84` is a private text**, confirmed by both the
  envelope destination and the destination inside the payload.
- **The envelope is the voice envelope**, including the `00 0a 80 0a 00 60`
  constants at bytes 32 to 37. Byte 12 reads `01` where voice reads `02`.
- **Byte 30 is the DMR data type** — CSBK, Data Header, Rate 1/2 and Rate 3/4
  appear — and it equals the low nibble of byte 51 in 153 of 162 frames, byte 51
  being the Slot Type established by the voice work.
- **Byte 41 counts blocks remaining**, down to zero.
- **Byte 52 reads `0x3b`** in 150 of 162 frames, the same value as
  `ipsc-master-voice.pcap` from the same repeater.

## The retries, which are the reason to keep this capture

Transmission groups repeat byte for byte apart from sequence and timestamp: the
group text twice, one private text four times, another twice, at four to five
second intervals.

**Something is waiting for an acknowledgement that never arrives.** QSP parses
neither type and answers nothing. Whether a master owes a reply — and what it
looks like — is the open question in
[ADR-0045](../../docs/adr/ADR-0045-ipsc-text-messages.md), and this fixture is
the evidence for it.
