package zellologon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k9mls/qsp/internal/secrets"
	"github.com/k9mls/qsp/internal/zello"
)

// Getter reads a credential. *secrets.Store satisfies it.
type Getter interface {
	Get(ctx context.Context, name string) (string, error)
	Has(ctx context.Context, name string) (bool, error)
}

// Options configures a Server.
type Options struct {
	// SocketPath is where to listen. Its directory must exist.
	SocketPath string
	// Store holds the credentials. Required.
	Store Getter
	// Issuer is the issuer string from Zello's developer portal. It is not a
	// secret — it names the key pair — so it lives in configuration.
	Issuer string
	// Audience is the token's azp claim. Empty selects zello.TokenAudience.
	Audience string
	// Log records what happened. Nil logs nothing.
	Log *slog.Logger
	// allowPeer decides whether a connecting process may be served. Nil
	// selects samePeerUser. A field for tests, which cannot become another
	// user.
	allowPeer func(net.Conn) error
}

// Server hands out logons.
type Server struct {
	opts Options
	ln   *net.UnixListener
	log  *slog.Logger
	wg   sync.WaitGroup

	served, refusedPeer, refusedLogon atomic.Uint64

	mu      sync.Mutex
	lastErr string
}

// Listen opens the socket.
//
// **A stale socket is removed, and only a socket.** QSP stopping uncleanly
// leaves the file behind and a second start would fail to bind; but a path
// that points at a regular file is a configuration mistake, and deleting it
// would destroy whatever the operator put there.
func Listen(opts Options) (*Server, error) {
	if opts.Store == nil {
		return nil, errors.New("zellologon: no credential store; the socket needs a database to read from")
	}
	if strings.TrimSpace(opts.Issuer) == "" {
		return nil, errors.New("zellologon: no issuer; give zello.issuer from the developer portal")
	}
	if opts.allowPeer == nil {
		opts.allowPeer = samePeerUser
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	if fi, err := os.Lstat(opts.SocketPath); err == nil {
		if fi.Mode().Type() != fs.ModeSocket {
			return nil, fmt.Errorf("zellologon: %s exists and is not a socket; refusing to remove it",
				opts.SocketPath)
		}
		if err := os.Remove(opts.SocketPath); err != nil {
			return nil, fmt.Errorf("zellologon: removing the stale socket %s: %w", opts.SocketPath, err)
		}
	}

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: opts.SocketPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("zellologon: cannot listen on %s: %w", opts.SocketPath, err)
	}
	// **Chmod after the bind, not a umask around it.** The umask is
	// process-wide, and QSP has goroutines creating files — the database, a
	// backup — that would take its value for that instant. The window between
	// the bind and this chmod is covered by the peer check, which refuses any
	// other user whatever the file's mode was; that is why there are two locks.
	if err := os.Chmod(opts.SocketPath, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("zellologon: restricting %s to its owner: %w", opts.SocketPath, err)
	}
	return &Server{opts: opts, ln: ln, log: log.With(slog.String("subsystem", "zello-logon"))}, nil
}

// Serve answers connections until ctx is done, then removes the socket.
func (s *Server) Serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = s.ln.Close()
	}()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() == nil {
				s.note(fmt.Sprintf("the logon socket stopped accepting: %v", err))
			}
			break
		}
		s.wg.Go(func() { s.answer(ctx, conn) })
	}
	s.wg.Wait()
	_ = os.Remove(s.opts.SocketPath)
}

func (s *Server) answer(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(Timeout))

	if err := s.opts.allowPeer(conn); err != nil {
		s.refusedPeer.Add(1)
		s.note(err.Error())
		s.log.Warn("refused a logon request", slog.String("reason", err.Error()))
		return
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil || req.Want != "logon" {
		s.write(conn, reply{Kind: KindUnusable, Error: "the request is not a logon request"})
		return
	}

	logon, rerr := s.logon(ctx)
	if rerr != nil {
		s.refusedLogon.Add(1)
		s.note(rerr.Message)
		s.write(conn, reply{Kind: rerr.Kind, Error: rerr.Message})
		return
	}
	s.served.Add(1)
	s.write(conn, reply{Logon: &logon})
	s.log.Info("handed out a Zello logon")
}

// logon assembles one from the store. The key is read and parsed per request,
// so a key replaced in the console is used from the next logon on.
func (s *Server) logon(ctx context.Context) (Logon, *Error) {
	values := map[string]string{}
	for _, name := range []string{PrivateKeyName, UsernameName, PasswordName} {
		v, err := s.opts.Store.Get(ctx, name)
		switch {
		case errors.Is(err, secrets.ErrNotFound):
			return Logon{}, &Error{Kind: KindMissing, Message: fmt.Sprintf(
				"the credential %q has not been entered; add it in the console", name)}
		case err != nil:
			return Logon{}, &Error{Kind: KindUnavailable, Message: fmt.Sprintf(
				"cannot read the credential %q: %v", name, err)}
		}
		values[name] = v
	}
	signer, err := zello.NewSigner(zello.SignerOptions{Issuer: s.opts.Issuer,
		PrivateKeyPEM: values[PrivateKeyName], Audience: s.opts.Audience})
	if err != nil {
		return Logon{}, &Error{Kind: KindUnusable, Message: err.Error()}
	}
	token, err := signer.Token()
	if err != nil {
		return Logon{}, &Error{Kind: KindUnusable, Message: err.Error()}
	}
	return Logon{Token: token, Username: values[UsernameName], Password: values[PasswordName]}, nil
}

func (s *Server) write(conn net.Conn, r reply) {
	if err := json.NewEncoder(conn).Encode(r); err != nil {
		s.note(fmt.Sprintf("cannot write a reply: %v", err))
	}
}

func (s *Server) note(msg string) {
	s.mu.Lock()
	s.lastErr = msg
	s.mu.Unlock()
}

// Counters reports logons served, requests refused because of who asked, and
// requests refused because a logon could not be made.
func (s *Server) Counters() (served, refusedPeer, refusedLogon uint64) {
	return s.served.Load(), s.refusedPeer.Load(), s.refusedLogon.Load()
}
