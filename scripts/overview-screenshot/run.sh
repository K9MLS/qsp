#!/bin/sh
# Regenerate docs/images/overview.png: a QSP server on this machine, four
# invented hotspots talking through it, and a photograph of its Overview.
# Run from the repository root. Needs Go, Python 3 with playwright and
# Pillow, and a Chromium for playwright.
set -eu

work=$(mktemp -d)
trap 'kill $server $peers 2>/dev/null || true; rm -rf "$work"' EXIT

go build -o "$work/qsp" ./cmd/qsp
"$work/qsp" -print-config > "$work/base.json"
printf 'demo-password\n' > "$work/peer.pass"
chmod 600 "$work/peer.pass"
python3 - "$work" <<'PY'
import json, sys
work = sys.argv[1]
c = json.load(open(work + "/base.json"))
c["server"]["listen_address"] = "127.0.0.1:18090"
c["database"]["dsn"] = work + "/qsp.db"
c["dmr"].update(enabled=True, forwarding=True, listen_address="127.0.0.1:62931",
                access={}, password_file=work + "/peer.pass")
json.dump(c, open(work + "/qsp.json", "w"), indent=1)
PY

"$work/qsp" -config "$work/qsp.json" > "$work/qsp.log" 2>&1 &
server=$!
sleep 2
go run scripts/overview-screenshot/demo_peers.go -server 127.0.0.1:62931 \
  -password-file "$work/peer.pass" > "$work/peers.log" 2>&1 &
peers=$!

# The overs before the last take about half a minute.
until grep -q '^ready' "$work/peers.log"; do
  kill -0 $peers 2>/dev/null || { cat "$work/peers.log" "$work/qsp.log"; exit 1; }
  sleep 1
done
sleep 3
mkdir -p docs/images
python3 scripts/overview-screenshot/shoot.py http://127.0.0.1:18090/ docs/images/overview.png
