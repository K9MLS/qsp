package console_test

import (
	"regexp"
	"strings"
	"testing"
)

// TestAnAdministratorCanChangeTheirOwnPasswordFromThePage.
//
// The only way to a new password was Reset, which makes one up, so an
// administrator who wanted to choose their own could not. Both halves are
// held here: the boxes on the page and the script that reads them.
//
// Break it: remove a box from admin.html, change one to type="text", or
// build the request address from a box's value in admin.js.
func TestAnAdministratorCanChangeTheirOwnPasswordFromThePage(t *testing.T) {
	html := readFile(t, "static/admin.html")
	js := stripComments(readFile(t, "static/admin.js"))

	for _, c := range []struct{ id, autocomplete, why string }{
		{"password-current", "current-password",
			"a console left open could be made somebody else's without it"},
		{"password-new", "new-password", "there is nowhere to type the new one"},
		{"password-again", "new-password",
			"a slip in a box that shows dots is a password nobody knows"},
	} {
		tag := regexp.MustCompile(`(?s)<input[^>]*id="` + c.id + `"[^>]*>`).FindString(html)
		if tag == "" {
			t.Errorf("no %q on the page: %s", c.id, c.why)
			continue
		}
		if !strings.Contains(tag, `type="password"`) {
			t.Errorf("%q shows what is typed into it: %s", c.id, tag)
		}
		// So a password manager fills the right box and offers to save the
		// new one.
		if !strings.Contains(tag, `autocomplete="`+c.autocomplete+`"`) {
			t.Errorf("%q is not marked %s for a password manager", c.id, c.autocomplete)
		}
		if !strings.Contains(js, `"`+c.id+`"`) {
			t.Errorf("%q is on the page and read by nothing", c.id)
		}
	}
	for _, id := range []string{"password-error", "password-done"} {
		if !strings.Contains(html, `id="`+id+`"`) || !strings.Contains(js, `"`+id+`"`) {
			t.Errorf("the answer has nowhere of its own to be shown: %q", id)
		}
	}

	if !strings.Contains(js, `fetch("/api/account/password"`) {
		t.Fatal("nothing sends the form; the address must be a fixed string, " +
			"never one built from what was typed")
	}
	// A password in an address is a password in a log.
	if regexp.MustCompile(`/api/account/password["']?\s*\+`).MatchString(js) {
		t.Error("the request address has something added to it")
	}
}
