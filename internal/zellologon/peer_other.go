//go:build !linux

package zellologon

import (
	"errors"
	"net"
)

// samePeerUser refuses everything off Linux: without SO_PEERCRED there is no
// kernel statement of who connected, and serving a logon on the file mode
// alone is a guarantee this package is not willing to make.
func samePeerUser(net.Conn) error {
	return errors.New("zellologon: peer credentials cannot be checked on this platform")
}
