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

// TestTheDropSummaryUsesAnExistingStyle checks the panel did not grow its own
// vocabulary for something the stylesheet already says.
//
// The first version of this feature introduced .drops, .drop, .drop__at and
// .drop__reason for a list of journal lines that took a third of the panel and
// appeared after every restart. It was replaced by one sentence in
// .inline-note, which the panel already used for the "no voice frames yet"
// note. **Four new classes to say something an existing class already said is
// how a stylesheet stops being a system**, and this project has already lost an
// evening to two rules sharing the name .hint.
func TestTheDropSummaryUsesAnExistingStyle(t *testing.T) {
	css := readFile(t, "static/console.css")
	for _, gone := range []string{".drops", ".drop__at", ".drop__reason"} {
		if strings.Contains(css, gone) {
			t.Errorf("%s is still in the stylesheet; the drop list was replaced by a note", gone)
		}
	}
	if !strings.Contains(css, ".inline-note") {
		t.Error("the style the drop summary uses is not defined")
	}
}

// TestTheDropSummaryOnlyAppearsWhenSomethingWasIgnored is the shape of the fix.
//
// Every drop was listed before, including the answered ones — a stale peer
// told to log in again, which is the protocol working. Three of those filled a
// third of the panel while IGNORED read 0 and had nothing to explain.
func TestTheDropSummaryOnlyAppearsWhenSomethingWasIgnored(t *testing.T) {
	js := stripComments(readFile(t, "static/console.js"))
	if !strings.Contains(js, "if (ignored > 0)") {
		t.Error("the drop summary is not gated on the counter it explains")
	}
	if !strings.Contains(js, "!d.answered") {
		t.Error("the summary counts answered drops, which are the protocol working")
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
