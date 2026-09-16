// Package zellologon hands the Zello connector a logon over a local socket,
// and never the key the logon is signed with.
//
// See ADR-0066. QSP holds the RSA private key in its credential store; the
// connector, qsp-zello, is a separate process holding an internet connection
// and a C codec, and is the last process that should hold a key able to sign
// logons for as long as the key pair lives. So QSP signs, and the connector
// asks.
//
// # The protocol
//
// One request, one reply, one connection. The connector writes a line of JSON,
// QSP writes a line of JSON back and closes. Nothing is negotiated and nothing
// is named by the caller: the credentials served are fixed below, so the
// socket cannot be asked for anything else in the store.
package zellologon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// The credential names, fixed so that the socket serves these three and
// nothing else. An operator enters them in the console under exactly these.
const (
	PrivateKeyName = "zello-private-key"
	UsernameName   = "zello-username"
	PasswordName   = "zello-password"
)

// Logon is what a connector needs to log on to Zello.
type Logon struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
	// Channel travels with the logon so the connector's own file holds
	// nothing an operator changes; it is set on QSP's Zello page.
	Channel string `json:"channel"`
}

// Kinds of refusal, so the connector can say which action an operator needs.
const (
	// KindMissing means a credential has not been entered.
	KindMissing = "missing"
	// KindUnusable means a credential is present and cannot be used — a key
	// that does not parse, an issuer not configured.
	KindUnusable = "unusable"
	// KindUnavailable means QSP could not read its store at all.
	KindUnavailable = "unavailable"
)

// Error is a refusal QSP sent, as opposed to a socket that did not answer.
type Error struct {
	Kind    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// ErrUnreachable wraps every failure to exchange with QSP, as opposed to QSP
// answering with a refusal: QSP not running, or not serving logons.
var ErrUnreachable = errors.New("QSP's logon socket did not answer")

type request struct {
	Want string `json:"want"`
}

type reply struct {
	Logon *Logon `json:"logon,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Error string `json:"error,omitempty"`
}

// Timeout bounds one exchange at either end. A logon is a few milliseconds of
// work; anything slower is a stuck peer, and neither side should wait on one.
const Timeout = 5 * time.Second

// Fetch asks QSP for a logon.
//
// A *Error is QSP refusing, with a Kind saying why. Any other error is the
// socket: QSP not running, or not configured to serve logons.
func Fetch(ctx context.Context, socketPath string) (Logon, error) {
	var d net.Dialer
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return Logon{}, fmt.Errorf("%w: cannot reach %s: %w", ErrUnreachable, socketPath, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if err := json.NewEncoder(conn).Encode(request{Want: "logon"}); err != nil {
		return Logon{}, fmt.Errorf("%w: asking for a logon: %w", ErrUnreachable, err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return Logon{}, fmt.Errorf("%w: reading the reply: %w", ErrUnreachable, err)
	}
	var r reply
	if err := json.Unmarshal(line, &r); err != nil {
		return Logon{}, fmt.Errorf("%w: the reply is not JSON: %w", ErrUnreachable, err)
	}
	if r.Error != "" {
		return Logon{}, &Error{Kind: r.Kind, Message: r.Error}
	}
	if r.Logon == nil {
		return Logon{}, fmt.Errorf("%w: QSP replied with neither a logon nor a refusal", ErrUnreachable)
	}
	return *r.Logon, nil
}
