# stun-introduction-unanswered.bin

**What a Quantar does when its link request is accepted and its introduction is
not answered.** Taken with QSP 0.1.303 on the far end, which accepted the link
and knew nothing further.

SHA-256 `b9b09b537dbd72c058014fc18357c0c0217da4e7c61deb91c57fa0be447e11b0`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-10-04, from 12:54:02 UTC, the first eight seconds |
| Station and router | as `stun-link-request.md` |
| Recorded with | `tcpdump -i any 'tcp port 1994'` on the QSP host |
| Contents | **The router's side of the TCP stream only**, 363 bytes, cut from the packet capture |

## What it holds

```
08 31 00 02 00 1e 01  + 30 bytes                     the router's opening message
08 31 00 00 00 02 01  fd 3f                          link request
08 31 00 00 00 0a 01  fd bf 01 03 c2 00 00 00 00 ff  introduction, 23 ms after QSP's fd 73
                      ... twice more, about half a second apart
08 31 00 00 00 02 01  fd 3f                          and round again, 1.55 s after the first
```

In the introduction `01` is the message type, `03` is twice the station's site
number plus one (site 1), and `c2` is the type a Quantar gives for itself. That
is the published account of this frame, confirmed here to the byte.

The whole capture ran this loop 97 times: 97 link requests, 97 acceptances from
QSP, 291 introductions.

## What it does not hold

QSP's side of the stream, which was `fd 73` after every link request and nothing
else. No answered introduction, no keepalive and no voice.
