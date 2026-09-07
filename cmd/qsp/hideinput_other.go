//go:build !linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// hideInput on platforms without the Linux termios path.
//
// The ioctl version in hideinput_linux.go uses TCGETS and TCSETS, which are
// Linux's names for the request; other systems spell them differently, and
// nothing in this project runs on them. **Rather than guess at constants that
// cannot be tested here**, these fall back to stty — which is what the whole
// program used before the container proved it was not always there.
//
// A port begins by writing the ioctl for that platform, not by trusting this.
func hideInput() (restore func(), err error) {
	if _, err := exec.LookPath("stty"); err != nil {
		return nil, errors.New("cannot hide the password: stty is not available, " +
			"and typing a password with it echoing to the screen is worse than " +
			"not setting one")
	}
	if err := stty("-echo"); err != nil {
		return nil, fmt.Errorf("a password must be typed at a terminal, not piped in: %w", err)
	}
	return func() { _ = stty("echo") }, nil
}

func stty(arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("stty %s: %w", arg, err)
	}
	return nil
}
