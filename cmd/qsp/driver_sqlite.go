package main

// Registering a database driver is a side effect of importing it, which makes
// it the kind of thing that arrives by accident inside some unrelated
// dependency and is then impossible to find. It gets its own file here so the
// act is deliberate, greppable, and attached to its reasoning.
//
// modernc.org/sqlite is SQLite transpiled to Go rather than bound to it through
// cgo. ADR-0005 chose it for that reason: QSP cross-compiles to linux/arm and
// arm64 with CGO_ENABLED=0, and a cgo-linked driver would end that. ADR-0009
// depends on the same property.
//
// This is the binary's decision, not the library's. internal/database resolves
// whatever driver name the configuration asks for through database/sql and
// stays ignorant of which ones exist, so a build that wants a different
// database changes this file and nothing else.
//
// It also brings the project's first dependency, and nine transitive ones with
// it. The claim worth defending was never "no dependencies" for its own sake —
// it was a binary that builds anywhere, needs no C toolchain, and has a small
// enough surface to audit. That survives.

import _ "modernc.org/sqlite"
