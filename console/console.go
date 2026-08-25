// Package console embeds the QSP web console.
//
// Assets are compiled into the binary so that a deployment is a single file and
// cannot serve a console that disagrees with the server it runs against.
//
// The console shell in this phase renders real health and the real event stream
// connection state, and nothing else. It contains no sample peers, no sample
// traffic and no illustrative charts: an empty instance shows an empty state
// that says why it is empty.
package console

import (
	"embed"
	"io/fs"
)

//go:embed static
var assets embed.FS

// Assets returns the console's static files rooted so that "/index.html"
// resolves without a "static" prefix.
func Assets() (fs.FS, error) {
	return fs.Sub(assets, "static")
}
