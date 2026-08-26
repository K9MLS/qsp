//go:build !windows

package main

import (
	"os"

	"github.com/k9mls/qsp/internal/config"
)

// checkPasswordFileMode refuses to start if the peer password file is readable
// beyond its owner.
//
// Split by platform rather than branching on runtime.GOOS so that the Windows
// build does not carry a check it can never apply, and so the reason for the
// difference is stated once, in the file it applies to.
func checkPasswordFileMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		// A missing or unreadable file is LoadPeerPassword's error to report,
		// with its guidance about creating one. Reporting it twice, differently,
		// helps nobody.
		return nil
	}
	return config.CheckPeerPasswordMode(info.Mode())
}
