# ADR-0001: Go for the core

**Status:** Accepted

## Context

QSP must install in under ten minutes on a club's Ubuntu server, run on a
Raspberry Pi, and handle many concurrent UDP peers. The candidate languages were
Go, Python, and Rust.

Python has the largest ham contributor base and every reference implementation
in this space is written in it. But shipping Python means the operator installs
Python, encounters dependency drift, and we wrap the result in Docker to hide
it — which is precisely the complexity QSP exists to remove.

Rust is an excellent technical fit with a much smaller ham contributor pool and
a materially slower path to a working prototype.

## Decision

Go, minimum version 1.22.

`go:embed` compiles the entire console into the binary: no Node, no build step,
no separate asset deployment. Cross-compilation to amd64, arm64 and armv7 from
one machine covers every target. Goroutines map cleanly onto per-peer UDP
handling.

## Consequences

- One file to download and run. No runtime, no interpreter, no dependency tree.
- **Accepted cost:** fewer drive-by contributions from the Python-heavy ham
  developer community. Judged worth it — a binary that just works buys more
  goodwill than theoretical contributor reach.
- The pure-Go constraint rules out cgo SQLite drivers (ADR-0005) and cgo audio
  codecs in the core (ADR-0009).
