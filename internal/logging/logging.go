// Package logging configures structured logging for QSP and provides the
// canonical attribute keys used across every subsystem.
//
// Consistent keys matter operationally: an operator grepping for a callsign or
// a peer ID must find every relevant line regardless of which subsystem emitted
// it. Constructing attributes ad hoc produces "peer", "peer_id", and "peerID"
// in the same log stream, so the helpers below are the only supported way to
// attach these fields.
//
// Secrets are never logged. See Redacted for the mechanism used when a value
// must be referenced but not disclosed.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Canonical attribute keys. Subsystems must use the helper constructors rather
// than these constants directly, but they are exported so that log consumers
// (dashboards, log shippers, tests) can reference them without string literals.
const (
	KeySubsystem = "subsystem"
	KeyPeerID    = "peer_id"
	// KeySourceRadio names the radio that keyed up, which survives relaying
	// while a peer ID does not. internal/ipsclink already uses this key.
	KeySourceRadio = "source"
	KeyCallsign    = "callsign"
	KeyTalkgroup   = "talkgroup"
	KeyTimeslot    = "timeslot"
	KeyRequestID   = "request_id"
	KeyStreamID    = "stream_id"
	KeyComponent   = "component"
)

// RedactedPlaceholder replaces any value that must not appear in logs.
const RedactedPlaceholder = "[REDACTED]"

// Format selects the encoding of log records.
type Format string

const (
	// FormatText is human-readable and intended for interactive use.
	FormatText Format = "text"
	// FormatJSON is machine-readable and intended for production and log shippers.
	FormatJSON Format = "json"
)

// ParseFormat converts a configuration string into a Format.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "text":
		return FormatText, nil
	case "json":
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("unknown log format %q: valid values are \"text\" and \"json\"", s)
	}
}

// ParseLevel converts a configuration string into a slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q: valid values are \"debug\", \"info\", \"warn\" and \"error\"", s)
	}
}

// Options configures a logger.
type Options struct {
	Level  slog.Level
	Format Format
	// AddSource includes file and line information. Useful during development,
	// costly in hot paths, so it is off by default.
	AddSource bool
}

// New builds a slog.Logger writing to w.
func New(w io.Writer, opts Options) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{
		Level:     opts.Level,
		AddSource: opts.AddSource,
	}

	var handler slog.Handler
	if opts.Format == FormatJSON {
		handler = slog.NewJSONHandler(w, handlerOpts)
	} else {
		handler = slog.NewTextHandler(w, handlerOpts)
	}
	return slog.New(handler)
}

// Discard returns a logger that writes nothing.
//
// It exists so that constructors can accept a nil logger without every one of
// them repeating a nil check, and without a missing logger becoming a panic in
// production.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// discardWriter throws data away.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// Subsystem returns a logger tagged with a subsystem name. Every package that
// logs should derive its logger this way exactly once, at construction.
//
// A nil logger yields a discard logger rather than panicking. Constructors
// therefore need no nil checks, and a caller that omits a logger loses its logs
// rather than the process.
func Subsystem(l *slog.Logger, name string) *slog.Logger {
	if l == nil {
		l = Discard()
	}
	return l.With(slog.String(KeySubsystem, name))
}

// PeerID returns the canonical attribute for a peer: the repeater or hotspot
// QSP is exchanging frames with.
//
// **Not for a radio.** An earlier version of this doc said "a DMR/P25 radio or
// peer ID", and that conflation put two different identifiers under one label:
// `call started` logged the radio that keyed up as peer_id while `relaying
// transmission` logged the peer it came from as peer_id, for the same stream.
// On the operator's network those numbers are 3132910 and 3132913, four apart,
// which is the worst possible case for noticing.
//
// A log field's name is a claim, the same way a counter's is. Use SourceRadio
// for the radio that transmitted.
func PeerID(id uint32) slog.Attr { return slog.Uint64(KeyPeerID, uint64(id)) }

// SourceRadio returns the canonical attribute for the radio that keyed up.
//
// **It survives relaying and a peer ID does not**, which is exactly why they
// need different names: the same transmission reaches four peers and carries
// one source throughout. internal/ipsclink already logs it as "source", so
// this is that convention rather than a fourth name for the same thing.
func SourceRadio(id uint32) slog.Attr { return slog.Uint64(KeySourceRadio, uint64(id)) }

// Callsign returns the canonical attribute for an amateur callsign.
func Callsign(cs string) slog.Attr { return slog.String(KeyCallsign, cs) }

// Talkgroup returns the canonical attribute for a talkgroup ID.
func Talkgroup(tg uint32) slog.Attr { return slog.Uint64(KeyTalkgroup, uint64(tg)) }

// Timeslot returns the canonical attribute for a DMR timeslot.
func Timeslot(ts int) slog.Attr { return slog.Int(KeyTimeslot, ts) }

// RequestID returns the canonical attribute for an HTTP request correlation ID.
func RequestID(id string) slog.Attr { return slog.String(KeyRequestID, id) }

// StreamID returns the canonical attribute for a voice stream ID.
func StreamID(id uint32) slog.Attr { return slog.Uint64(KeyStreamID, uint64(id)) }

// Redacted returns an attribute whose value is replaced by a placeholder.
//
// Use this when the presence of a credential is operationally meaningful but
// its value must never be written. Never pass a secret to any other attribute
// constructor.
func Redacted(key string) slog.Attr { return slog.String(key, RedactedPlaceholder) }
