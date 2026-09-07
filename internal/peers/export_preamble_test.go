package peers

import "github.com/k9mls/qsp/internal/protocol/hbp"

// IsPreambleForTest exposes the suppression rule to the external test package.
//
// It is exported through a test file so the rule itself stays unexported: the
// decision belongs to this package and nothing outside it should be able to
// ask, but a test in peers_test needs to check it against a real capture.
func IsPreambleForTest(frame hbp.Data) bool { return isPreamble(frame) }
