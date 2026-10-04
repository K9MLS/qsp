# stun-voice-three-calls.bin

**The first open link to a Motorola Quantar, and the first voice heard through
one.** Taken with QSP 0.1.305 on the far end, the first build the repeater
linked to.

SHA-256 `4d7adb2a5250a06ec2661af5776668c015c34e75ee2680f37645a06ea78d9b07`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-10-04, from 16:34:59 UTC, 113 seconds |
| Station and router | as `stun-link-request.md` |
| Radio | a P25 handheld keyed three times through the repeater |
| Recorded with | `tcpdump -i any 'tcp port 1994'` on the QSP host |
| Contents | **The router's side of the TCP stream only**, 14045 bytes, from the router connecting to the last frame captured |

## What it holds

The link opening, then idle, then three transmissions.

```
fd 3f                              the repeater asks for the link
fd bf 01 03 c2 00 00 00 00 ff      and introduces itself: site 1, a Quantar
fd 73                              it accepts QSP's own request
07 bf 01 03 c2 00 00 00 00 ff      its introduction again, from address 07
fd 01                              Receive Ready, every 5.01 seconds, 20 of them
```

Each transmission is information frames, control `03`, address `07`:

```
07 03 00 02 02 0c 0b 00 00 00 00 00     start
07 03 60 …  (32 bytes)                  header, first half
07 03 61 …  (24 bytes)                  header, second half
07 03 62 … 07 03 73 …                   voice: the P25 network frames, types 62 to 73
07 03 00 02 02 25 0b 00 00 00 00 00     end, sent twice
```

| Transmission | Seconds | Voice frames |
|---|---|---|
| 1 | 2.8 | 135 |
| 2 | 1.8 | 81 |
| 3 | 6.1 | 297 |

528 information frames in all: 513 voice, 6 header halves, 3 starts, 6 ends.

**The link control alternates.** Frames `64`, `65` and `66` of every other
voice unit carry `00 00 04 · 00 00 01 · 7b 4b af`: format 00, manufacturer 00,
talkgroup 1, radio 8080303. The units between carry `06 90 01 · 2f 24 d9 ·
ba ef d8`, a Motorola word, manufacturer 90, which names neither. Frame `70`
is `80 00 00` throughout: no encryption.

## What it does not hold

QSP's side of the stream, which is in the log lines of that day and was: `fd 73`,
`fd 3f`, `fd bf 01 05 c2 00 00 00 00 ff` twice, and `fd 01` every two seconds.
**No voice sent to the repeater**, which is what phase 4 needs and nobody has
captured. No second talkgroup, no second radio, no encrypted call, and no data.
