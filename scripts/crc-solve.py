#!/usr/bin/env python3
"""Solve the DMR packet CRC from the group calibration capture.

This is the search that found dmrfec.PacketCRC, kept so the result can be
repeated from the fixture rather than taken on trust. See ADR-0067.

**It solves rather than guesses.** For two messages of the same length, a
CRC's initial value and output mask cancel, and the polynomial P must divide

    D(x) * x^32 + R(x)

where D is the two messages XORed and R their CRCs XORed. The greatest common
divisor over several such pairs is P if a CRC-32 exists for that arrangement
of the bytes, and is small if not. So the search is over arrangements — where
the covered region starts, what order the octets are taken in, the two
reflections and the byte order of the stored CRC — and never over
polynomials.

Then, with the polynomial fixed, the usual four combinations of initial value
and output mask are tried against all six samples.

Expected output: exactly one arrangement with a degree-32 result, polynomial
0x04c11db7, pair-swapped octets, not reflected, stored little-endian, and
init 0 / mask 0 matching 6/6. The region start is reported as both 0 and 2
because every sample begins with the same two octets, 45 00, which XOR away;
only the initial-value step tells them apart, and it picks 0.

Instrument, not QSP code, and Python for the reason scripts/stun-capture.py
gives: it stays out of the Go gate chain. No dependencies.

    python3 scripts/crc-solve.py [testdata/ipsc/ipsc-text-group-cal.pcap]
"""

import itertools
import struct
import sys

CAPTURE = "testdata/ipsc/ipsc-text-group-cal.pcap"
SLL2 = 20            # Linux cooked v2 header
DATAGRAM = 54        # IP Site Connect BPTC datagram
DATA_TYPE_AT = 30
BLOCK_AT = 38
HEADER, RATE12 = 6, 7


def samples(path):
    """Every complete Rate 1/2 user-data stream in the capture."""
    raw = open(path, "rb").read()
    if struct.unpack("<I", raw[20:24])[0] != 276:
        sys.exit(f"{path}: not LINUX_SLL2")
    streams, order, off = {}, [], 24
    while off + 16 <= len(raw):
        cap = struct.unpack("<I", raw[off + 8:off + 12])[0]
        off += 16
        pkt, off = raw[off:off + cap], off + cap
        ip = pkt[SLL2:]
        udp = ip[(ip[0] & 15) * 4:]
        dg = udp[8:struct.unpack(">H", udp[4:6])[0]]
        if len(dg) != DATAGRAM:
            continue
        sid = dg[12:16]
        if sid not in streams:
            streams[sid] = {"header": None, "blocks": []}
            order.append(sid)
        kind = dg[DATA_TYPE_AT] & 15
        if kind == HEADER:
            streams[sid]["header"] = dg[BLOCK_AT:BLOCK_AT + 12]
        elif kind == RATE12:
            streams[sid]["blocks"].append(dg[BLOCK_AT:BLOCK_AT + 12])
    out = []
    for sid in order:
        s = streams[sid]
        if s["header"] and len(s["blocks"]) == s["header"][8] & 0x7F:
            out.append(b"".join(s["blocks"]))
    return out


def deg(a):
    return a.bit_length() - 1


def pmod(a, b):
    while a and deg(a) >= deg(b):
        a ^= b << (deg(a) - deg(b))
    return a


def pgcd(a, b):
    while b:
        a, b = b, pmod(a, b)
    return a


REV8 = [int(f"{i:08b}"[::-1], 2) for i in range(256)]


def rev32(x):
    return int(f"{x:032b}"[::-1], 2)


ORDERS = {
    "wire order": lambda b: b,
    # An odd-length region leaves its last octet unpaired, where it stays.
    "pair-swapped": lambda b: bytes(b[i ^ 1] if i ^ 1 < len(b) else b[i] for i in range(len(b))),
    "reversed": lambda b: b[::-1],
    "32-bit words reversed": lambda b: b"".join(b[i:i + 4][::-1] for i in range(0, len(b), 4)),
}
STORED = {
    "big-endian": lambda c: int.from_bytes(c, "big"),
    "little-endian": lambda c: int.from_bytes(c, "little"),
    "16-bit halves swapped": lambda c: int.from_bytes(c[2:] + c[:2], "big"),
    "octets swapped in pairs": lambda c: int.from_bytes(bytes([c[1], c[0], c[3], c[2]]), "big"),
}


def solve(streams):
    hits = []
    for start in range(0, min(len(s) for s in streams) - 4):
        for oname, order in ORDERS.items():
            for refin, refout in itertools.product((False, True), repeat=2):
                for cname, stored in STORED.items():
                    regs = []
                    for s in streams:
                        m = order(s[start:-4])
                        if refin:
                            m = bytes(REV8[x] for x in m)
                        c = stored(s[-4:])
                        regs.append((len(m), int.from_bytes(m, "big"), rev32(c) if refout else c))
                    g, pairs = 0, 0
                    for (la, ma, ca), (lb, mb, cb) in itertools.combinations(regs, 2):
                        if la == lb and ma != mb:
                            g = pgcd(g, ((ma ^ mb) << 32) ^ ca ^ cb)
                            pairs += 1
                    if pairs >= 2 and deg(g) == 32:
                        hits.append((start, oname, refin, refout, cname, pairs, g))
    return hits


def crc(data, poly, init):
    reg = init
    for b in data:
        reg ^= b << 24
        for _ in range(8):
            reg = ((reg << 1) ^ poly) & 0xFFFFFFFF if reg & 0x80000000 else (reg << 1) & 0xFFFFFFFF
    return reg


def main():
    streams = samples(sys.argv[1] if len(sys.argv) > 1 else CAPTURE)
    print(f"{len(streams)} complete transmissions, lengths {sorted(len(s) for s in streams)}")
    hits = solve(streams)
    for start, oname, refin, refout, cname, pairs, g in hits:
        print(f"start {start}, {oname}, refin={refin}, refout={refout}, CRC {cname}: "
              f"polynomial {g & 0xFFFFFFFF:#010x} over {pairs} pairs")
    if not hits:
        sys.exit("no arrangement gives a degree-32 divisor")
    for start, oname, refin, refout, cname, pairs, g in hits:
        if refin or refout:
            continue  # the init/mask check below is written for the unreflected form
        for init, mask in itertools.product((0, 0xFFFFFFFF), repeat=2):
            ok = sum(crc(ORDERS[oname](s[start:-4]), g & 0xFFFFFFFF, init) ^ mask == STORED[cname](s[-4:])
                     for s in streams)
            print(f"  start {start}, init {init:#010x}, mask {mask:#010x}: {ok}/{len(streams)}")


if __name__ == "__main__":
    main()
