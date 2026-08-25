// Package hbp implements the DMR Homebrew Repeater Protocol.
//
// # Provenance
//
// Every message in this package was derived from real traffic captured in
// testdata/hbp/, not from any reference implementation. Per
// docs/adr/ADR-0008-protocol-licensing.md, no HBP implementation source has
// been read, ported or transliterated. Where this package makes a claim about
// the protocol, a fixture demonstrates it.
//
// The consequence is that this package implements exactly what was observed and
// refuses to guess at the rest. Message kinds that are known to exist but were
// not captured are recognised and rejected with ErrNotCaptured rather than
// being parsed speculatively. That is deliberate: a wrong guess about a close
// or disconnect message would produce a peer that silently misbehaves, which is
// worse than one that plainly cannot handle it yet.
//
// # Two dialects
//
// The captures contain two independent implementations talking HBP to each
// other, and they differ:
//
//   - A master link (DMRGateway to BrandMeister) uses RPTC for configuration
//     and RPTPING/MSTPONG for keepalives.
//   - A local link (MMDVMHost to DMRGateway) uses DMRC, a shorter configuration
//     message that omits the location fields, and DMRP, a bare four-byte
//     keepalive carrying no repeater ID at all.
//
// Both are supported. A parser that assumed one shape would fail against half
// of a normal Pi-Star installation.
//
// # Parsing is not interpretation
//
// Parse returns what the bytes say. It does not decide what they mean, because
// in one important case the bytes are genuinely ambiguous: RPTACK is ten bytes
// carrying a four-byte payload both when it delivers an authentication salt and
// when it acknowledges a configuration. Nothing in the packet distinguishes
// them; only the connection's state does. Ack therefore exposes the raw payload
// and offers both interpretations, and it is the peer state machine's job to
// choose. See Ack.
package hbp
