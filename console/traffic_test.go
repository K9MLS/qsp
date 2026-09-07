package console_test

import (
	"os"
	"strings"
	"testing"
)

// TestTheTrafficPanelDrawsWhatItSaysItDraws is the test that would have caught
// this.
//
// The traffic panel's own comment said "the reasons are below" while
// `recent_drops` was rendered nowhere at all — the payload has carried the
// timestamp, source, reason and answered flag since the counters were added,
// and the console has never drawn one of them. An operator asking "what are
// these 25?" had a number with no time on it and no cause.
//
// **This is the recurring shape**: something built, wired, exported and never
// called, for the thirteenth recorded time. It survived because nothing here
// compares what the API sends with what the page reads.
func TestTheTrafficPanelDrawsWhatItSaysItDraws(t *testing.T) {
	// **Comments are stripped first.** The first version of this test searched
	// the whole file, and passed with the rendering deliberately deleted,
	// because the comment explaining the rendering still mentioned every field
	// by name. A test that a comment can satisfy checks the documentation.
	js := stripComments(readFile(t, "static/console.js"))

	// Fields of Traffic that the panel promises to show. Each is checked by
	// the name the JSON uses, because that is the string the page has to read.
	for _, field := range []string{
		"datagrams_in",
		"frames_accepted",
		"collisions",
		"ignored",
		"recent_drops",
	} {
		if !strings.Contains(js, field) {
			t.Errorf("the traffic panel never reads %q, which the API sends", field)
		}
	}

	// A drop note is useless without its timestamp: a cumulative counter with
	// no time axis reads as "this morning" whatever hour produced it.
	for _, part := range []string{".at", ".reason", ".answered"} {
		if !strings.Contains(js, "d"+part) {
			t.Errorf("a drop note is rendered without its %s", strings.TrimPrefix(part, "."))
		}
	}
}

// stripComments removes /* */ and // comments so a claim in prose cannot stand
// in for the code that makes it true.
//
// It is deliberately crude: it does not understand strings or regular
// expressions, so a // inside a string literal would truncate that line. That
// is acceptable here because the result is only searched for field names, and
// a crude stripper that removes too much can only make this test stricter.
func stripComments(src string) string {
	var out strings.Builder
	for {
		block := strings.Index(src, "/*")
		line := strings.Index(src, "//")
		switch {
		case block < 0 && line < 0:
			out.WriteString(src)
			return out.String()
		case block >= 0 && (line < 0 || block < line):
			out.WriteString(src[:block])
			end := strings.Index(src[block:], "*/")
			if end < 0 {
				return out.String()
			}
			src = src[block+end+2:]
		default:
			out.WriteString(src[:line])
			end := strings.IndexByte(src[line:], '\n')
			if end < 0 {
				return out.String()
			}
			src = src[line+end:]
		}
	}
}

// TestDropNotesAreStyled checks the classes the panel emits have rules, since a
// class with no rule renders as unstyled text and looks like a bug rather than
// a list.
func TestDropNotesAreStyled(t *testing.T) {
	css := readFile(t, "static/console.css")
	for _, class := range []string{".drops", ".drop", ".drop__at", ".drop__reason"} {
		if !strings.Contains(css, class) {
			t.Errorf("the console emits %s and the stylesheet has no rule for it", class)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return string(b)
}
