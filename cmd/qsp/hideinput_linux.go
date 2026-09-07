//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Hiding a typed password, without asking the operating system for help.
//
// # Why this file exists
//
// `adduser` used to turn echo off by running `stty -echo`, with a comment
// saying that if stty were missing the prompt would refuse rather than echo.
// **That was the right refusal and the wrong mechanism**, and the container
// found out: the image is a `scratch` layer holding one static binary, so there
// is no stty, no shell and no /bin at all.
//
//	qsp: cannot hide the password: stty is not available
//
// A server nobody can create an account on is a server nobody can sign in to,
// and it was the first thing an operator hit after a successful install.
//
// # Doing it in the process instead
//
// Turning off ECHO is two ioctls on the terminal, and everything they need —
// TCGETS, TCSETS, the Termios layout — is in the standard library. No shell, no
// external binary, and **no new dependency**, which ADR-0004 would have made
// the alternative expensive: golang.org/x/term is the usual answer and it is a
// module this project would then carry forever for eleven lines of syscall.
//
// It also fixes the same failure on a systemd install into a minimal image, and
// on anything else without a full userland.
//
// The check that this is a terminal is the TCGETS itself: it fails with ENOTTY
// on a pipe, which is exactly the case where a password must not be read.

// hideInput turns off terminal echo on standard input and returns a function
// that restores it.
//
// **It refuses rather than continuing when echo cannot be turned off.** Typing
// a password with it visible on screen, into a session that may be logged or
// recorded or looked over, is worse than not setting one.
func hideInput() (restore func(), err error) {
	fd := int(os.Stdin.Fd())

	var before syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, &before); err != nil {
		return nil, fmt.Errorf("cannot hide the password: this is not a terminal "+
			"(%w). Run it with a terminal attached — under Docker that is "+
			"`docker compose exec -it`", err)
	}

	after := before
	after.Lflag &^= syscall.ECHO
	if err := ioctl(fd, syscall.TCSETS, &after); err != nil {
		return nil, fmt.Errorf("cannot hide the password: %w, and typing one with "+
			"it echoing to the screen is worse than not setting one", err)
	}

	return func() { _ = ioctl(fd, syscall.TCSETS, &before) }, nil
}

// ioctl issues one terminal control request.
//
// The unsafe.Pointer is the ioctl calling convention and nothing more: the
// kernel reads or writes a Termios at that address, and the value is a local
// that outlives the call.
func ioctl(fd int, request uintptr, t *syscall.Termios) error {
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), request,
		uintptr(unsafe.Pointer(t)), 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
