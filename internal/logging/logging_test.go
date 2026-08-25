package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"debug", slog.LevelDebug, false},
		{"INFO", slog.LevelInfo, false},
		{"  warn  ", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"verbose", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseLevel(%q): expected error, got none", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseLevel(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseLevelErrorNamesValidValues(t *testing.T) {
	// Constitution §12: errors must explain how to fix them, not merely that
	// something is wrong.
	_, err := ParseLevel("verbose")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"debug", "info", "warn", "error"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message %q does not mention valid value %q", err.Error(), want)
		}
	}
}

func TestParseFormat(t *testing.T) {
	if f, err := ParseFormat("JSON"); err != nil || f != FormatJSON {
		t.Errorf("ParseFormat(JSON) = %v, %v", f, err)
	}
	if f, err := ParseFormat("text"); err != nil || f != FormatText {
		t.Errorf("ParseFormat(text) = %v, %v", f, err)
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("ParseFormat(xml): expected error, got none")
	}
}

func TestJSONHandlerEmitsCanonicalKeys(t *testing.T) {
	var buf bytes.Buffer
	l := Subsystem(New(&buf, Options{Level: slog.LevelInfo, Format: FormatJSON}), "test")
	l.Info("call started",
		PeerID(3121380),
		Callsign("K9MLS"),
		Talkgroup(3100),
		Timeslot(2),
		StreamID(42),
	)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	want := map[string]any{
		KeySubsystem: "test",
		KeyCallsign:  "K9MLS",
		KeyPeerID:    float64(3121380),
		KeyTalkgroup: float64(3100),
		KeyTimeslot:  float64(2),
		KeyStreamID:  float64(42),
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("record[%q] = %v, want %v", k, rec[k], v)
		}
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, Options{Level: slog.LevelWarn, Format: FormatText})
	l.Info("should not appear")
	if buf.Len() != 0 {
		t.Errorf("info record emitted at warn level: %q", buf.String())
	}
	l.Warn("should appear")
	if buf.Len() == 0 {
		t.Error("warn record suppressed at warn level")
	}
}

func TestRedactedNeverEmitsValue(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, Options{Level: slog.LevelInfo, Format: FormatJSON})
	l.Info("credential loaded", Redacted("private_key"))

	out := buf.String()
	if !strings.Contains(out, RedactedPlaceholder) {
		t.Errorf("expected redaction placeholder in %q", out)
	}
}
