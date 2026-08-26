//go:build windows

package main

// checkPasswordFileMode does nothing on Windows.
//
// os.Stat does not report an ACL. It synthesises a mode from the read-only
// attribute, so an ordinary file reads as 0666 no matter how tightly it is
// actually secured. Enforcing the POSIX check here would reject every correctly
// protected file and teach operators to work around a control rather than
// satisfy it, which is worse than not having one.
//
// The equivalent on Windows is an ACL granting the service account alone; that
// is a deployment concern, and docs/HARDWARE-TEST.md says so.
func checkPasswordFileMode(string) error { return nil }
