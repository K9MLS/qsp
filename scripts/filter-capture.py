#!/usr/bin/env python3
"""Keep only radio traffic in a pcap, dropping everything else.

**Why this exists.** The three P25 captures were taken without a capture
filter, so alongside the reflector conversation they held 159 mDNS packets
naming household devices and their services, Syncthing local discovery with a
device ID and the operator's public address, Plex discovery, and SSDP with
router UUIDs. Replacing the addresses would have left the device inventory, so
the traffic goes rather than the strings.

**It filters by port, not by string**, so it removes a class of leak rather
than the instances somebody noticed. A packet is kept only if it is on one of
the ports the capture exists to document; ARP, IPv6 and ICMP are kept because
they carry no service inventory and every other capture in testdata has them.

Byte-for-byte faithful to what it keeps: the file header is copied unchanged,
and each kept packet keeps its original record header, so timestamps, capture
lengths and payloads are exactly as they arrived. Nothing is rewritten, which
is what lets the parsing evidence -- 96 polls, 5.01 seconds between them --
still hold afterwards.

    scripts/filter-capture.py in.pcap out.pcap 41000 42010 42020 6074
"""

import struct
import sys

# Link types this understands, with the offset of the IP header in a packet.
# 276 is LINUX_SLL2, which is what `tcpdump -i any` writes on a current kernel
# and what these captures are; 1 is Ethernet and 113 the older cooked form.
LINK_OFFSETS = {1: 14, 113: 16, 276: 20}
# Where the EtherType sits for each, so a non-IPv4 packet is recognised rather
# than parsed as one.
LINK_ETHERTYPE = {1: 12, 113: 14, 276: 0}


def ipv4(link, pkt):
    """Return the IPv4 header of a packet, or None if it is not IPv4."""
    off = LINK_OFFSETS[link]
    et = LINK_ETHERTYPE[link]
    if len(pkt) < off:
        return None
    if struct.unpack(">H", pkt[et:et + 2])[0] != 0x0800:
        return None
    ip = pkt[off:]
    if not ip or (ip[0] >> 4) != 4:
        return None
    return ip


def keep(link, pkt, ports):
    """Keep radio traffic, and anything that is not IPv4."""
    ip = ipv4(link, pkt)
    if ip is None:
        return True
    proto = ip[9]
    if proto == 1:  # ICMP carries no service inventory.
        return True
    if proto not in (6, 17):
        return True
    l4 = ip[(ip[0] & 0xF) * 4:]
    if len(l4) < 4:
        return True
    src, dst = struct.unpack(">HH", l4[:4])
    return src in ports or dst in ports


def main(argv):
    if len(argv) < 4:
        sys.exit(__doc__)
    src, dst, ports = argv[1], argv[2], {int(p) for p in argv[3:]}

    data = open(src, "rb").read()
    magic = data[:4]
    if magic in (b"\xd4\xc3\xb2\xa1", b"\x4d\x3c\xb2\xa1"):
        endian = "<"
    elif magic in (b"\xa1\xb2\xc3\xd4", b"\xa1\xb2\x3c\x4d"):
        endian = ">"
    else:
        sys.exit(f"{src}: not a pcap this understands (magic {magic.hex()})")

    link = struct.unpack(endian + "I", data[20:24])[0]
    if link not in LINK_OFFSETS:
        sys.exit(f"{src}: link type {link} is not one this understands")

    out = bytearray(data[:24])
    off, total, kept = 24, 0, 0
    while off + 16 <= len(data):
        header = data[off:off + 16]
        _, _, caplen, _ = struct.unpack(endian + "IIII", header)
        packet = data[off + 16:off + 16 + caplen]
        off += 16 + caplen
        total += 1
        if keep(link, packet, ports):
            out += header + packet
            kept += 1

    open(dst, "wb").write(bytes(out))
    print(f"{src}: {kept} of {total} packets kept, {total - kept} dropped")


if __name__ == "__main__":
    main(sys.argv)
