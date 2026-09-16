#!/usr/bin/env bash
#
# Run every gate CI runs, locally. Use before opening a pull request.

set -euo pipefail

cd "$(dirname "$0")/.."

fail=0
step() {
  printf '\n\033[1m==> %s\033[0m\n' "$1"
}

step "gofmt"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "Not gofmt-formatted:"
  echo "$unformatted"
  echo "Run: gofmt -w ."
  fail=1
else
  echo "ok"
fi

step "go build ./..."
go build ./... || fail=1

step "go vet ./..."
go vet ./... || fail=1

step "go test ./..."
go test ./... || fail=1

step "go test -race ./..."
go test -race ./... || fail=1

step "documentation accuracy"
go test ./cmd/qsp/ -run 'TestDocumented|TestEmptiness|TestNothingClaims|TestUnbuilt' || fail=1

# ADR-0009 keeps Opus behind the zello build tag, so nothing above compiles
# qsp-zello. Skipped only when libopus headers are absent, and said so.
step "zello-tagged connector"
if pkg-config --exists opus 2>/dev/null; then
  CGO_ENABLED=1 go vet -tags zello ./cmd/qsp-zello/ ./internal/opus/ ./internal/zellobridge/ || fail=1
  CGO_ENABLED=1 go test -race -tags zello ./cmd/qsp-zello/ ./internal/opus/ ./internal/zellobridge/ || fail=1
else
  echo "libopus headers not installed; skipping."
  echo "Install with: sudo dnf install opus-devel   (Debian/Ubuntu: libopus-dev)"
fi

step "staticcheck ./..."
if command -v staticcheck >/dev/null 2>&1; then
  staticcheck ./... || fail=1
else
  echo "staticcheck not installed; skipping."
  echo "Install with: go install honnef.co/go/tools/cmd/staticcheck@latest"
fi

if [ "$fail" -ne 0 ]; then
  printf '\n\033[31mSome checks failed.\033[0m\n'
  exit 1
fi

printf '\n\033[32mAll checks passed.\033[0m\n'
