#!/usr/bin/env bash
#
# Run two QSP instances on this machine, peered to each other over OpenBridge.
#
# Every upstream path in this project is code that has never met a far end.
# OpenBridge was written from the specification, outbound peer mode from
# ADR-0024, and both have been exercised only by tests that supply their own
# other side. This runs a real one.
#
# It is not a soak and not a substitute for the air test. It answers one
# question the test suite cannot: does a frame leave one instance and arrive at
# the other, over a real socket, with a real passphrase.
#
# Nothing here touches the production instance. Both processes bind 127.0.0.1
# on ports nothing else uses, and both write to /tmp.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
run="/tmp/qsp-pair"

case "${1:-start}" in
stop)
	pkill -f "qsp-pair/(alpha|bravo).json" 2>/dev/null || true
	echo "stopped"
	exit 0
	;;
clean)
	pkill -f "qsp-pair/(alpha|bravo).json" 2>/dev/null || true
	rm -rf "$run"
	echo "removed $run"
	exit 0
	;;
start) ;;
*)
	echo "usage: $0 [start|stop|clean]" >&2
	exit 2
	;;
esac

mkdir -p "$run"

# **The passphrase is shared and the peer passwords are not.** OpenBridge
# authenticates both ends against one secret; the DMR listeners are separate
# networks that happen to be on one machine.
[ -f "$run/pair.pass" ] || printf 'pair-test-passphrase' >"$run/pair.pass"
[ -f "$run/alpha.peer.pass" ] || printf 'alpha-peer' >"$run/alpha.peer.pass"
[ -f "$run/bravo.peer.pass" ] || printf 'bravo-peer' >"$run/bravo.peer.pass"
chmod 600 "$run"/*.pass

cp "$root/deploy/pair/alpha.json" "$root/deploy/pair/bravo.json" "$run/"

echo "building"
go build -o "$run/qsp" "$root/cmd/qsp"

start() {
	local name="$1"
	"$run/qsp" -config "$run/$name.json" >"$run/$name.log" 2>&1 &
	echo "$name: pid $!  log $run/$name.log"
}

start alpha
start bravo
sleep 2

echo
echo "alpha console  http://127.0.0.1:8091   DMR 127.0.0.1:62041"
echo "bravo console  http://127.0.0.1:8092   DMR 127.0.0.1:62042"
echo
echo "watch both:    tail -f $run/alpha.log $run/bravo.log"
echo "link health:   curl -s http://127.0.0.1:8091/healthz | grep -o 'upstream[^,]*'"
echo "stop:          $0 stop"
