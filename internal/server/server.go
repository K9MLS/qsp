// Package server hosts QSP's HTTP console.
//
// This phase establishes the transport: routing, middleware, health endpoints,
// the Server-Sent Events stream, and graceful shutdown. It deliberately exposes
// no management API. Endpoints that change state arrive with the features they
// belong to, after authorisation is designed.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/peers"
)

// Options configures a Server.
type Options struct {
	// ListenAddress is the host:port to bind.
	ListenAddress string
	// ReadHeaderTimeout bounds how long a client may take to send headers.
	ReadHeaderTimeout time.Duration
	// ReadTimeout bounds reading a whole request.
	ReadTimeout time.Duration
	// WriteTimeout bounds writing a response.
	//
	// It is deliberately not applied to the event stream: a long-lived SSE
	// connection would be severed by it. See the events handler.
	WriteTimeout time.Duration
	// IdleTimeout bounds an unused keep-alive connection.
	IdleTimeout time.Duration
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration
	// BehindProxy declares that a reverse proxy terminates TLS in front of
	// QSP, which makes forwarding headers trustworthy.
	BehindProxy bool
	// ConsoleAssets serves the embedded console. It may be nil, in which case
	// the console routes report that no assets are built into this binary.
	ConsoleAssets fs.FS
	// Calls reads the record of completed transmissions. Nil means none is
	// kept, which is different from a quiet network.
	Calls CallHistory
	// Callsign resolves a radio ID to a display name. Nil leaves the record
	// showing numbers, which is honest and less useful.
	Callsign func(uint32) string
	// Links supplies what is known about links to other networks. Nil means
	// this build has none, which is different from an instance with none
	// configured.
	Links LinkSource
	// Peers supplies the peer list. Nil means the DMR listener is not enabled,
	// which /api/peers reports rather than returning an empty list that would
	// look like "nobody is connected".
	Peers PeerSource
	// PeersDisabledReason explains a nil Peers, and is shown to the operator.
	PeersDisabledReason string
	// IPSCPeers supplies Motorola repeaters, which are a second listener on a
	// second socket rather than more of the same peers.
	//
	// Nil means the IPSC listener is not enabled. It is deliberately a
	// separate field rather than a slice of sources: the two listeners are
	// enabled independently, and an operator running only one should not have
	// to reason about an empty slot in a list.
	IPSCPeers PeerSource
	// Auth is the login flow. Nil means this instance has no administrator
	// accounts, which is a working state rather than a fault: QSP exposed no
	// endpoint that changes anything for its first several phases, and an
	// instance that only observes still does not need one.
	Auth Authenticator
	// Logins reports refused logins, so the console can say that somebody is
	// being turned away rather than leaving it to a member's phone call.
	Logins LoginReporter
	// Config exposes the running configuration for reading and saving. Nil
	// means this instance was started without one, which is a working state:
	// it runs on defaults and cannot be reconfigured from a browser.
	Config ConfigManager
	// Audit records administrative actions. Nil records nothing.
	Audit audit.Recorder
	// Restart stops QSP so that its supervisor starts it again.
	//
	// **Nil is a working state**, and the console says so rather than showing a
	// button that does nothing: an instance run in the foreground has nothing
	// watching it, and exiting would simply stop the server.
	Restart func()
	// Map configures the console's peer map.
	Map MapSettings
	// Forwarding reports whether this instance relays traffic, for the
	// console. It is a plain bool rather than part of PeerSource because it is
	// fixed at startup and asking the listener for it every poll would imply
	// otherwise.
	Forwarding bool
	// Join supplies the connection details shown to a member onboarding a
	// hotspot. The server does not read configuration itself, so the binary
	// fills this in.
	Join JoinSettings
}

// Server owns the HTTP listener and its lifecycle.
type Server struct {
	// live holds the options a saved configuration can change. See live.go.
	live live

	opts   Options
	log    *slog.Logger
	health Registry
	// offered holds passphrases this instance has offered and not yet seen
	// come back, so a reciprocal invitation needs no secret typed. See
	// offered.go.
	offered offeredPassphrases
	bus     *events.Bus
	http    *http.Server
	ln      net.Listener

	// baseCtx is the parent of every request context, and cancelling it is how
	// Shutdown reaches long-lived handlers.
	//
	// http.Server.Shutdown waits for active connections to finish but does not
	// cancel their request contexts. A Server-Sent Events handler blocks on
	// r.Context().Done() and so never finishes on its own, which means a single
	// open console tab holds shutdown hostage until the timeout expires. Every
	// test shut a server down with no browser attached and never saw it; a real
	// operator saw it on the first try.
	baseCtx    context.Context
	cancelBase context.CancelFunc
}

// Registry is the subset of health.Registry the server needs. Depending on the
// interface rather than the concrete type keeps the server testable without
// constructing a real registry.
type Registry interface {
	Run(ctx context.Context) health.Report
}

// New constructs a Server. It does not listen; call Start.
func New(log *slog.Logger, reg Registry, bus *events.Bus, opts Options) (*Server, error) {
	if reg == nil {
		return nil, errors.New("server requires a health registry")
	}
	if bus == nil {
		return nil, errors.New("server requires an event bus")
	}
	if opts.ListenAddress == "" {
		return nil, errors.New("server requires a listen address")
	}

	s := &Server{
		opts:   opts,
		log:    logging.Subsystem(log, "server"),
		health: reg,
		bus:    bus,
	}
	s.baseCtx, s.cancelBase = context.WithCancel(context.Background())

	s.http = &http.Server{
		Addr:              opts.ListenAddress,
		Handler:           s.handler(),
		ReadHeaderTimeout: opts.ReadHeaderTimeout,
		ReadTimeout:       opts.ReadTimeout,
		IdleTimeout:       opts.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return s.baseCtx },
	}
	return s, nil
}

// Handler exposes the configured routes, for tests.
func (s *Server) Handler() http.Handler { return s.http.Handler }

// apiRoute pairs a request pattern with the handler serving it.
type apiRoute struct {
	pattern string
	handler http.HandlerFunc
}

// apiRoutes is the one place an API endpoint is declared. handler registers
// from it, handleNoConsole reports from it, and SECURITY.md's endpoint
// inventory is checked against it by cmd/qsp/docaccuracy_test.go — so an
// endpoint cannot be added in code and forgotten in the security documentation.
//
// The console asset root is deliberately absent: it is conditional on whether
// assets are embedded, and it serves files rather than an API.
func (s *Server) apiRoutes() []apiRoute {
	return []apiRoute{
		{"GET /healthz", s.handleHealth},
		{"GET /readyz", s.handleReady},
		{"GET /api/events", s.handleEvents},
		{"GET /api/peers", s.handlePeers},
		{"GET /api/join", s.handleJoin},
		{"GET /api/join/config", s.handleHotspotConfig},

		{"POST /api/login", s.handleLogin},
		{"POST /api/logout", s.handleLogout},
		{"GET /api/session", s.handleSession},
		// Behind requireSession, which also enforces the origin check: the two
		// questions are asked of the same requests, and separating them is how
		// one gets forgotten on a new endpoint.
		{"GET /api/calls", s.requireSession(s.handleCalls)},
		{"GET /api/links", s.requireSession(s.handleLinks)},
		{"POST /api/peers/{id}/password", s.requireSession(s.handleIssueCredential)},
		{"DELETE /api/peers/{id}/password", s.requireSession(s.handleRevokeCredential)},
		{"POST /api/restart", s.requireSession(s.handleRestart)},
		{"POST /api/links/offer", s.requireSession(s.handleOfferPeering)},
		{"POST /api/links/offer-link", s.requireSession(s.handleOfferLink)},
		{"POST /api/links/accept", s.requireSession(s.handleAcceptPeering)},
		// **A page that creates a link must remove one.** Accepting a peering
		// wrote an upstream, a bridge and a passphrase file, and nothing could
		// undo any of it — an operator whose first attempt went wrong was left
		// with a broken link on the page unless they edited JSON on the server.
		{"DELETE /api/links/{name}", s.requireSession(s.handleRemoveLink)},
		{"DELETE /api/links/inbound/{id}", s.requireSession(s.handleRefuseInbound)},
		{"PUT /api/links/{name}/address", s.requireSession(s.handleLinkAddress)},
		{"GET /api/config", s.requireSession(s.handleGetConfig)},
		{"POST /api/config", s.requireSession(s.handleSaveConfig)},
		{"GET /api/config/versions", s.requireSession(s.handleConfigVersions)},
		{"GET /api/config/versions/{number}", s.requireSession(s.handleConfigVersion)},
	}
}

// APIPatterns returns the request patterns the server registers, in
// registration order. The zero Server is enough because only the patterns are
// read; the bound handlers are never called.
func APIPatterns() []string {
	routes := (&Server{}).apiRoutes()
	patterns := make([]string, len(routes))
	for i, r := range routes {
		patterns[i] = r.pattern
	}
	return patterns
}

// APIPaths returns the registered patterns with their HTTP method stripped,
// for prose that names endpoints rather than routes.
func APIPaths() []string {
	patterns := APIPatterns()
	paths := make([]string, len(patterns))
	for i, p := range patterns {
		if _, path, ok := strings.Cut(p, " "); ok {
			paths[i] = path
			continue
		}
		paths[i] = p
	}
	return paths
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()

	for _, r := range s.apiRoutes() {
		mux.HandleFunc(r.pattern, r.handler)
	}

	if s.opts.ConsoleAssets != nil {
		// /join is the URL an admin sends to fifty club members, so it is worth
		// a redirect rather than making them type join.html. It is not in
		// apiRoutes because it serves a page, not an API — the same reason the
		// asset root is not listed there.
		// /signin, for the same reason /join exists: a URL an operator types
		// or bookmarks should not end in .html.
		mux.HandleFunc("GET /signin", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/signin.html", http.StatusFound)
		})
		// The page itself is served to anyone; it shows a sign-in prompt rather
		// than a form when nobody is. The endpoints behind it are what require
		// a session, which is where the decision belongs.
		mux.HandleFunc("GET /access", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/access.html", http.StatusFound)
		})
		mux.HandleFunc("GET /network", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/network.html", http.StatusFound)
		})
		mux.HandleFunc("GET /bridges", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/bridges.html", http.StatusFound)
		})
		mux.HandleFunc("GET /record", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/record.html", http.StatusFound)
		})
		mux.HandleFunc("GET /links", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/links.html", http.StatusFound)
		})
		mux.HandleFunc("GET /history", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/history.html", http.StatusFound)
		})
		mux.HandleFunc("GET /join", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/join.html", http.StatusFound)
		})
		mux.Handle("GET /", revalidated(http.FileServerFS(s.opts.ConsoleAssets)))
	} else {
		mux.HandleFunc("GET /", s.handleNoConsole)
	}

	return chain(mux,
		withRequestID(),
		withRecovery(s.log),
		withSecurityHeaders(s.opts.Map.TileURL),
		withLogging(s.log),
	)
}

// Start binds the listener and serves in a background goroutine.
//
// Binding happens synchronously so that a port conflict is reported to the
// caller rather than surfacing later in a log line nobody reads.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.opts.ListenAddress)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w (check that the port is free and that you have permission to bind it)",
			s.opts.ListenAddress, err)
	}
	s.ln = ln
	s.log.Info("console listening", slog.String("address", ln.Addr().String()))

	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("console stopped unexpectedly", slog.String("error", err.Error()))
		}
	}()
	return nil
}

// Address returns the bound address, which is useful when the configured port
// was zero. It returns the configured address before Start.
func (s *Server) Address() string {
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.opts.ListenAddress
}

// Shutdown stops accepting connections and waits for in-flight requests,
// bounded by ShutdownTimeout.
func (s *Server) Shutdown(ctx context.Context) error {
	timeout := s.opts.ShutdownTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	// Cancel every in-flight request context first. Without this, a streaming
	// handler waiting on r.Context().Done() never returns and Shutdown blocks
	// until its timeout.
	s.cancelBase()

	shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := s.http.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("console did not shut down cleanly within %s: %w", timeout, err)
	}
	s.log.Info("console stopped")
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	report := s.health.Run(r.Context())
	status := http.StatusOK
	if report.Status == health.StatusFailing {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, s.log, status, report)
}

// handleReady answers whether the instance should receive traffic. It is
// intentionally terse so that a load balancer or container runtime can poll it
// cheaply.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	report := s.health.Run(r.Context())
	body := map[string]any{
		"ready":  report.Ready(),
		"status": report.Status,
	}
	status := http.StatusOK
	if !report.Ready() {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, s.log, status, body)
}

// handleNoConsole reports honestly that no console assets are embedded.
//
// Constitution §3: a missing capability says so rather than showing something
// that pretends to work. The endpoint list is derived from apiRoutes rather
// than written out, so it cannot fall behind the routes actually registered.
func (s *Server) handleNoConsole(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.log, http.StatusNotFound, map[string]any{
		"error":  "no console assets are embedded in this binary",
		"detail": "these API endpoints are available: " + strings.Join(APIPaths(), ", "),
	})
}

func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent, so this cannot be turned into an
		// error response. Logging it is the only honest option.
		log.Warn("cannot write JSON response", slog.String("error", err.Error()))
	}
}

// revalidated makes a browser check before reusing a console asset.
//
// **Embedded files carry no modification time**, so http.ServeContent sends
// neither Last-Modified nor ETag, and a browser with no validator falls back to
// heuristic caching — it keeps the file for as long as it likes. An operator who
// upgrades QSP then gets the new server and the old console, indefinitely, with
// no way to know why the fix they read about did not arrive.
//
// no-cache does not mean "do not store": it means "revalidate before use". With
// no validator to revalidate against the browser refetches, which for a console
// of a few tens of kilobytes is the right trade against serving stale
// JavaScript after every upgrade.
func revalidated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// LoginReporter reports logins the peer master is refusing.
//
// An interface so internal/server does not need a Master to be tested, which
// is the same reason every other collaborator here is one.
type LoginReporter interface {
	LoginFailures(now time.Time) []peers.LoginFailure
	BlockedSources(now time.Time) int
}
