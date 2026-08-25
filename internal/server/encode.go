package server

import (
	"bytes"
	"encoding/json"

	"github.com/k9mls/qsp/internal/events"
)

// marshalEvent encodes an event for the SSE data field.
//
// Server-Sent Events are newline-delimited, so an encoded payload containing a
// literal newline would corrupt the frame. Go's encoder escapes newlines inside
// strings, and the trailing newline it appends is trimmed here.
func marshalEvent(ev events.Event) ([]byte, error) {
	return marshalCompact(ev)
}

// resync is the control payload telling a client its view may be incomplete.
type resync struct {
	Reason string `json:"reason"`
	Action string `json:"action"`
}

func marshalResync(reason string) ([]byte, error) {
	return marshalCompact(resync{
		Reason: reason,
		Action: "discard local state and fetch a fresh snapshot",
	})
}

func marshalCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
