//go:build linux

package zellologon

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// samePeerUser refuses a connection from any user but the one QSP runs as.
//
// **The file mode already does this, and this is the second lock.** A socket
// created under a permissive umask, or a directory whose permissions an
// operator loosened, would otherwise serve a logon to every user on the host.
// SO_PEERCRED is the kernel's statement of who connected, not the caller's.
func samePeerUser(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("zellologon: a logon connection is not a Unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return fmt.Errorf("zellologon: reading the peer's credentials: %w", err)
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("zellologon: reading the peer's credentials: %w", err)
	}
	if credErr != nil {
		return fmt.Errorf("zellologon: reading the peer's credentials: %w", credErr)
	}
	if int(cred.Uid) != os.Getuid() {
		return fmt.Errorf("zellologon: uid %d asked for a logon and only uid %d is served",
			cred.Uid, os.Getuid())
	}
	return nil
}
