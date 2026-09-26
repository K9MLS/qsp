#!/usr/bin/env python3
"""Record what a Cisco router's serial tunnel sends, and nothing else.

This is ADR-0060 phase 1's instrument. The router carries a Quantar's V.24
frames over TCP with STUN, and no published specification describes the
Motorola framing above HDLC — so knowledge of it comes from captures taken
here, exactly as it did for IPSC (ADR-0029). This script is how the bytes get
onto disk.

**It records and does not answer.** Phase 2 answers the keepalive, and it
cannot be written yet: STUN basic wraps each frame in a header this project has
never seen, so any reply would be a guess about framing we do not hold. A
listener that echoed bytes back would look like progress and would be
indistinguishable from one that had learned something. Record first; the reply
is written from the capture, not before it.

Instrument, not QSP code. It lives in scripts/ and is deliberately Python so it
stays out of the Go gate chain, following scripts/scrub-history.py. Its
correctness is judged by whether the bytes land on disk and match what tcpdump
saw beside it, which is the point of running both.

Run it on the test server, never production, and take a pcap alongside it — the
pcap is the fixture format testdata/ uses, and two recordings of one event that
disagree is a fact worth having:

    sudo tcpdump -i any -s 0 -w /tmp/quantar-$(date +%Y%m%d-%H%M%S).pcap \
        'tcp port 1994' &
    python3 scripts/stun-capture.py --out-dir /tmp/quantar

Then, on the router, point the tunnel here and bring the port up:

    stun route all tcp <this host>

Each accepted connection writes two files: a .bin of the raw stream exactly as
it arrived, byte for byte with nothing added, and a .log of frame arrival times
and sizes. The .bin is the fixture; the .log is for reading gaps, because a
keepalive's period is a fact about the far end and it is invisible in a hex
dump.
"""

from __future__ import annotations

import argparse
import datetime
import os
import selectors
import signal
import socket
import sys
import threading
import time

# The DVSwitch Quantar_Bridge .ini names 1994 and Cisco's serial tunnel has used
# it since it carried SDLC. It is an interface description, not an
# implementation detail borrowed from anyone (see ADR-0029).
DEFAULT_PORT = 1994

# One read per frame where possible: the largest thing the router can send is
# bounded by the 2104-byte MTU IOS set on the interface itself, and a generous
# buffer keeps one HDLC frame in one read rather than splitting it across two
# and inventing a boundary that was never on the wire.
READ_SIZE = 8192


def utc_stamp() -> str:
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d-%H%M%SZ")


def iso_now() -> str:
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="milliseconds")


class Recorder:
    """Writes one connection's stream to a .bin and its timing to a .log."""

    def __init__(self, out_dir: str, peer: tuple[str, int]) -> None:
        host, port = peer
        base = os.path.join(out_dir, f"stun-{utc_stamp()}-{host.replace(':', '_')}-{port}")
        self.bin_path = base + ".bin"
        self.log_path = base + ".log"
        # Buffering is off on the byte stream: a capture that is lost because
        # the process was interrupted before a flush is a capture that did not
        # happen, and this script exists to be interrupted by hand.
        self._bin = open(self.bin_path, "wb", buffering=0)
        self._log = open(self.log_path, "w", encoding="utf-8", buffering=1)
        self.total = 0
        self.frames = 0
        self._first: float | None = None
        self._last: float | None = None
        self._log.write(f"# stun-capture, peer {host}:{port}, opened {iso_now()}\n")
        self._log.write("# elapsed_ms\tdelta_ms\tbytes\trunning_total\n")

    def record(self, chunk: bytes, now: float) -> None:
        self._bin.write(chunk)
        if self._first is None:
            self._first = now
        elapsed = (now - self._first) * 1000.0
        delta = 0.0 if self._last is None else (now - self._last) * 1000.0
        self._last = now
        self.total += len(chunk)
        self.frames += 1
        self._log.write(f"{elapsed:.1f}\t{delta:.1f}\t{len(chunk)}\t{self.total}\n")

    def close(self) -> None:
        self._log.write(
            f"# closed {iso_now()}: {self.frames} reads, {self.total} bytes\n"
        )
        for handle in (self._bin, self._log):
            try:
                handle.close()
            except OSError:
                pass


def serve(bind: str, port: int, out_dir: str, stop: threading.Event) -> int:
    os.makedirs(out_dir, exist_ok=True)

    listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        listener.bind((bind, port))
    except OSError as err:
        print(f"cannot bind {bind}:{port}: {err}", file=sys.stderr)
        return 1
    listener.listen(4)
    listener.setblocking(False)

    sel = selectors.DefaultSelector()
    sel.register(listener, selectors.EVENT_READ, None)
    recorders: dict[socket.socket, Recorder] = {}

    print(f"listening on {bind}:{port}, writing to {out_dir}")
    print("nothing is sent back — this records only (see ADR-0060 phase 1)")
    print("waiting for the router; Ctrl-C to stop")

    try:
        while not stop.is_set():
            # A timeout rather than a blocking select, so Ctrl-C is answered
            # promptly even on a link that has gone quiet — which is the normal
            # state of a link whose far end is not talking yet.
            for key, _ in sel.select(timeout=0.5):
                sock = key.fileobj
                if sock is listener:
                    conn, peer = listener.accept()
                    conn.setblocking(False)
                    rec = Recorder(out_dir, peer)
                    recorders[conn] = rec
                    sel.register(conn, selectors.EVENT_READ, rec)
                    print(f"connection from {peer[0]}:{peer[1]} -> {rec.bin_path}")
                    continue

                rec = key.data
                try:
                    chunk = sock.recv(READ_SIZE)
                except ConnectionResetError:
                    chunk = b""
                except BlockingIOError:
                    continue
                if not chunk:
                    print(
                        f"closed: {rec.frames} reads, {rec.total} bytes -> {rec.bin_path}"
                    )
                    sel.unregister(sock)
                    sock.close()
                    rec.close()
                    recorders.pop(sock, None)
                    continue

                rec.record(chunk, time.monotonic())
                # A running count on stdout is the difference between "it is
                # working" and "it is running": a silent process and a process
                # receiving nothing look identical otherwise, which is the
                # failure this whole rig exists to tell apart.
                print(
                    f"\r{rec.total} bytes in {rec.frames} reads", end="", flush=True
                )
    finally:
        print()
        for sock, rec in list(recorders.items()):
            try:
                sel.unregister(sock)
            except (KeyError, ValueError):
                pass
            sock.close()
            rec.close()
            print(f"wrote {rec.total} bytes to {rec.bin_path}")
        sel.close()
        listener.close()

    return 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Record a Cisco STUN serial tunnel. Records only; sends nothing."
    )
    parser.add_argument(
        "--bind",
        default="0.0.0.0",
        help="address to listen on (default: all interfaces)",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=DEFAULT_PORT,
        help=f"TCP port the router's stun route points at (default: {DEFAULT_PORT})",
    )
    parser.add_argument(
        "--out-dir",
        default="stun-captures",
        help="directory for the .bin and .log files (default: ./stun-captures)",
    )
    args = parser.parse_args(argv)

    stop = threading.Event()

    def handle(signum: int, _frame: object) -> None:
        del signum
        stop.set()

    signal.signal(signal.SIGINT, handle)
    signal.signal(signal.SIGTERM, handle)

    return serve(args.bind, args.port, args.out_dir, stop)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
