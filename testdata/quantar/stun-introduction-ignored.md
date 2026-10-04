# stun-introduction-ignored.bin

**A Quantar ignoring an introduction sent on a link open from one end only.**
Taken with QSP 0.1.304 on the far end, which accepted the station's link
request, made none of its own, and answered each introduction at once with
`fd bf 01 05 c2 00 00 00 00 ff`.

SHA-256 `d212846ac19665f698cd00b6197fa7e87083b7e77d214386d5efa0e63b4bc8a8`

## Provenance

| | |
|---|---|
| Captured by | K9MLS, Denton TX |
| Date | 2026-10-04, from 13:06:41 UTC, eight seconds |
| Station and router | as `stun-link-request.md` |
| Recorded with | `tcpdump -i any 'tcp port 1994'` on the QSP host |
| Contents | **The router's side of the TCP stream only**, 326 bytes, cut from the packet capture to begin at a link request |

## What it holds

The same loop as `stun-introduction-unanswered.bin`, to the millisecond: a link
request, three introductions about half a second apart, and round again every
1.55 seconds. QSP's introduction went out within a millisecond of each of the
station's and changed nothing. The whole capture ran the loop 65 times.

**No acceptance from the station anywhere in it**, which is the fact 0.1.305 is
built on: the station was never asked to open the link, so it never said yes.

## What it does not hold

QSP's side of the stream. A link open from both ends, a keepalive from the
station, or voice.
