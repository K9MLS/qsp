// Package config defines QSP's configuration model, its defaults, its
// validation rules, and the versioning concepts the console builds on.
//
// Three rules govern this package:
//
//  1. There is exactly one configuration system. Subsystems receive typed
//     values from this package; none of them read files or environment
//     variables on their own.
//
//  2. An invalid configuration can never become active. Validate returns every
//     problem it finds rather than the first, because an operator fixing a form
//     should see all of it at once.
//
//  3. Every field carries the operator guidance the console renders. That
//     guidance lives beside the field definition so the two cannot drift.
//
// Configuration is ultimately edited through the web UI, not by hand. The JSON
// representation is a persistence format, not a user interface.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"sort"
	"strings"
	"time"
)

// SchemaVersion identifies the shape of the configuration document.
//
// It is incremented when a change requires migration of stored configuration.
// A document bearing a higher version than the running binary understands is
// rejected rather than partially interpreted.
const SchemaVersion = 1

// Config is the complete configuration of a QSP instance.
type Config struct {
	// Version is the schema version of this document.
	Version int `json:"version"`

	Server   Server   `json:"server"`
	Database Database `json:"database"`
	Logging  Logging  `json:"logging"`
	Events   Events   `json:"events"`
	DMR      DMR      `json:"dmr"`
}

// DMR configures the Homebrew Protocol listener that peers connect to.
//
// # Why the password is a file path
//
// Configuration is versioned, exportable and diffable. A shared secret stored
// in this document would be written into the version history table, appear in
// every export, and show up in a diff on the console. PasswordFile keeps it out
// of all three: the document records where the secret lives, never what it is.
//
// See docs/adr/ADR-0012.
type DMR struct {
	// Enabled turns the peer listener on. When false, QSP runs as a console
	// only and the network subsystem reports itself unavailable.
	Enabled bool `json:"enabled"`
	// ListenAddress is the UDP host:port to bind. 62031 is the port observed in
	// practice for the Homebrew Protocol.
	ListenAddress string `json:"listen_address"`
	// PasswordFile is the path to a file whose contents are the shared peer
	// password. Leading and trailing whitespace is stripped. The file should be
	// mode 0600.
	PasswordFile string `json:"password_file"`
	// PeerTimeout is how long a registered peer may be silent before it is
	// removed. Observed keepalive interval is 10 s.
	PeerTimeout Duration `json:"peer_timeout"`
	// LoginTimeout bounds an incomplete handshake, which holds a slot without
	// providing service.
	LoginTimeout Duration `json:"login_timeout"`
	// MaxPeers bounds the registry.
	MaxPeers int `json:"max_peers"`
	// Forwarding turns audio relaying on.
	//
	// It is separate from Enabled, and off by default, so that an operator can
	// run QSP as a master and watch peers connect before it starts putting
	// audio on anybody's repeater. Turning it on is the moment QSP stops
	// observing and starts transmitting.
	Forwarding bool `json:"forwarding"`
	// Join is what a club member needs in order to point a hotspot at this
	// network. Optional: an empty Join means /api/join reports what it can and
	// tells the member to ask their admin for the rest, rather than guessing.
	Join Join `json:"join"`
	// Bridges are the routing rules. Traffic is only relayed between endpoints
	// a bridge joins.
	Bridges []Bridge `json:"bridges"`
	// Triggers open a bridge when somebody transmits on it, and close it after
	// a hang time. A bridge may be scheduled, triggered, both, or neither.
	Triggers []Trigger `json:"triggers"`
	// Schedule holds recurring windows that enable bridges automatically.
	//
	// A bridge named by any window is controlled entirely by the schedule; its
	// own "enabled" field is ignored. A bridge with no window uses that field.
	// One mechanism decides each bridge, so there is never a question of which
	// setting wins.
	Schedule []Window `json:"schedule"`
	// Upstreams are links to other DMR networks over OpenBridge.
	Upstreams []Upstream `json:"upstreams"`
}

// Upstream is a link to another DMR server over OpenBridge.
//
// See docs/adr/ADR-0018-openbridge.md. Everything here is administrator
// configuration: a club in Toulouse points at a different master from a club in
// Texas, and QSP has no opinion about which.
type Upstream struct {
	// Name identifies this link on the console and in the health report.
	Name string `json:"name"`
	// Enabled turns the link on.
	//
	// It defaults to false deliberately. Enabling an upstream puts a club's
	// audio onto somebody else's network, and that should be a deliberate edit
	// rather than something inherited from a copied configuration.
	Enabled bool `json:"enabled"`
	// Address is the far end, host:port. OpenBridge conventionally uses 62035.
	Address string `json:"address"`
	// ListenAddress is the local UDP host:port to receive on. OpenBridge has no
	// connection establishment, so the far end sends to an address agreed in
	// advance rather than one discovered from our packets.
	ListenAddress string `json:"listen_address"`
	// NetworkID identifies this server to the far end, in the format of a DMR
	// radio ID. It is stamped into the repeater ID field of every frame sent,
	// which on OpenBridge names the sending server rather than a repeater.
	NetworkID uint32 `json:"network_id"`
	// PassphraseFile holds the shared secret, mode 0600.
	//
	// It is a path for the same reason DMR.PasswordFile is: configuration gets
	// versioned, exported and pasted into support requests, and a secret should
	// not travel with it.
	PassphraseFile string `json:"passphrase_file"`
	// Export names local talkgroups whose traffic is sent upstream.
	//
	// These are the *local* talkgroup and timeslot. QSP moves traffic to TS1
	// on the way out, because proper OpenBridge passes all traffic on TS1 and
	// an administrator should not have to remember that.
	Export []UpstreamTalkgroup `json:"export"`
	// Import names talkgroups accepted from upstream, with the local talkgroup
	// and timeslot they are delivered on.
	//
	// Export and Import are separate lists because they are genuinely different
	// sets: a club may send its own net upstream while accepting a nationwide
	// talkgroup down. Collapsing them makes the asymmetric case unexpressible
	// and the symmetric case look safer than it is.
	Import []UpstreamTalkgroup `json:"import"`
	// StaleAfter is how long without traffic before the link is reported as
	// possibly broken.
	//
	// OpenBridge has no keep-alive, so QSP cannot distinguish a quiet talkgroup
	// from a dead link. Any value here is a guess; the health summary says so
	// rather than claiming to know. Zero disables the warning.
	StaleAfter Duration `json:"stale_after"`
}

// UpstreamTalkgroup is one talkgroup carried over a link, named as it exists
// locally.
type UpstreamTalkgroup struct {
	// Talkgroup is the local talkgroup ID.
	Talkgroup uint32 `json:"talkgroup"`
	// Timeslot is the local timeslot, 1 or 2. Traffic is translated to TS1 for
	// the link itself.
	Timeslot int `json:"timeslot"`
}

// Trigger opens a bridge on demand, when somebody transmits on it.
//
// A scheduled bridge is open because the calendar says so; a triggered bridge is
// open because somebody is using it. Clubs want both.
type Trigger struct {
	// Bridge is the name of the bridge this trigger opens.
	Bridge string `json:"bridge"`
	// On are the endpoints that open it. Usually a subset of the bridge's own
	// endpoints, so a local repeater can open a link outward without the wider
	// network opening it inward.
	On []Endpoint `json:"on"`
	// HangTime is how long the bridge stays open after the last transmission,
	// for example "3m". It must outlast the pauses in a conversation.
	HangTime Duration `json:"hang_time"`
	// Enabled allows a trigger to be suspended without deleting it.
	Enabled bool `json:"enabled"`
}

// Window is a recurring period during which a bridge carries traffic.
//
// Times are local wall-clock times in the named IANA zone, not instants. A net
// at 20:00 happens at 20:00 all year; storing an instant would drag it an hour
// off at each daylight-saving change.
type Window struct {
	// Bridge is the name of the bridge this window enables.
	Bridge string `json:"bridge"`
	// Days are weekdays, 0 for Sunday through 6 for Saturday, in the window's
	// own zone.
	Days []int `json:"days"`
	// Start is the local time of day in 24-hour HH:MM, for example "20:00".
	Start string `json:"start"`
	// Duration is how long the window stays open, for example "1h".
	Duration Duration `json:"duration"`
	// Timezone is an IANA name such as "America/Chicago". Abbreviations like
	// "CST" are not accepted: they cannot express "20:00 local all year".
	Timezone string `json:"timezone"`
	// Enabled allows a window to be suspended without deleting it.
	Enabled bool `json:"enabled"`
}

// Bridge joins endpoints so that traffic arriving at one reaches the others.
type Bridge struct {
	// Name identifies the bridge to an operator. It must be unique.
	Name string `json:"name"`
	// Enabled reports whether the bridge is currently carrying traffic.
	// Scheduled bridging will flip this rather than adding a mechanism.
	Enabled bool `json:"enabled"`
	// Endpoints are the places this bridge joins. At least two are required.
	Endpoints []Endpoint `json:"endpoints"`
}

// Join describes what a club member needs in order to point a hotspot at this
// network, and is served by GET /api/join.
//
// This is configuration rather than something QSP derives, and the reason is
// the whole difficulty of onboarding: the number a member dials is rewritten
// by their own hotspot before QSP ever sees it. QSP knows only the arriving
// talkgroup. Only the admin, who has seen the TGRewrite lines in
// /etc/dmrgateway, knows both halves — and a member told only one of them
// reaches nothing and concludes the software is broken.
//
// The shared peer password is deliberately absent. Everything here is safe to
// show a club's members; the password is not, and it travels separately.
type Join struct {
	// NetworkName is what the club calls this network.
	NetworkName string `json:"network_name"`
	// Address is the host or IP a member's hotspot should point at. Left empty
	// when the admin has not said; the page then asks them to ask, rather than
	// guessing at an address that may be a container's.
	Address string `json:"address"`
	// Talkgroups are the ones members may use.
	Talkgroups []JoinTalkgroup `json:"talkgroups"`
}

// JoinTalkgroup is one talkgroup as a member must dial it.
type JoinTalkgroup struct {
	// Name is what the club calls it, in plain words.
	Name string `json:"name"`
	// Dialled is the number entered into the radio.
	Dialled uint32 `json:"dialled"`
	// Arrives is the talkgroup it becomes by the time QSP sees it. Zero means
	// the hotspot does not rewrite it.
	Arrives uint32 `json:"arrives,omitempty"`
	// Timeslot is 1 or 2.
	Timeslot int `json:"timeslot"`
}

// Target returns the talkgroup QSP will actually receive.
func (t JoinTalkgroup) Target() uint32 {
	if t.Arrives != 0 {
		return t.Arrives
	}
	return t.Dialled
}

// Endpoint is one talkgroup on one timeslot at one peer.
type Endpoint struct {
	// Peer is the peer's repeater ID, or 0 for every connected peer.
	Peer uint32 `json:"peer"`
	// Talkgroup is the talkgroup ID.
	Talkgroup uint32 `json:"talkgroup"`
	// Timeslot is 1 or 2.
	Timeslot int `json:"timeslot"`
}

// Server configures the HTTP console listener.
type Server struct {
	// ListenAddress is the host:port the console binds to.
	ListenAddress string `json:"listen_address"`
	// ReadHeaderTimeout bounds how long a client may take to send request
	// headers. It is the primary defence against slow-header denial of service.
	ReadHeaderTimeout Duration `json:"read_header_timeout"`
	// ReadTimeout bounds the total time to read a request.
	ReadTimeout Duration `json:"read_timeout"`
	// WriteTimeout bounds the total time to write a response. Server-Sent
	// Events connections are exempt; see the server package.
	WriteTimeout Duration `json:"write_timeout"`
	// IdleTimeout bounds how long a keep-alive connection may sit unused.
	IdleTimeout Duration `json:"idle_timeout"`
	// ShutdownTimeout bounds graceful shutdown before connections are forced closed.
	ShutdownTimeout Duration `json:"shutdown_timeout"`
	// BehindProxy indicates the console is served behind a reverse proxy that
	// terminates TLS. It affects which forwarding headers are trusted.
	BehindProxy bool `json:"behind_proxy"`
}

// Database configures persistent storage.
type Database struct {
	// Driver is the database/sql driver name to open. It must already be
	// registered by the binary; see docs/adr/ADR-0005.
	Driver string `json:"driver"`
	// DSN is the data source name passed to the driver.
	DSN string `json:"dsn"`
	// MaxOpenConns bounds concurrent connections. SQLite tolerates many
	// readers but exactly one writer, so this is deliberately small.
	MaxOpenConns int `json:"max_open_conns"`
	// ConnMaxLifetime bounds how long a pooled connection is reused.
	ConnMaxLifetime Duration `json:"conn_max_lifetime"`
	// BusyTimeout is how long SQLite waits for a lock before returning busy.
	BusyTimeout Duration `json:"busy_timeout"`
}

// Logging configures the structured logger.
type Logging struct {
	// Level is one of debug, info, warn, error.
	Level string `json:"level"`
	// Format is one of text, json.
	Format string `json:"format"`
	// IncludeSource adds file and line to each record. Useful in development,
	// costly in hot paths.
	IncludeSource bool `json:"include_source"`
}

// Events configures the internal event bus.
type Events struct {
	// HistorySize is how many past events are retained for reconnecting
	// console clients. Larger values tolerate longer disconnections at the
	// cost of memory.
	HistorySize int `json:"history_size"`
	// SubscriberBuffer is the per-client queue depth. A client that exceeds it
	// is marked lagged and must resynchronise.
	SubscriberBuffer int `json:"subscriber_buffer"`
}

// Default returns the recommended configuration.
//
// Every value here is the one the console displays as "recommended" and
// restores when an operator clicks restore-to-default.
func Default() Config {
	return Config{
		Version: SchemaVersion,
		Server: Server{
			ListenAddress:     "127.0.0.1:8080",
			ReadHeaderTimeout: Duration(5 * time.Second),
			ReadTimeout:       Duration(30 * time.Second),
			WriteTimeout:      Duration(30 * time.Second),
			IdleTimeout:       Duration(120 * time.Second),
			ShutdownTimeout:   Duration(15 * time.Second),
			BehindProxy:       false,
		},
		Database: Database{
			Driver:          "sqlite",
			DSN:             "qsp.db",
			MaxOpenConns:    4,
			ConnMaxLifetime: Duration(time.Hour),
			BusyTimeout:     Duration(5 * time.Second),
		},
		Logging: Logging{
			Level:         "info",
			Format:        "text",
			IncludeSource: false,
		},
		Events: Events{
			HistorySize:      256,
			SubscriberBuffer: 64,
		},
		DMR: DMR{
			// Off by default. A freshly installed QSP must not start accepting
			// connections before an operator has decided it should.
			Enabled:       false,
			ListenAddress: "0.0.0.0:62031",
			PasswordFile:  "",
			PeerTimeout:   Duration(60 * time.Second),
			LoginTimeout:  Duration(30 * time.Second),
			MaxPeers:      200,
			Forwarding:    false,
			Bridges:       nil,
			Triggers:      nil,
			Schedule:      nil,
		},
	}
}

// Duration is a time.Duration that serialises to and from a human-readable
// string such as "30s". Storing raw nanoseconds in a document an operator may
// read or export would be needlessly hostile.
type Duration time.Duration

// AsDuration converts back to time.Duration.
func (d Duration) AsDuration() time.Duration { return time.Duration(d) }

// String implements fmt.Stringer.
func (d Duration) String() string { return time.Duration(d).String() }

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements json.Unmarshaler, accepting only a duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string such as \"30s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("cannot parse duration %q: use a value such as \"500ms\", \"30s\" or \"1h\"", s)
	}
	*d = Duration(parsed)
	return nil
}

// FieldError describes one invalid field.
//
// Field is the JSON path an operator would recognise. Problem states what is
// wrong. Fix states what to do about it. Constitution §12 forbids errors that
// say only that a value is invalid.
type FieldError struct {
	Field   string
	Problem string
	Fix     string
}

// Error implements error.
func (e FieldError) Error() string {
	return fmt.Sprintf("%s: %s (%s)", e.Field, e.Problem, e.Fix)
}

// ValidationError aggregates every problem found in one configuration.
type ValidationError struct {
	Errors []FieldError
}

// Error implements error.
func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return "invalid configuration: " + e.Errors[0].Error()
	}
	parts := make([]string, 0, len(e.Errors))
	for _, fe := range e.Errors {
		parts = append(parts, fe.Error())
	}
	return fmt.Sprintf("invalid configuration (%d problems): %s", len(e.Errors), strings.Join(parts, "; "))
}

// Fields returns the JSON paths of every invalid field, sorted.
func (e *ValidationError) Fields() []string {
	out := make([]string, 0, len(e.Errors))
	for _, fe := range e.Errors {
		out = append(out, fe.Field)
	}
	sort.Strings(out)
	return out
}

type validator struct{ errs []FieldError }

func (v *validator) add(field, problem, fix string) {
	v.errs = append(v.errs, FieldError{Field: field, Problem: problem, Fix: fix})
}

func (v *validator) positive(field string, value int, fix string) {
	if value <= 0 {
		v.add(field, fmt.Sprintf("must be greater than zero, got %d", value), fix)
	}
}

func (v *validator) positiveDuration(field string, value Duration, fix string) {
	if value <= 0 {
		v.add(field, fmt.Sprintf("must be greater than zero, got %s", value), fix)
	}
}

// Validate reports every problem with c.
//
// It returns nil when the configuration is usable. A non-nil result is always
// of type *ValidationError.
func (c Config) Validate() error {
	v := &validator{}

	switch {
	case c.Version <= 0:
		v.add("version", fmt.Sprintf("must be a positive schema version, got %d", c.Version),
			fmt.Sprintf("set it to %d", SchemaVersion))
	case c.Version > SchemaVersion:
		v.add("version", fmt.Sprintf("document is version %d but this build understands up to %d", c.Version, SchemaVersion),
			"upgrade QSP, or restore a configuration version created by this build")
	}

	if strings.TrimSpace(c.Server.ListenAddress) == "" {
		v.add("server.listen_address", "must not be empty",
			"use \"127.0.0.1:8080\" for local access, or \"0.0.0.0:8080\" to listen on every interface")
	} else if _, _, err := net.SplitHostPort(c.Server.ListenAddress); err != nil {
		v.add("server.listen_address", fmt.Sprintf("%q is not a host:port address", c.Server.ListenAddress),
			"include a port, for example \"127.0.0.1:8080\"")
	}

	v.positiveDuration("server.read_header_timeout", c.Server.ReadHeaderTimeout,
		"use \"5s\"; this bounds slow-header denial of service and must not be disabled")
	v.positiveDuration("server.read_timeout", c.Server.ReadTimeout, "use \"30s\"")
	v.positiveDuration("server.write_timeout", c.Server.WriteTimeout, "use \"30s\"")
	v.positiveDuration("server.idle_timeout", c.Server.IdleTimeout, "use \"120s\"")
	v.positiveDuration("server.shutdown_timeout", c.Server.ShutdownTimeout,
		"use \"15s\"; this bounds how long in-flight requests may finish during shutdown")

	if strings.TrimSpace(c.Database.Driver) == "" {
		v.add("database.driver", "must not be empty",
			"use \"sqlite\"; the driver must be registered in the binary")
	}
	if strings.TrimSpace(c.Database.DSN) == "" {
		v.add("database.dsn", "must not be empty",
			"use \"qsp.db\" for a file in the working directory, or an absolute path")
	}
	v.positive("database.max_open_conns", c.Database.MaxOpenConns,
		"use 4; SQLite permits many readers but only one writer, so a large pool does not help")
	v.positiveDuration("database.conn_max_lifetime", c.Database.ConnMaxLifetime, "use \"1h\"")
	v.positiveDuration("database.busy_timeout", c.Database.BusyTimeout,
		"use \"5s\"; this is how long a query waits for a write lock before failing")

	if _, err := parseLevel(c.Logging.Level); err != nil {
		v.add("logging.level", fmt.Sprintf("%q is not a recognised level", c.Logging.Level),
			"use \"debug\", \"info\", \"warn\" or \"error\"")
	}
	if _, err := parseFormat(c.Logging.Format); err != nil {
		v.add("logging.format", fmt.Sprintf("%q is not a recognised format", c.Logging.Format),
			"use \"text\" for interactive use or \"json\" for production")
	}

	if c.DMR.Enabled {
		if strings.TrimSpace(c.DMR.ListenAddress) == "" {
			v.add("dmr.listen_address", "must not be empty when the DMR listener is enabled",
				"use \"0.0.0.0:62031\" to accept peers on every interface")
		} else if _, _, err := net.SplitHostPort(c.DMR.ListenAddress); err != nil {
			v.add("dmr.listen_address", fmt.Sprintf("%q is not a host:port address", c.DMR.ListenAddress),
				"include a port, for example \"0.0.0.0:62031\"")
		}
		if strings.TrimSpace(c.DMR.PasswordFile) == "" {
			v.add("dmr.password_file", "must not be empty when the DMR listener is enabled",
				"create a file containing the shared peer password, mode 0600, and give its path here; "+
					"the password is never stored in this configuration")
		}
		v.positiveDuration("dmr.peer_timeout", c.DMR.PeerTimeout,
			"use \"60s\"; peers send a keepalive every 10 s, so this tolerates five losses")
		v.positiveDuration("dmr.login_timeout", c.DMR.LoginTimeout,
			"use \"30s\"; this bounds how long an unfinished login may hold a slot")
		v.positive("dmr.max_peers", c.DMR.MaxPeers,
			"use 200; this bounds memory and the size of a routing decision")

		names := make(map[string]bool, len(c.DMR.Bridges))
		for i, b := range c.DMR.Bridges {
			field := fmt.Sprintf("dmr.bridges[%d]", i)
			if strings.TrimSpace(b.Name) == "" {
				v.add(field+".name", "must not be empty",
					"give the bridge a name you will recognise on the console, such as \"tuesday-net\"")
			} else if key := strings.ToLower(strings.TrimSpace(b.Name)); names[key] {
				v.add(field+".name", fmt.Sprintf("%q is used by more than one bridge", b.Name),
					"bridge names must be unique; rename one of them")
			} else {
				names[key] = true
			}
			if len(b.Endpoints) < 2 {
				v.add(field+".endpoints", fmt.Sprintf("has %d endpoint(s)", len(b.Endpoints)),
					"a bridge needs at least 2 endpoints to connect anything")
			}
			for j, e := range b.Endpoints {
				ef := fmt.Sprintf("%s.endpoints[%d]", field, j)
				if e.Talkgroup == 0 {
					v.add(ef+".talkgroup", "must not be 0",
						"use the talkgroup number, for example 3148")
				}
				if e.Timeslot != 1 && e.Timeslot != 2 {
					v.add(ef+".timeslot", fmt.Sprintf("is %d", e.Timeslot),
						"DMR has two timeslots; use 1 or 2")
				}
			}
		}

		for i, tr := range c.DMR.Triggers {
			field := fmt.Sprintf("dmr.triggers[%d]", i)
			if strings.TrimSpace(tr.Bridge) == "" {
				v.add(field+".bridge", "must name the bridge this trigger opens",
					"use the name of one of the bridges in dmr.bridges")
			} else if !names[strings.ToLower(strings.TrimSpace(tr.Bridge))] {
				v.add(field+".bridge", fmt.Sprintf("%q does not match any configured bridge", tr.Bridge),
					"check the spelling against dmr.bridges")
			}
			if len(tr.On) == 0 {
				v.add(field+".on", "is empty, so nothing could ever open the bridge",
					"list the endpoints that should open it, usually the local repeater's talkgroup")
			}
			for j, e := range tr.On {
				ef := fmt.Sprintf("%s.on[%d]", field, j)
				if e.Talkgroup == 0 {
					v.add(ef+".talkgroup", "must not be 0", "use the talkgroup number, for example 3148")
				}
				if e.Timeslot != 1 && e.Timeslot != 2 {
					v.add(ef+".timeslot", fmt.Sprintf("is %d", e.Timeslot), "DMR has two timeslots; use 1 or 2")
				}
			}
			if tr.HangTime < 0 {
				v.add(field+".hang_time", "must not be negative", "use \"3m\", or omit it for the default")
			}
		}

		// Upstreams. Each link puts a club's audio on somebody else's network,
		// so the errors here name the consequence rather than the field.
		upstreamNames := make(map[string]bool, len(c.DMR.Upstreams))
		for i, u := range c.DMR.Upstreams {
			field := fmt.Sprintf("dmr.upstreams[%d]", i)

			if strings.TrimSpace(u.Name) == "" {
				v.add(field+".name", "must not be empty",
					"name it after the network it reaches, such as \"brandmeister\"")
			} else if key := strings.ToLower(strings.TrimSpace(u.Name)); upstreamNames[key] {
				v.add(field+".name", fmt.Sprintf("%q is used by more than one upstream", u.Name),
					"names appear in the health report; give each link a distinct one")
			} else {
				upstreamNames[key] = true
			}

			// A disabled link is a note about intent, not a live connection, so
			// only its name is checked. Requiring a passphrase file for a link
			// switched off would stop an operator from writing down a
			// configuration before they have been granted the bridge.
			if !u.Enabled {
				continue
			}

			if strings.TrimSpace(u.Address) == "" {
				v.add(field+".address", "must not be empty when the link is enabled",
					"use the far end's host:port, such as \"3102.master.brandmeister.network:62035\"")
			} else if _, _, err := net.SplitHostPort(u.Address); err != nil {
				v.add(field+".address", fmt.Sprintf("%q is not host:port", u.Address),
					"OpenBridge conventionally uses port 62035")
			}

			if strings.TrimSpace(u.ListenAddress) == "" {
				v.add(field+".listen_address", "must not be empty when the link is enabled",
					"OpenBridge has no connection setup, so the far end sends to an address "+
						"agreed in advance; use \"0.0.0.0:62035\"")
			} else if _, _, err := net.SplitHostPort(u.ListenAddress); err != nil {
				v.add(field+".listen_address", fmt.Sprintf("%q is not host:port", u.ListenAddress),
					"use \"0.0.0.0:62035\"")
			}

			if u.NetworkID == 0 {
				v.add(field+".network_id", "must not be 0",
					"use the DMR ID the far end expects; it identifies this server in every frame sent")
			}

			if strings.TrimSpace(u.PassphraseFile) == "" {
				v.add(field+".passphrase_file", "must not be empty when the link is enabled",
					"create a file containing the passphrase agreed with the far end, mode 0600, "+
						"and give its path here")
			}

			if len(u.Export) == 0 && len(u.Import) == 0 {
				v.add(field, "carries no talkgroups in either direction",
					"add an entry to \"export\" or \"import\"; a link with neither is enabled "+
						"but does nothing")
			}

			if u.StaleAfter < 0 {
				v.add(field+".stale_after", "must not be negative",
					"use \"4h\", or 0 to disable the warning")
			}

			for _, dir := range []struct {
				name string
				tgs  []UpstreamTalkgroup
			}{{"export", u.Export}, {"import", u.Import}} {
				for j, tg := range dir.tgs {
					sub := fmt.Sprintf("%s.%s[%d]", field, dir.name, j)
					if tg.Talkgroup == 0 {
						v.add(sub+".talkgroup", "must not be 0",
							"use the talkgroup as it exists on this network, not as the far end names it")
					}
					if tg.Timeslot != 1 && tg.Timeslot != 2 {
						v.add(sub+".timeslot", fmt.Sprintf("is %d", tg.Timeslot),
							"DMR has two timeslots; use 1 or 2. Traffic is moved to TS1 for the "+
								"link itself, which QSP does for you")
					}
				}
			}
		}

		// Join. What a member is told to dial has to be a talkgroup this
		// instance will actually relay, or they reach silence and blame the
		// software. QSP can check that, and it is the one part of onboarding
		// an admin cannot verify without a second radio.
		reachable := make(map[[2]uint32]bool)
		for _, b := range c.DMR.Bridges {
			for _, e := range b.Endpoints {
				reachable[[2]uint32{e.Talkgroup, uint32(e.Timeslot)}] = true
			}
		}

		seen := make(map[[2]uint32]string, len(c.DMR.Join.Talkgroups))
		for i, tg := range c.DMR.Join.Talkgroups {
			field := fmt.Sprintf("dmr.join.talkgroups[%d]", i)

			if strings.TrimSpace(tg.Name) == "" {
				v.add(field+".name", "must not be empty",
					"name it the way the club refers to it, such as \"Club chat\"")
			}
			if tg.Dialled == 0 {
				v.add(field+".dialled", "must not be 0",
					"use the number a member enters into their radio, which their hotspot may rewrite")
			}
			if tg.Timeslot != 1 && tg.Timeslot != 2 {
				v.add(field+".timeslot", fmt.Sprintf("is %d", tg.Timeslot),
					"DMR has two timeslots; use 1 or 2")
			}

			key := [2]uint32{tg.Target(), uint32(tg.Timeslot)}
			if prior, dup := seen[key]; dup {
				v.add(field, fmt.Sprintf("arrives as TG %d on TS %d, the same as %q",
					tg.Target(), tg.Timeslot, prior),
					"two entries that arrive identically are indistinguishable to a member; remove one")
			}
			seen[key] = tg.Name

			// Only meaningful once bridges exist; an instance with none is
			// observing rather than relaying, and says so elsewhere.
			if len(c.DMR.Bridges) > 0 && tg.Timeslot >= 1 && tg.Timeslot <= 2 &&
				tg.Dialled != 0 && !reachable[key] {
				v.add(field, fmt.Sprintf("tells members to dial %d, arriving as TG %d on TS %d, "+
					"which no bridge carries", tg.Dialled, tg.Target(), tg.Timeslot),
					"add that talkgroup to a bridge, or correct \"arrives\" to match the rewrite "+
						"in the hotspot's DMRGateway configuration")
			}
		}

		for i, w := range c.DMR.Schedule {
			field := fmt.Sprintf("dmr.schedule[%d]", i)
			if strings.TrimSpace(w.Bridge) == "" {
				v.add(field+".bridge", "must name the bridge this window enables",
					"use the name of one of the bridges in dmr.bridges")
			} else if !names[strings.ToLower(strings.TrimSpace(w.Bridge))] {
				v.add(field+".bridge", fmt.Sprintf("%q does not match any configured bridge", w.Bridge),
					"check the spelling against dmr.bridges; a window naming a bridge that does not "+
						"exist would leave you waiting for a net that never links")
			}
			if len(w.Days) == 0 {
				v.add(field+".days", "is empty, so the window would never run",
					"list weekdays as numbers, 0 for Sunday through 6 for Saturday")
			}
			for _, d := range w.Days {
				if d < 0 || d > 6 {
					v.add(field+".days", fmt.Sprintf("contains %d", d),
						"use 0 for Sunday through 6 for Saturday")
					break
				}
			}
			if _, _, err := parseClock(w.Start); err != nil {
				v.add(field+".start", fmt.Sprintf("%q is not a time of day", w.Start),
					"use 24-hour HH:MM, for example \"20:00\"")
			}
			if w.Duration <= 0 {
				v.add(field+".duration", "must be greater than zero",
					"use \"1h\" for a one-hour net")
			}
			if strings.TrimSpace(w.Timezone) == "" {
				v.add(field+".timezone", "must not be empty",
					"use an IANA name such as \"America/Chicago\", not an abbreviation like \"CST\"")
			}
		}

		if c.DMR.Forwarding && len(c.DMR.Bridges) == 0 {
			v.add("dmr.bridges", "forwarding is enabled but no bridges are configured",
				"add at least one bridge, or set dmr.forwarding to false; "+
					"with no bridges QSP accepts traffic and relays none of it")
		}
	}

	v.positive("events.history_size", c.Events.HistorySize,
		"use 256; this is how many events a reconnecting console client can replay")
	v.positive("events.subscriber_buffer", c.Events.SubscriberBuffer,
		"use 64; a client exceeding this is marked lagged and resynchronises")

	if len(v.errs) == 0 {
		return nil
	}
	return &ValidationError{Errors: v.errs}
}

// Load reads and validates a configuration document.
//
// Unknown fields are rejected: a typo in a field name must not silently leave
// the default in place, because the operator would believe a setting had been
// applied when it had not.
func Load(r io.Reader) (Config, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	cfg := Default()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("cannot read configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save writes c as indented JSON.
//
// Save refuses to write an invalid configuration. Persisting something that
// cannot be loaded again would strand the operator.
func Save(w io.Writer, c Config) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("refusing to write an invalid configuration: %w", err)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(c)
}

// parseLevel and parseFormat duplicate the logging package's parsers so that
// config does not depend on logging. The duplication is two switch statements
// and keeps the dependency graph acyclic; both are covered by tests.
func parseLevel(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "info", "warn", "warning", "error":
		return strings.ToLower(strings.TrimSpace(s)), nil
	default:
		return "", fmt.Errorf("unknown level %q", s)
	}
}

func parseFormat(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "text", "json":
		return strings.ToLower(strings.TrimSpace(s)), nil
	default:
		return "", fmt.Errorf("unknown format %q", s)
	}
}

// CheckPeerPasswordMode reports whether a peer password file's permissions are
// tight enough to hold a shared secret.
//
// A password readable by every account on the host is not a shared secret, and
// nothing about that failure is visible: the file works, peers authenticate,
// and the exposure is silent. ssh refuses a private key with loose permissions
// for the same reason, and this follows that precedent rather than warning and
// continuing — a warning in a log nobody reads is not a control.
//
// Only the group and world bits matter. The owner's bits are their business,
// and the execute bit, while meaningless here, is not a disclosure.
//
// This takes a mode rather than a path so that it is testable without touching
// a filesystem, and so the decision about which platforms can enforce it lives
// with the caller. Windows cannot: os.Stat synthesises a mode from the
// read-only attribute and reports 0666 for an ordinary file whatever its ACL
// says, so enforcing this there would reject every correctly secured file.
func CheckPeerPasswordMode(mode fs.FileMode) error {
	if perm := mode.Perm() & 0o077; perm != 0 {
		return fmt.Errorf("the peer password file is mode %#o, readable beyond its owner; "+
			"run chmod 600 on it — a shared secret every account on this host can read "+
			"is not a secret", mode.Perm())
	}
	return nil
}

// LoadPeerPassword reads the shared peer password from DMR.PasswordFile.
//
// The password never enters Config, so it never reaches the version history,
// an export, or a diff. Reading it is a separate, explicit act.
//
// An empty or whitespace-only file is an error: it would otherwise authenticate
// every peer that guessed an empty password.
func LoadPeerPassword(read func(string) ([]byte, error), path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("no peer password file is configured; set dmr.password_file")
	}
	raw, err := read(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the peer password file %q: %w "+
			"(create it with the shared password as its only contents, mode 0600)", path, err)
	}
	password := strings.TrimSpace(string(raw))
	if password == "" {
		return nil, fmt.Errorf("the peer password file %q is empty; "+
			"put the shared password in it, or no peer can be authenticated safely", path)
	}
	return []byte(password), nil
}

// parseClock reads a 24-hour HH:MM time of day.
//
// It duplicates the scheduler's parser so that config depends on nothing, in
// keeping with the dependency rule that config imports only the standard
// library. Both are covered by tests.
func parseClock(s string) (hour, minute int, err error) {
	trimmed := strings.TrimSpace(s)
	if _, err := fmt.Sscanf(trimmed, "%d:%d", &hour, &minute); err != nil {
		return 0, 0, fmt.Errorf("cannot read %q as HH:MM", s)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("%q is not a valid time of day", s)
	}
	return hour, minute, nil
}
