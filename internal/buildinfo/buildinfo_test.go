package buildinfo_test

import (
	"os"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/buildinfo"
)

// TestTheVersionConstantMatchesTheVersionFile is the whole reason a constant is
// acceptable here.
//
// Two places holding a release number is only safe if something notices when
// they disagree. Without this the constant would drift from the file the moment
// somebody bumped one and not the other, and the binary would confidently
// report a version nobody released.
func TestTheVersionConstantMatchesTheVersionFile(t *testing.T) {
	raw, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatalf("reading VERSION: %v", err)
	}
	want := strings.TrimSpace(string(raw))
	if buildinfo.Version != want {
		t.Errorf("buildinfo.Version is %q and the VERSION file says %q; "+
			"bump both or the binary reports a release that does not exist",
			buildinfo.Version, want)
	}
}
