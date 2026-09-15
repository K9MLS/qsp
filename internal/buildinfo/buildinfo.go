// Package buildinfo carries the release number into the binary.
package buildinfo

// Version is the release number, and must equal the VERSION file at the root of
// the repository.
//
// # Why this exists at all
//
// The VERSION file was read by nothing. `qsp --version` came from
// debug.ReadBuildInfo, which reports a pseudo-version like
// v0.0.0-20260903014457-6db5cc98ec75, so the number bumped in three consecutive
// patches had no connection to anything the running binary would say. Nobody
// noticed until an operator ran the command and compared.
//
// # Why a constant rather than a linker flag
//
// cmd/qsp already has a `version` variable for `-ldflags -X` and nothing ever
// sets it, which is the same failure one level along: a mechanism that exists,
// is documented, and is never invoked. A release built with a plain `go build`
// — which is how this project is built and deployed — would still report
// nothing.
//
// A constant is compiled in unconditionally. The linker flag still wins when it
// is set, so a release pipeline can override it, but the ordinary build is
// correct without anybody remembering a flag.
//
// # Why the file is not embedded
//
// An embed directive cannot reach outside its own directory, and VERSION lives
// at the repository root where a human editing a release number will look for
// it.
// Copying the file here would create two files to keep in step and no way to
// notice when they drift. A constant plus a test that reads the real file is
// the same guarantee with one file.
const Version = "0.1.229"
