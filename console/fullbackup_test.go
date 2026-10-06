package console_test

import (
	"regexp"
	"strings"
	"testing"
)

// TestTheFullBackupCanBeMadeAndRestoredFromThePage.
//
// **The server could do both and no page asked it to.** The encrypted backup,
// the one that rebuilds a server, was reachable only by writing the request
// by hand, while the page offered the backup with no passwords in it under
// the plain name "backup". Both halves are held here, because building the
// named half and forgetting the called half is this project's recurring
// fault, and so is a control on a page that nothing reads.
//
// Break it: remove either fetch from admin.js, or either control from the
// page.
func TestTheFullBackupCanBeMadeAndRestoredFromThePage(t *testing.T) {
	html := readFile(t, "static/admin.html")
	js := stripComments(readFile(t, "static/admin.js"))

	for _, c := range []struct{ id, why string }{
		{"full-backup-passphrase", "there is nowhere to give a full backup its passphrase"},
		{"full-backup-passphrase-again", "a mistyped passphrase makes a backup nobody can open, and it is asked for once"},
		{"full-backup", "there is nothing to press to make a full backup"},
		{"full-backup-warning", "the warning that the passphrase is the operator's to keep has nowhere to be shown"},
		{"full-restore-file", "there is nowhere to choose a full backup to restore"},
		{"full-restore-passphrase", "there is nowhere to give the passphrase of a backup being restored"},
		{"full-restore", "there is nothing to press to read a full backup"},
		{"full-restore-summary", "what a restore would do has nowhere to be shown before it is done"},
		{"full-restore-confirmed", "there is nothing to press to confirm a restore"},
		{"full-restore-result", "what came back, and what did not, has nowhere to be shown"},
	} {
		if !strings.Contains(html, `id="`+c.id+`"`) {
			t.Errorf("%s: no %q on the page", c.why, c.id)
		}
		if !strings.Contains(js, `"`+c.id+`"`) {
			t.Errorf("%q is on the page and read by nothing", c.id)
		}
	}

	for _, endpoint := range []string{"/api/admin/full-backup", "/api/admin/full-restore"} {
		call := regexp.MustCompile(`(?s)fetch\("` + regexp.QuoteMeta(endpoint) + `",\s*\{(.*?)\}\)`)
		m := call.FindStringSubmatch(js)
		if m == nil {
			t.Errorf("nothing on the administration page calls %s", endpoint)
			continue
		}
		if !strings.Contains(m[1], `method: "POST"`) {
			t.Errorf("%s is not called with POST", endpoint)
		}
		if !strings.Contains(m[1], "body: JSON.stringify(") || !strings.Contains(m[1], "passphrase:") {
			t.Errorf("%s is not sent its passphrase in the body", endpoint)
		}
	}
}

// A passphrase in an address is in every proxy's log and the browser's
// history, and it opens every backup made with it. The server refuses to
// read one from there; the page must not put one there.
//
// Break it: build an address with the passphrase in it anywhere in admin.js.
func TestAPassphraseIsNeverPutInAnAddress(t *testing.T) {
	js := stripComments(readFile(t, "static/admin.js"))
	for _, bad := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)[?&]passphrase=`),
		regexp.MustCompile(`(?i)fetch\([^,)]*passphrase`),
		regexp.MustCompile(`(?i)(location|href|src)\s*=[^;]*passphrase`),
		regexp.MustCompile(`(?i)(localStorage|sessionStorage|console\.log)[^;]*passphrase`),
	} {
		if found := bad.FindString(js); found != "" {
			t.Errorf("a passphrase leaves the request body: %q", found)
		}
	}
	if !strings.Contains(js, "passphrase") {
		t.Fatal("no passphrase is handled in admin.js at all; this check is not checking anything")
	}
}
