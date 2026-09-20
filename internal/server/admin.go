package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/buildinfo"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/health"
)

// The administration page's one endpoint (ADR-0055).
//
// # Why a page, and why one endpoint
//
// Several defects found on 2026-09-08 existed because nothing could show them.
// A server's identifier is generated at startup, written to disk and announced
// to every neighbour, and appeared in one log line that scrolled away. The
// callsign lookup was off on one server and on on the other, and nothing said
// so. The version was read out of `journalctl` all evening because there was
// nowhere on a page to look.
//
// One endpoint rather than five, because the page answers one question — *is
// this server healthy, and what does it need from me* — and five requests would
// let the blocks disagree with each other about the same moment.
//
// # What it must not become
//
// **Not a settings dump.** ADR-0055's rule: a page may edit a setting when it
// is the page that reports the problem, and — binding harder — if it is not
// reporting a problem with a setting it does not get to edit it. Every field
// added here has to answer that, and "it is administration" is not an answer.

// adminBody is what the administration page reads.
type adminBody struct {
	Server   adminServer    `json:"server"`
	Agree    adminAgreement `json:"agreement"`
	Services adminServices  `json:"services"`
	Sessions adminSessions  `json:"sessions"`
}

// adminSessions is how long a login lasts, and what that means right now.
//
// **Reported before it is editable, which is ADR-0055's condition.** A
// lifetime box on its own would be a settings editor, which this page is not;
// what makes it this page's business is that nothing anywhere said which value
// was in force, whether it was the operator's choice or the built-in default,
// or when the session doing the reading would end. An operator part-way
// through a restore wants the last of those.
type adminSessions struct {
	// LifetimeSeconds is the lifetime a new login gets.
	LifetimeSeconds int64 `json:"lifetime_seconds"`
	// Default reports that nothing is configured, so the built-in twelve
	// hours applies. The distinction is the one the file hides: a
	// configuration that says nothing and a configuration that says twelve
	// hours read identically from the outside.
	Default bool `json:"default"`
	// Active is how many logins are valid now, expired ones excluded.
	Active int `json:"active"`
	// ExpiresAt and ExpiresInSeconds are the requesting session's own end,
	// stated twice for the reason uptime is: a timestamp answers "when" and
	// the remainder answers "will this outlast what I am about to do".
	ExpiresAt        time.Time `json:"expires_at,omitzero"`
	ExpiresInSeconds int64     `json:"expires_in_seconds,omitempty"`
	// Limits are what the configuration validator will accept, so the page
	// can refuse a value before a round trip and say the same thing.
	MinSeconds int64 `json:"min_seconds"`
	MaxSeconds int64 `json:"max_seconds"`
}

// sessionCounter is the part of the accounts service the sessions block needs.
//
// **Optional rather than added to AccountAdmin**, so that a server built for a
// test without one reports no count instead of failing to compile. The risk in
// that is a production wiring where the count is silently always absent, which
// is what the assertion below exists to catch.
type sessionCounter interface {
	ActiveSessions(ctx context.Context) (int, error)
}

var _ sessionCounter = (*auth.Service)(nil)

// adminServer is what this server is.
type adminServer struct {
	// Network and Callsign are the display name and the operator's callsign —
	// what a human reads (ADR-0053).
	Network  string `json:"network,omitempty"`
	Callsign string `json:"callsign,omitempty"`
	// Identifier is the truncated form, and there is deliberately no way to
	// edit it. It is here for the one case it exists for: two servers may
	// choose the same display name, and then this is what tells them apart.
	Identifier string `json:"identifier,omitempty"`
	// Version is what this process is running — the string the `starting` log
	// line carries, which until now was the only place to read it.
	Version string `json:"version"`
	// StartedAt and UptimeSeconds describe the same fact twice, deliberately.
	//
	// **A server restarted a minute ago shows a small number, which is correct
	// and reads as a fault.** The start time beside it makes "4m" legible as
	// *since 08:14* rather than as something going wrong.
	StartedAt     time.Time `json:"started_at"`
	UptimeSeconds int64     `json:"uptime_seconds"`
}

// adminAgreement reports whether the running server matches its configuration.
//
// **This block is why the page earns its place.** The state is already computed
// and was reported only as a sentence beside whichever link happened to be
// saved, so a server could be several changes away from its own configuration
// with no single view saying so. It cost three round trips in one evening.
type adminAgreement struct {
	// Agrees is true when the running server matches the saved configuration.
	Agrees bool `json:"agrees"`
	// Pending names the settings a restart would apply, in the words the rest
	// of the console already uses.
	Pending []string `json:"pending,omitempty"`
	// Version is the configuration version now saved.
	Version int64 `json:"version,omitempty"`
	// Writable reports whether this instance can be configured from here at
	// all. An instance started without -config runs on defaults and says so
	// rather than discovering it at save.
	Writable bool `json:"writable"`
	// NotWritable explains why, when it cannot.
	NotWritable string `json:"not_writable,omitempty"`
}

// adminServices reports what is running that nobody configured per-link.
type adminServices struct {
	// Callsigns is the lookup: on or off, and whether it can work.
	Callsigns adminCallsigns `json:"callsigns"`
	// Health is the subsystem report, unchanged.
	//
	// **Read from the health registry rather than recomputed.** Health is the
	// machine-readable subsystem check and this page is the operator-facing
	// view of the same facts; two sources would be two things that drift, and
	// this project has met that shape often enough to name it.
	Health health.Report `json:"health"`
}

// adminCallsigns is the callsign lookup, and the one setting this page edits.
//
// It qualifies under ADR-0055's rule because this page is what reports it as
// off: an operator found it by noticing that one server's Last-heard table
// showed callsigns and the other showed radio IDs.
type adminCallsigns struct {
	Enabled bool   `json:"enabled"`
	Contact string `json:"contact,omitempty"`
	// Usable reports whether the lookup can actually run.
	//
	// Equal to Enabled today, and kept separate because they answer different
	// questions: an operator asks whether radios are being named, and the
	// configuration says whether the setting is on. A future reason for the
	// lookup to be on and not working — a registry that will not answer, a
	// contact address it has rejected — belongs here rather than in a second
	// field nobody reads.
	Usable bool `json:"usable"`
	// Why explains an unusable state in the operator's terms.
	Why string `json:"why,omitempty"`
}

// handleAdmin assembles the administration page.
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	body := adminBody{
		Server: adminServer{
			Version:       buildinfo.Version,
			StartedAt:     s.startedAt,
			UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		},
	}

	if s.opts.Config != nil {
		cfg := s.opts.Config.Current()
		body.Server.Network = strings.TrimSpace(cfg.DMR.Join.NetworkName)
		body.Server.Callsign = strings.TrimSpace(cfg.DMR.Identity.Callsign)
		body.Server.Identifier = config.ShortIdentifier(strings.TrimSpace(cfg.Server.Identifier))
		body.Agree = agreement(s.opts.Config)
		body.Services.Callsigns = callsignState(cfg.DMR.Callsigns)
	} else {
		body.Agree.NotWritable = "this instance was started without a configuration file, " +
			"so it runs on defaults and cannot be configured from here"
	}

	if s.health != nil {
		body.Services.Health = s.health.Run(r.Context())
	}

	body.Sessions = s.sessionState(r)

	writeJSON(w, s.log, http.StatusOK, body)
}

// agreement compares the running server with its saved configuration.
func agreement(store ConfigManager) adminAgreement {
	out := adminAgreement{Agrees: true, Writable: true}
	if err := store.Writable(); err != nil {
		out.Writable = false
		out.NotWritable = err.Error()
	}
	// **Compared against what is running, not against the previous save.** A
	// server several changes behind its configuration must say so once, not
	// once per change that happened to be noticed.
	if pending := store.PendingRestart(); len(pending) > 0 {
		out.Agrees = false
		out.Pending = pending
	}
	return out
}

// callsignState reports the lookup and whether it can work.
func callsignState(c config.Callsigns) adminCallsigns {
	out := adminCallsigns{
		Enabled: c.Enabled,
		Contact: strings.TrimSpace(c.Contact),
	}
	// **Two states, not three.** An earlier version reported a third — on, with
	// no contact address, and therefore unable to run — and no server can be in
	// it: `config.Validate` refuses a configuration with the lookup enabled and
	// no contact, so such a document never loads. The branch rendered nothing
	// and the test covering it asserted on a struct built by hand, which is a
	// test that cannot fail.
	//
	// The state is prevented rather than reported, which is the stronger of the
	// two, and the endpoint that saves this setting refuses the same
	// combination with the same words before it reaches the validator.
	if !c.Enabled {
		out.Why = "callsign lookup is off, so Last heard shows radio IDs rather than callsigns"
		return out
	}
	out.Usable = true
	return out
}

// callsignRequest is the one setting this page may change.
type callsignRequest struct {
	Enabled bool   `json:"enabled"`
	Contact string `json:"contact"`
}

// handleCallsigns turns the callsign lookup on or off and sets the contact.
//
// **The one setting this page edits**, under ADR-0055's rule: a page may edit a
// setting when it is the page that reports the problem. This page is what
// reports the lookup as off — an operator found it by noticing that one
// server's Last-heard table showed callsigns and the other showed radio IDs,
// which no page said anything about.
func (s *Server) handleCallsigns(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance cannot be configured from here"})
		return
	}

	var req callsignRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}

	contact := strings.TrimSpace(req.Contact)
	// **Refused rather than saved as a setting that says on and does
	// nothing.** The registry asks automated clients to identify themselves,
	// so a lookup with no contact never runs — and §7 already forbids a field
	// that means "not applicable" while looking like "not set".
	if req.Enabled && contact == "" {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "callsign lookup needs a contact address: the registry asks automated " +
				"clients to identify themselves, and QSP will not invent one for you",
		})
		return
	}

	cfg := s.opts.Config.Current()
	cfg.DMR.Callsigns.Enabled = req.Enabled
	cfg.DMR.Callsigns.Contact = contact

	author := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		author = sess.Username
	}
	summary := "callsign lookup off"
	if req.Enabled {
		summary = "callsign lookup on"
	}

	version, err := s.opts.Config.Save(r.Context(), cfg, author, summary)
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, s.log, http.StatusOK, map[string]any{
		"callsigns":     callsignState(cfg.DMR.Callsigns),
		"version":       version.Number,
		"needs_restart": config.NeedsRestart(s.opts.Config.Current(), cfg),
	})
}

// sessionState reports the lifetime, the count, and this session's own end.
func (s *Server) sessionState(r *http.Request) adminSessions {
	out := adminSessions{
		LifetimeSeconds: int64(auth.DefaultSessionLifetime.Seconds()),
		Default:         true,
		MinSeconds:      int64(config.MinSessionLifetime.Seconds()),
		MaxSeconds:      int64(config.MaxSessionLifetime.Seconds()),
	}
	if s.opts.Config != nil {
		if d := time.Duration(s.opts.Config.Current().Server.SessionLifetime); d > 0 {
			out.LifetimeSeconds = int64(d.Seconds())
			out.Default = false
		}
	}
	if counter, ok := s.opts.Accounts.(sessionCounter); ok && counter != nil {
		if n, err := counter.ActiveSessions(r.Context()); err == nil {
			out.Active = n
		} else {
			s.log.Warn("cannot count sessions", "error", err)
		}
	}
	if sess, ok := SessionFrom(r.Context()); ok && !sess.ExpiresAt.IsZero() {
		out.ExpiresAt = sess.ExpiresAt.UTC()
		if left := time.Until(sess.ExpiresAt); left > 0 {
			out.ExpiresInSeconds = int64(left.Seconds())
		}
	}
	return out
}

// sessionLifetimeRequest is the new lifetime, in seconds.
type sessionLifetimeRequest struct {
	Seconds int64 `json:"seconds"`
}

// handleSessionLifetime sets how long a console login lasts.
//
// **The second setting this page edits, and it qualifies the same way**: the
// block above it is what reports the value in force, whether it is the default
// and when the reader's own session ends. Before this it was a line in a file
// on a page whose purpose is to keep an operator out of the file.
//
// **Existing sessions keep the expiry they were issued.** A change that logged
// everybody out would make a setting adjustment an outage, and the operator
// making it would be the first one out. The new value applies at the next
// login, and the page says so.
func (s *Server) handleSessionLifetime(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance cannot be configured from here"})
		return
	}

	var req sessionLifetimeRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}

	cfg := s.opts.Config.Current()
	cfg.Server.SessionLifetime = config.Duration(time.Duration(req.Seconds) * time.Second)

	author := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		author = sess.Username
	}

	// **Saved through the same validator `-check` runs**, rather than checked
	// again here: two places that decide what a valid lifetime is are two
	// places that disagree later. The bounds and their reasons come back as
	// the error the page shows.
	version, err := s.opts.Config.Save(r.Context(), cfg, author,
		fmt.Sprintf("session lifetime %s", time.Duration(req.Seconds)*time.Second))
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, s.log, http.StatusOK, map[string]any{
		"sessions": s.sessionState(r),
		"version":  version.Number,
	})
}
