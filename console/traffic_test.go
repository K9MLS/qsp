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

	// **The intent rather than one spelling of it.** This matched the literal
	// `if (ignored > 0)` and broke the moment the condition gained a second
	// term — while the property it exists to protect was untouched. A gate
	// that fails on a rewrite it should not care about is a gate somebody
	// edits out.
	//
	// The property: the summary is drawn only when the counter it explains is
	// non-zero. That now runs through `showDrops`, which is itself derived
	// from the counter, so both halves are checked.
	if !strings.Contains(js, "showDrops = ignored > 0") {
		t.Error("the drop summary's condition is not derived from the counter it " +
			"explains")
	}
	if !strings.Contains(js, "if (ignored > 0 && showDrops)") {
		t.Error("the summary is not gated on both the counter and the note's own " +
			"lifetime")
	}
	if !strings.Contains(js, "!d.answered") {
		t.Error("the summary counts answered drops, which are the protocol working")
	}
}

// TestTheAmberCounterAndItsReasonEndTogether is the property the dissolve must
// not break.
//
// The note exists because a permanently amber IGNORED read as a fault and had
// nothing to explain it. **Dismissing the explanation while leaving the
// counter amber would restore exactly that**, so the counter's colour is
// decided by the same value that decides whether the note is drawn.
func TestTheAmberCounterAndItsReasonEndTogether(t *testing.T) {
	js := stripComments(readFile(t, "static/console.js"))

	if !strings.Contains(js, `ignored > 0 && showDrops ? "metric--warn"`) {
		t.Error("the ignored counter's amber is not tied to the note being on " +
			"screen; an amber number outliving its reason is the fault the note " +
			"was added to fix")
	}
	// And the note is removed rather than hidden, so a re-render does not find
	// a stale one to animate again.
	if !strings.Contains(js, "removeChild(note)") {
		t.Error("the note is hidden rather than removed")
	}
	// The dismissal re-renders, so the colour changes in the same moment the
	// reason goes rather than on whatever refresh happens next.
	if !strings.Contains(js, "renderTraffic(lastTraffic)") {
		t.Error("dismissing the note does not re-render, so the counter keeps " +
			"its amber until something else happens")
	}
}

// TestTheDissolveHasAReducedMotionPathThatStillRemovesTheNote.
//
// Somebody who asked for less movement still wants the note to go; they do not
// want it to slide. `animation: none` alone would have left it on screen for
// ever on exactly the machines that asked for less.
func TestTheDissolveHasAReducedMotionPathThatStillRemovesTheNote(t *testing.T) {
	css := readFile(t, "static/console.css")

	if !strings.Contains(css, "@keyframes note-dissolve") {
		t.Fatal("the dissolve animation is not defined")
	}
	if !strings.Contains(css, ".inline-note--drops") {
		t.Error("the drop advisory has no style of its own, so the height " +
			"collapse has nothing to clip against")
	}

	reduced := css[strings.LastIndex(css, "prefers-reduced-motion"):]
	_ = reduced
	if !strings.Contains(css, "prefers-reduced-motion") {
		t.Fatal("the stylesheet has no reduced-motion guard")
	}

	// The guard for this animation must do more than switch it off, or the
	// note never leaves.
	// **The last occurrence, not the first.** The stylesheet has several
	// reduced-motion guards and the rule for this animation appears twice —
	// once to define it and once inside the guard, which comes later. Indexing
	// from the front found the table's guard instead, where there is no
	// note to remove, so the check would have passed or failed for reasons
	// nothing to do with the dissolve.
	guard := lastSectionAround(css, ".inline-note.is-dissolving")
	if guard == "" {
		t.Fatal("the dissolve has no reduced-motion guard of its own")
	}
	if !strings.Contains(guard, "display: none") {
		t.Error("reduced motion switches the animation off without removing the " +
			"note, so it would stay on screen for ever")
	}

	// And a JavaScript timer rather than animationend, because an animation
	// that never runs never ends.
	js := stripComments(readFile(t, "static/console.js"))
	if strings.Contains(js, "animationend") {
		t.Error("the dissolve waits for animationend; with reduced motion the " +
			"animation never runs, so the element would never be removed")
	}
}

// lastSectionAround returns a window of the stylesheet around the final
// occurrence of a marker, or "".
func lastSectionAround(css, marker string) string {
	at := strings.LastIndex(css, marker)
	if at < 0 {
		return ""
	}
	end := at + len(marker) + 200
	if end > len(css) {
		end = len(css)
	}
	return css[at:end]
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return string(b)
}
