package console_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryPageThatSavesTheConfigurationSaysWhatItWasGiven.
//
// A page posts the whole configuration having changed part of it. Sent with
// what the page was given, the server saves only that part; sent without, it
// replaces everything, and undoes whatever changed elsewhere while the page
// was open. One page forgetting is one page that silently removes a link
// somebody accepted, so every script that posts to /api/config is read.
//
// **The history page is the one that must not**: restoring a version means
// "make it exactly this".
//
// Break it: take `base: loaded` out of any page's save.
func TestEveryPageThatSavesTheConfigurationSaysWhatItWasGiven(t *testing.T) {
	scripts, err := filepath.Glob("static/*.js")
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no scripts found: %v", err)
	}
	// A POST to /api/config, up to the end of the object it sends.
	save := regexp.MustCompile(`(?s)fetch\("/api/config",\s*\{\s*method:\s*"POST".*?body:\s*JSON\.stringify\((.*?)\)\s*\}\)`)

	saving := 0
	for _, path := range scripts {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range save.FindAllStringSubmatch(stripComments(string(raw)), -1) {
			saving++
			sendsBase := strings.Contains(m[1], "base: loaded")
			restores := filepath.Base(path) == "history.js"
			switch {
			case restores && sendsBase:
				t.Errorf("%s sends what it was given: a restore would be merged and not restore", path)
			case !restores && !sendsBase:
				t.Errorf("%s saves the configuration without saying what it was given, "+
					"so it undoes whatever changed elsewhere while it was open", path)
			}
		}
	}
	if saving < 6 {
		t.Fatalf("found %d saves of the configuration, want the six pages; this check has gone blind", saving)
	}
}
