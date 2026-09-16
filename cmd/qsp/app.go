package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"os"
	"path/filepath"
	"syscall"

	"github.com/k9mls/qsp/console"
	"github.com/k9mls/qsp/internal/ambe"
	"github.com/k9mls/qsp/internal/audio"
	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/buildinfo"
	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/callsigns"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/database"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/ipsclink"
	"github.com/k9mls/qsp/internal/p25link"
	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/homebrew"
	"github.com/k9mls/qsp/internal/routing"
	"github.com/k9mls/qsp/internal/scheduler"
	"github.com/k9mls/qsp/internal/secrets"
	"github.com/k9mls/qsp/internal/server"
	"github.com/k9mls/qsp/internal/upstream"
	"github.com/k9mls/qsp/internal/vocoderlink"
	"github.com/k9mls/qsp/internal/zellologon"
)

// app holds the constructed dependency graph.
//
// Dependencies are wired here by constructor injection and passed explicitly.
// There is no global state and no container: the graph is small enough to read
// top to bottom, and keeping it that way is a design goal rather than an
// accident.
type app struct {
	cfg       config.Config
	log       *slog.Logger
	bus       *events.Bus
	db        *database.DB
	audit     audit.Recorder
	secrets   *secrets.Store
	callStore *calls.Store
	srv       *server.Server
	dmr       *peers.Listener
	ipsc      *ipsclink.Listener
	p25       *p25link.Listener
	health    *health.Registry
	upstreams *upstream.Set

	// vocoders supervises the configured transcoder channels.
	//
	// A supervisor rather than a startup step because a dongle is a thing
	// that gets unplugged — its documented recovery path is removing its
	// power for ten seconds.
	vocoders *ambe.Supervisor
	// transcoding carries routed calls through those chips and out as USRP.
	// Nil when no transcoder is enabled.
	transcoding *vocoderlink.Set
	// transcodingChannels are the same channels, for their health checks.
	transcodingChannels []*vocoderlink.Channel
	// zelloLogon hands qsp-zello a logon (ADR-0066). Nil when
	// zello.logon_socket is empty.
	zelloLogon *zellologon.Server
	// auth is the concrete login service, kept alongside the interface the
	// server holds because the session sweep is not something an HTTP handler
	// ever needs and does not belong on that interface.
	auth *auth.Service
	// configManager holds the running configuration and saves a new one.
	configManager *configManager
	// names resolves radio IDs against the amateur DMR registry. Nil when
	// lookups are off, which is the default.
	names *callsigns.Service
	// master authenticates peers. Kept so the console can report what it is
	// refusing.
	master *peers.Master
	// closers are run in reverse order during shutdown.
	closers []func(context.Context) error
}

// build constructs every subsystem.
//
// The database is optional. This binary registers modernc.org/sqlite (see
// driver_sqlite.go and docs/adr/ADR-0005), so the default configuration opens a
// database and migrates it. A configuration naming some other driver still
// fails with ErrDriverNotRegistered, which is a declared condition rather than a
// fault: startup continues and the health check reports the database as
// unavailable with the reason. Any other database error is fatal.
func build(ctx context.Context, cfg config.Config, configPath string, log *slog.Logger) (*app, error) {
	a := &app{cfg: cfg, log: log}

	a.bus = events.NewBus(log, events.Options{
		HistorySize:      cfg.Events.HistorySize,
		SubscriberBuffer: cfg.Events.SubscriberBuffer,
	})
	a.closers = append(a.closers, func(context.Context) error {
		a.bus.Close()
		return nil
	})

	// The log first, so events recorded before the database opens still reach
	// somewhere. The database is added below when it is available.
	trail := audit.NewMulti(log, audit.NewLogRecorder(log))
	a.audit = trail

	var dbUnavailableReason string
	var callStore *calls.Store
	db, err := database.Open(ctx, log, database.Options{
		Driver:          cfg.Database.Driver,
		DSN:             cfg.Database.DSN,
		BusyTimeout:     cfg.Database.BusyTimeout.AsDuration(),
		MaxOpenConns:    cfg.Database.MaxOpenConns,
		ConnMaxLifetime: cfg.Database.ConnMaxLifetime.AsDuration(),
	})
	switch {
	case errors.Is(err, database.ErrDriverNotRegistered):
		dbUnavailableReason = fmt.Sprintf(
			"no %q driver is registered in this build; persistence is disabled (see docs/adr/ADR-0005)",
			cfg.Database.Driver)
		log.Warn("running without persistence", slog.String("reason", dbUnavailableReason))
	case err != nil:
		return nil, err
	default:
		a.db = db
		a.closers = append(a.closers, func(context.Context) error { return db.Close() })

		result, migrateErr := db.Migrate(ctx)
		if migrateErr != nil {
			return nil, migrateErr
		}
		log.Info("schema ready",
			slog.Int("schema_version", result.SchemaVersion),
			slog.Int("applied_now", len(result.Applied)),
			slog.Int("already_applied", result.AlreadyApplied),
		)

		// **Nothing did this.** Migration 0002 created audit_events with its
		// indexes, the schema carried it to version 4, SECURITY.md described a
		// trail that settles arguments between administrators, and
		// LogRecorder was the only implementation of audit.Recorder in the
		// program. An instance running for weeks held zero rows and could not
		// have held any.
		trail.Add(audit.NewSQLRecorder(db.SQL(), log))
		log.Info("audit trail persisted", slog.String("table", "audit_events"))

		callStore = calls.NewStore(db.SQL(), log, cfg.DMR.Calls.Retain.AsDuration())
		a.callStore = callStore
		if callStore.Enabled() {
			// **Pruned at startup and then on a timer**, not on every write:
			// deleting on each insert makes every transmission pay for the
			// retention policy. A row outliving its window by an hour matters
			// to nobody. See ADR-0033.
			if n, err := callStore.Prune(ctx, time.Now().UTC()); err != nil {
				log.Warn("cannot prune the call history", "error", err)
			} else if n > 0 {
				log.Info("pruned the call history", slog.Int64("removed", n))
			}
			log.Info("call history persisted",
				slog.String("table", "calls"),
				slog.String("retain", cfg.DMR.Calls.Retain.AsDuration().String()))
		} else {
			log.Info("call history is not kept; set dmr.calls.retain to keep one")
		}
	}

	// **Before anything that reads it.** This was built after the console's
	// view source captured a.names, so the view held nil, no radio ID was ever
	// queued, and the cache stayed empty on a working instance with lookups
	// enabled and logging that they were.
	// Credentials an operator types into the console.
	//
	// **Only with a database**, because there is nowhere else to keep them —
	// and the endpoints say so rather than accepting a password and storing
	// nothing. The key lives beside the database file for the same reason the
	// database does: one directory to back up, one to give the right
	// permissions.
	if a.db != nil {
		keyPath := filepath.Join(filepath.Dir(cfg.Database.DSN), "secrets.key")
		store, serr := secrets.Open(secrets.Options{
			DB:      a.db.SQL(),
			KeyPath: keyPath,
		})
		if serr != nil {
			return nil, serr
		}
		a.secrets = store
		log.Info("credential store ready", "key", keyPath)
	}

	// **A logon socket with nowhere to read credentials from is refused at
	// startup**, not served as a stream of refusals: an operator who set
	// zello.logon_socket meant the connector to work.
	if sock := strings.TrimSpace(cfg.Zello.LogonSocket); sock != "" {
		if a.secrets == nil {
			return nil, fmt.Errorf("zello.logon_socket is set and there is no database to keep " +
				"the Zello credentials in; configure database.dsn or remove zello.logon_socket")
		}
		srv, zerr := zellologon.Listen(zellologon.Options{
			SocketPath: sock,
			Store:      a.secrets,
			Issuer:     cfg.Zello.Issuer,
			Audience:   cfg.Zello.Audience,
			Log:        log,
		})
		if zerr != nil {
			return nil, zerr
		}
		a.zelloLogon = srv
		log.Info("serving Zello logons", slog.String("socket", sock))
	}

	// Radio ID lookups. Off unless configured, and refused without a contact
	// address — the registry asks automated clients to identify themselves and
	// QSP has no business inventing one. See ADR-0030.
	if cfg.DMR.Callsigns.Enabled {
		var store callsigns.Store
		if a.db != nil {
			cs, serr := callsigns.NewSQLStore(a.db.SQL())
			if serr != nil {
				return nil, serr
			}
			store = cs
		}
		resolver, rerr := callsigns.New(callsigns.Options{
			Contact: cfg.DMR.Callsigns.Contact,
		}, store)
		if rerr != nil {
			return nil, rerr
		}
		fetcher, ferr := callsigns.NewHTTPFetcher(buildVersion(), cfg.DMR.Callsigns.Contact)
		if ferr != nil {
			return nil, ferr
		}
		a.names = callsigns.NewService(log, resolver, fetcher, store)
		log.Info("radio ID lookups enabled",
			slog.String("registry", callsigns.Endpoint),
			slog.String("contact", cfg.DMR.Callsigns.Contact),
		)
	}

	master, dmrDisabledReason, err := buildDMR(cfg, log, a.bus)
	if err != nil {
		return nil, err
	}
	if master != nil {
		var core *routing.Core
		sched, serr := buildSchedule(cfg)
		if serr != nil {
			return nil, serr
		}
		triggers, terr2 := buildTriggers(cfg)
		if terr2 != nil {
			return nil, terr2
		}
		if cfg.DMR.Forwarding {
			table, terr := buildTable(cfg, sched, triggers, time.Now())
			if terr != nil {
				return nil, terr
			}
			if sched != nil {
				log.Info("schedule loaded",
					slog.Int("windows", len(cfg.DMR.Schedule)),
					slog.Any("scheduled_bridges", sched.Bridges()),
				)
				for _, occ := range sched.Preview(time.Now(), 1) {
					if occ.Skipped {
						log.Warn("a scheduled occurrence will be skipped",
							slog.String("bridge", occ.Bridge),
							slog.String("when", occ.LocalStart),
							slog.String("reason", occ.Note),
						)
						continue
					}
					log.Info("next scheduled window",
						slog.String("bridge", occ.Bridge),
						slog.String("starts", occ.LocalStart),
					)
				}
			}
			// Recomputed rather than threaded out of buildDMR: it is a pure
			// function of the configuration, and returning it would widen
			// buildDMR's signature for one caller. Validate has already
			// accepted the same document, so an error here means the two
			// disagree and is worth failing on rather than defaulting to
			// lists that permit everything.
			lists, aerr := cfg.AccessLists()
			if aerr != nil {
				return nil, fmt.Errorf("cannot apply the access lists: %w", aerr)
			}
			core, err = routing.NewCore(routing.CoreOptions{
				Access: lists,
				Table:  table,
				Peers:  readyPeers{master: master},
				// Where radios are, so a private call can reach one. The
				// master learns this from traffic; see ADR-0021.
				Subscribers: master,
				// Which talkgroups each peer wants; see ADR-0023.
				Attached: master,
				// Links to other QSP servers, which repeat reaches the way it
				// reaches a hotspot; see ADR-0051.
				QSPLinks: qspLinkNames(cfg),
			})
			if err != nil {
				return nil, err
			}
			log.Info("forwarding enabled; peers on a talkgroup hear each other",
				slog.Int("bridges", len(cfg.DMR.Bridges)),
				slog.Int("enabled_bridges", table.EnabledCount()),
				slog.Int("qsp_links", len(qspLinkNames(cfg))),
			)
		} else {
			log.Info("forwarding disabled; traffic is observed and not relayed")
		}

		// Off unless configured, because parrot swallows a talkgroup and one
		// QSP takes is one the operator did not choose to lose.
		var parrotRecorder *parrot.Recorder
		if cfg.DMR.Parrot.Enabled {
			slot := hbp.Timeslot1
			if cfg.DMR.Parrot.Timeslot == 2 {
				slot = hbp.Timeslot2
			}
			rec, perr := parrot.New(parrot.Config{
				Talkgroup:   cfg.DMR.Parrot.Talkgroup,
				Timeslot:    slot,
				MaxDuration: cfg.DMR.Parrot.MaxDuration.AsDuration(),
				Gap:         cfg.DMR.Parrot.Gap.AsDuration(),
			})
			if perr != nil {
				return nil, perr
			}
			parrotRecorder = rec
			log.Info("parrot enabled",
				slog.Uint64("talkgroup", uint64(cfg.DMR.Parrot.Talkgroup)),
				slog.String("timeslot", slot.String()),
			)
		}

		links, lerr := buildUpstreams(log, cfg, func(name string, frame hbp.Data) {
			// Resolved at call time rather than captured: the listener does not
			// exist yet. Links are not started until run(), by which point
			// a.dmr is set and never written again.
			if a.dmr != nil {
				a.dmr.DeliverFromUpstream(name, frame)
			}
		})
		if lerr != nil {
			return nil, lerr
		}
		a.upstreams = links
		// Kept so the console can report what the master is refusing: an
		// operator should learn about a run of failed logins from the page
		// rather than from a member's phone call.
		a.master = master

		// **Seeded from the record, so Last heard is not empty after a deploy.**
		// ADR-0033 kept completed calls and gave them their own page, which is
		// not the same thing: the panel an operator actually looks at still
		// began at nothing on every restart.
		tracker := calls.NewTracker(calls.Options{})
		if callStore != nil && callStore.Enabled() {
			recent, serr := callStore.Since(ctx, time.Now().UTC().Add(-24*time.Hour), calls.DefaultHistory)
			if serr != nil {
				log.Warn("cannot seed last heard from the record", "error", serr)
			} else if len(recent) > 0 {
				tracker.Seed(recent)
				log.Info("last heard seeded from the record", slog.Int("calls", len(recent)))
			}
		}

		// **The master already implements all of this and was never told to.**
		// internal/peers/attachments.go has static and dynamic attachment, the
		// timeout, expiry and the Attached the routing core consults — and
		// SetSubscription had no caller anywhere in the program, so every peer
		// received every talkgroup regardless of configuration. The same shape
		// as the audit trail: built, wired, and never switched on.
		if cfg.DMR.Subscription.Enabled {
			static := make([]peers.Attachment, 0, len(cfg.DMR.Subscription.Static))
			for _, a := range cfg.DMR.Subscription.Static {
				static = append(static, peers.Attachment{
					Peer:      hbp.RepeaterID(a.Peer),
					Talkgroup: a.Talkgroup,
					Timeslot:  hbp.Timeslot(a.Timeslot),
					Static:    true,
				})
			}
			master.SetSubscription(peers.SubscriptionConfig{
				Enabled: true,
				Timeout: cfg.DMR.Subscription.Timeout.AsDuration(),
				Static:  static,
			})
			log.Info("per-peer talkgroup attachment enabled",
				slog.String("timeout", cfg.DMR.Subscription.Timeout.AsDuration().String()),
				slog.Int("static", len(static)),
				slog.Int("unlink_talkgroup", int(cfg.DMR.Subscription.Unlink)))
		}

		// Vocoder channels, built before the listener because the listener
		// hands them frames. Only enabled transcoders: a disabled one is a
		// line in a document and not a thing to go looking for.
		//
		// **The USRP socket is bound here, at startup**, so a port already
		// taken fails the start with the address in the message rather than
		// surfacing as a transcoder that never carries anything.
		var vocoders []ambe.Vocoder
		for _, t := range cfg.DMR.Transcoders {
			if !t.Enabled {
				continue
			}
			vocoders = append(vocoders, ambe.Vocoder{
				Name:    t.Name,
				Address: t.Address,
				Rate:    t.RateIndex(),
			})
		}
		var transcoders peers.TranscoderSender
		if len(vocoders) > 0 {
			a.vocoders = ambe.NewSupervisor(log, vocoders)
			for _, t := range cfg.DMR.Transcoders {
				if !t.Enabled {
					continue
				}
				conn, uerr := audio.Listen(t.USRPListen, t.USRPPeer)
				if uerr != nil {
					return nil, fmt.Errorf("transcoder %q: %w", t.Name, uerr)
				}
				a.closers = append(a.closers, func(context.Context) error { return conn.Close() })
				// What a transmission built from USRP audio is sent on: this
				// transcoder's own endpoint in the first bridge naming it, so
				// routing recognises the traffic as that bridge's. Validation
				// already refuses an enabled transcoder no bridge names.
				talkgroup, timeslot := transcoderEndpoint(cfg, t.Name)
				name := t.Name
				ch, cerr := vocoderlink.New(vocoderlink.Options{
					Name:      t.Name,
					Chip:      vocoderlink.SupervisedChip(a.vocoders, t.Name),
					Radio:     conn,
					Log:       log,
					RadioID:   t.RadioID,
					Talkgroup: talkgroup,
					Timeslot:  timeslot,
					// Resolved at call time: the listener is built just below,
					// and channels do not run until run(), by which point a.dmr
					// is set and never written again.
					Deliver: func(frame hbp.Data) {
						if a.dmr != nil {
							a.dmr.DeliverFromTranscoder(name, frame)
						}
					},
				})
				if cerr != nil {
					return nil, fmt.Errorf("transcoder %q: %w", t.Name, cerr)
				}
				a.transcodingChannels = append(a.transcodingChannels, ch)
				log.Info("transcoder configured",
					slog.String("transcoder", t.Name),
					slog.String("vocoder", t.Address),
					slog.String("usrp_listen", t.USRPListen),
					slog.String("usrp_peer", t.USRPPeer))
			}
			a.transcoding = vocoderlink.NewSet(a.transcodingChannels...)
			// Assigned only when non-nil: a nil *Set in the interface would
			// be a non-nil sender, and the listener would stop reporting that
			// nothing receives transcoder frames.
			transcoders = a.transcoding
		}

		listener, lerr := peers.NewListener(log, peers.ListenerConfig{
			ListenAddress: cfg.DMR.ListenAddress,
			Master:        master,
			Bus:           a.bus,
			Calls:         tracker,
			CallStore:     callStore,
			UnlinkTalkgroup: func() uint32 {
				if !cfg.DMR.Subscription.Enabled {
					return 0
				}
				return cfg.DMR.Subscription.Unlink
			}(),
			UnlinkTimeslot: cfg.DMR.Subscription.UnlinkTimeslot,
			Parrot:         parrotRecorder,
			Routing:        core,
			Upstreams:      upstreamSender(links),
			Transcoders:    transcoders,
			ScheduleState:  bridgeState(sched, triggers),
			Triggers:       triggers,
			Rebuild:        func(now time.Time) (*routing.Table, error) { return buildTable(cfg, sched, triggers, now) },
		})
		if lerr != nil {
			return nil, lerr
		}
		a.dmr = listener
		a.closers = append(a.closers, func(context.Context) error { return listener.Close() })

		// The link hands received frames back through the listener, because the
		// listener owns the socket peers are reachable on.
		if links != nil {
			for _, name := range links.Names() {
				log.Info("upstream configured", slog.String("link", name))
			}
			a.closers = append(a.closers, func(context.Context) error { return links.Close() })
		}
	} else {
		log.Info("DMR listener disabled", slog.String("reason", dmrDisabledReason))
	}

	registry := health.NewRegistry(health.Options{})
	registry.MustRegister(database.HealthCheck{DB: a.db, UnavailableReason: dbUnavailableReason})
	registry.MustRegister(processCheck{started: time.Now()})
	// The IPSC listener's own parrot recorder, built from the same settings as
	// the DMR listener's and deliberately not the same object.
	//
	// **Both key recordings by radio ID, and the two protocols share the DMR ID
	// space.** This network had 3132910 registered on both listeners at once on
	// 2026-09-02: a Pi-Star and an XPR8300. A shared recorder would have merged
	// their recordings and replayed one operator's audio into the other's
	// radio, with nothing logged to say so. Two recorders degrade to two
	// independent parrots.
	var ipscParrot *parrot.Recorder
	if cfg.IPSC.Enabled && cfg.DMR.Parrot.Enabled {
		slot := hbp.Timeslot1
		if cfg.DMR.Parrot.Timeslot == 2 {
			slot = hbp.Timeslot2
		}
		rec, perr := parrot.New(parrot.Config{
			Talkgroup:   cfg.DMR.Parrot.Talkgroup,
			Timeslot:    slot,
			MaxDuration: cfg.DMR.Parrot.MaxDuration.AsDuration(),
			Gap:         cfg.DMR.Parrot.Gap.AsDuration(),
		})
		if perr != nil {
			return nil, perr
		}
		ipscParrot = rec
		// The operator has to do something QSP cannot check for them.
		log.Info("parrot enabled for Motorola repeaters; "+
			"the talkgroup must be in each repeater's codeplug or the replay "+
			"reaches the repeater and is not transmitted",
			slog.Uint64("talkgroup", uint64(cfg.DMR.Parrot.Talkgroup)),
			slog.String("timeslot", slot.String()),
		)
	}
	// IPSC is its own listener on its own port. A club may run a Motorola
	// repeater, an HBP network, both or neither, so neither enables the other.
	// **Both name the page, not the configuration field.** These strings were
	// written when editing `qsp.json` was the only way to turn either listener
	// on, and stayed put after the console grew a control for each — which is
	// a health report telling an operator to edit a file for something they can
	// click, and the same shape as the four documents that went on recommending
	// `qsp adduser` after the setup wizard replaced it.
	//
	// What a health line is for is the state a subsystem is in and what would
	// change it. **What a subsystem is not** — that enabling P25 here will not
	// link a Motorola Quantar — is documentation, and it lives in the panel's
	// hint where an operator is already reading about P25.
	ipscDisabledReason := "IPSC is off. Turn it on in Network settings to serve Motorola repeaters"
	p25DisabledReason := "P25 is off. Turn it on in Network settings to serve P25 gateways"
	if cfg.P25.Enabled {
		// **A P25 reflector, and it does not touch DMR.** ADR-0034: P25
		// carries IMBE and DMR carries AMBE+2, so routing one through the
		// other is tandem vocoding and QSP will not do it. A P25 call crosses
		// between P25 endpoints and nowhere else, which is why this listener
		// needs no sink into the DMR side and has none.
		pl, perr := p25link.New(log, p25link.Config{
			ListenAddress:    cfg.P25.ListenAddress,
			Callsign:         cfg.P25.Callsign,
			AllowedCallsigns: cfg.P25.AllowedCallsigns,
		})
		if perr != nil {
			return nil, perr
		}
		a.p25 = pl
	}

	if cfg.IPSC.Enabled {
		// A Motorola repeater's audio reaches the rest of the network through
		// the DMR listener, because that listener owns the socket Homebrew
		// peers are reachable on. With no DMR listener there is nowhere to
		// deliver to, and the IPSC listener records transmissions without
		// carrying them rather than pretending otherwise.
		// Validation refuses an enabled listener with no colour code, so this
		// is never nil in practice. It is still checked, because a nil
		// dereference here would take the daemon down over a configuration
		// mistake, and refusing to start with a reason is the behaviour §7
		// asks for.
		if cfg.IPSC.ColourCode == nil {
			return nil, fmt.Errorf("ipsc.colour_code is not set; the IPSC listener cannot build " +
				"bursts without the colour code the repeater is programmed with")
		}
		colourCode := *cfg.IPSC.ColourCode

		var deliver, observe func(hbp.RepeaterID, hbp.Data)
		if a.dmr != nil {
			deliver = a.dmr.DeliverFromIPSC
			// Recording a transmission and carrying it are separate acts, and
			// parrot takes what it handles before Deliver is reached. Without
			// this, a Motorola operator keying the parrot talkgroup would
			// leave no trace on the console while a hotspot operator doing the
			// same left one.
			observe = a.dmr.ObserveFromIPSC
			log.Info("IPSC audio is bridged to DMR peers",
				slog.Int("colour_code", int(colourCode)),
				slog.Bool("slot_bit_is_timeslot2", cfg.IPSC.SlotBitIsTimeslot2))
		} else {
			log.Warn("IPSC is enabled with no DMR listener, so transmissions are recorded but not carried",
				slog.String("remedy", "enable dmr to bridge Motorola audio to hotspots"))
		}
		il, ierr := ipsclink.New(log, ipsclink.Config{
			ListenAddress: cfg.IPSC.ListenAddress,
			MasterID:      cfg.IPSC.MasterID,
			AllowedPeers:  cfg.IPSC.AllowedPeers,
			PeerNames:     cfg.IPSC.PeerNames,
			PeerTimeout:   time.Duration(cfg.IPSC.PeerTimeoutSeconds) * time.Second,
			Observe:       observe,
			Deliver:       deliver,
			Parrot:        ipscParrot,
			Bridge: ipscbridge.Config{
				ColourCode:         colourCode,
				SlotBitIsTimeslot2: cfg.IPSC.SlotBitIsTimeslot2,
			},
		})
		if ierr != nil {
			return nil, ierr
		}
		a.ipsc = il
	}

	// **The two listeners are each other's destination, so this is wired after
	// both exist.** It says which of the three states it is in every time,
	// because the first version of this call was written and never reached:
	// an exported method nobody invokes compiles, passes vet, passes
	// staticcheck and passes every test, and the only symptom is silence on
	// air. §8a calls this "declared and read by nothing" and it has now
	// happened nine times.
	switch {
	case a.dmr != nil && a.ipsc != nil:
		a.dmr.SetIPSCSink(a.ipsc.SendVoice)
		// **This caveat used to say no capture of a master sending voice
		// existed, and that stopped being true on 2026-09-03.**
		// testdata/ipsc/ipsc-master-voice.pcap is 347 packets of an XPR8300's
		// own RF, 288 of them voice, and four tests read it — including
		// internal/ipscbridge/master_test.go.
		//
		// A stale caveat is worse than none. On 2026-09-08 a repeater keyed up
		// and transmitted silence, and this line was read twice as evidence
		// that the direction was unverifiable, when the reference to verify it
		// against had been in the repository for five days. The remaining fault
		// is measurable, and saying otherwise sent an hour in the wrong
		// direction.
		log.Info("relaying network audio to IPSC repeaters",
			slog.String("caveat", "a repeater may key up and transmit silence; "+
				"compare what QSP sends against testdata/ipsc/ipsc-master-voice.pcap (ADR-0041)"))
	case a.ipsc != nil:
		log.Warn("IPSC repeaters will not hear the network: no DMR listener to relay from",
			slog.String("remedy", "enable dmr"))
	}

	registry.MustRegister(ipsclink.HealthCheck{Listener: a.ipsc, DisabledReason: ipscDisabledReason})
	registry.MustRegister(p25link.HealthCheck{Listener: a.p25, DisabledReason: p25DisabledReason})

	registry.MustRegister(peers.HealthCheck{Listener: a.dmr, DisabledReason: dmrDisabledReason})
	registry.MustRegister(peers.PeersHealthCheck{Listener: a.dmr, Master: master, DisabledReason: dmrDisabledReason})
	registry.MustRegister(routingCheck{enabled: cfg.DMR.Forwarding, bridges: len(cfg.DMR.Bridges)})
	registry.MustRegister(schedulerCheck{windows: len(cfg.DMR.Schedule), forwarding: cfg.DMR.Forwarding})
	if a.upstreams != nil {
		// Before the checks are built, so each link knows whether anything can
		// reach it. A link opens regardless of dmr.forwarding and then reports
		// a silence it cannot explain.
		a.upstreams.SetRelaying(cfg.DMR.Forwarding)
		for _, name := range a.upstreams.Names() {
			registry.MustRegister(a.upstreams.CheckFor(name))
		}
	}
	// Vocoder channels, built with the DMR listener above.
	if a.vocoders != nil {
		for _, name := range a.vocoders.Names() {
			registry.MustRegister(a.vocoders.CheckFor(name))
		}
	}
	for _, ch := range a.transcodingChannels {
		registry.MustRegister(ch.Checker())
	}
	if a.zelloLogon != nil {
		registry.MustRegister(a.zelloLogon.Checker(a.secrets))
	}

	for _, s := range unbuiltSubsystems {
		registry.MustRegister(unbuilt(s.name, s.arrives))
	}
	a.health = registry

	assets, err := console.Assets()
	if err != nil {
		return nil, fmt.Errorf("cannot open embedded console assets: %w", err)
	}

	var peerSource server.PeerSource
	if a.dmr != nil {
		peerSource = peerViews{listener: a.dmr, names: a.names}
	}

	// Nil when there is no database, which is a working state: an instance
	// that only observes needs no accounts, and ADR-0026 keeps the web surface
	// free of any unauthenticated path that writes. The handlers say so rather
	// than returning a 404 that would read like a missing feature.
	var authService server.Authenticator
	// **The concrete service, for the two options that need more than
	// authenticating.** Setting up the first administrator and managing the
	// rest are not part of the login flow, so they are separate interfaces —
	// and nil for an instance with no database, which disables both pages
	// explicitly rather than by accident.
	var accountService *auth.Service
	if a.db != nil {
		repo, rerr := auth.NewSQLRepository(a.db.SQL())
		if rerr != nil {
			return nil, rerr
		}
		// **The policy comes from the configuration now.** It was
		// `auth.Policy{}` here and in the two account commands, so
		// DefaultSessionLifetime was the only value QSP could have and no
		// operator could change it. Zero still selects the default, so a
		// configuration that says nothing behaves exactly as before.
		svc, serr := auth.NewService(repo, auth.Policy{
			SessionLifetime: time.Duration(a.cfg.Server.SessionLifetime),
		}, nil)
		if serr != nil {
			return nil, serr
		}
		authService = svc
		accountService = svc
		a.auth = svc
	}

	// The configuration manager. A nil writer is a working state: an instance
	// started without -config runs on defaults and cannot be reconfigured from
	// a browser, which the console reports rather than discovering at save.

	manager := &configManager{current: cfg, startup: cfg}
	a.configManager = manager
	if configPath != "" {
		writer, werr := config.NewWriter(configPath)
		if werr != nil {
			return nil, werr
		}
		manager.writer = writer
	}
	if a.db != nil {
		store, serr := config.NewSQLVersionStore(a.db.SQL())
		if serr != nil {
			return nil, serr
		}
		manager.store = store
	}

	// **After the writer and the version store, because it writes.** A server
	// that predates ADR-0053 has no identifier and still needs one; the
	// bootstrap only writes a configuration where none exists at all, which is
	// never true of a server already running.
	if err := ensureIdentifier(ctx, manager, log); err != nil {
		return nil, err
	}
	cfg = manager.Current()

	srv, err := server.New(log, registry, a.bus, server.Options{
		ListenAddress:       cfg.Server.ListenAddress,
		ReadHeaderTimeout:   cfg.Server.ReadHeaderTimeout.AsDuration(),
		ReadTimeout:         cfg.Server.ReadTimeout.AsDuration(),
		WriteTimeout:        cfg.Server.WriteTimeout.AsDuration(),
		IdleTimeout:         cfg.Server.IdleTimeout.AsDuration(),
		ShutdownTimeout:     cfg.Server.ShutdownTimeout.AsDuration(),
		BehindProxy:         cfg.Server.BehindProxy,
		ConsoleAssets:       assets,
		Calls:               callHistory(callStore),
		Callsign:            func(id uint32) string { return resolve(id, nil, a.names) },
		Links:               linkSource(a.upstreams, cfg),
		Peers:               peerSource,
		PeersDisabledReason: dmrDisabledReason,
		IPSCPeers:           ipscPeerSource(a.ipsc, a.names),
		P25Gateways:         p25Source(a.p25),
		Forwarding:          cfg.DMR.Enabled && cfg.DMR.Forwarding,
		Auth:                authService,
		// The first administrator comes from the setup page (ADR-0056) and
		// every one after it from the administration page. Both are the same
		// service; they are separate options so that an instance without an
		// account store disables each explicitly rather than by accident.
		Setup:    setupOrNil(accountService),
		Accounts: accountsOrNil(accountService),
		Config:   manager,
		Audit:    a.audit,
		Secrets:  a.secrets,
		// **The ordinary exit, not a bespoke one.** SIGTERM to this process
		// takes exactly the path systemctl restart already takes, so the audit
		// record, the shutdown timeout and every subsystem's close run as they
		// always have. A restart that unwound differently from a stop would be
		// a second shutdown path to keep true.
		Restart: func() {
			if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
				log.Error("cannot stop this process to restart it",
					slog.String("error", err.Error()))
			}
		},
		Map:  mapSettings(cfg),
		Join: joinSettings(cfg),
	})
	if err != nil {
		return nil, err
	}
	a.srv = srv
	if a.master != nil {
		srv.SetLogins(a.master)
	}
	// The join page, the map and the forwarding flag are derived from the
	// configuration and were captured once at construction, so a saved change
	// to any of them applied to nothing while NeedsRestart reported that no
	// restart was needed.
	manager.applyServer = func(c config.Config) {
		srv.ApplyConfig(joinSettings(c), mapSettings(c), c.DMR.Enabled && c.DMR.Forwarding)
	}
	// **Before the listener starts**, so a server with no administrator has
	// already printed its setup token by the time the console can be reached
	// (ADR-0056).
	if err := srv.PrepareSetup(ctx); err != nil {
		return nil, err
	}
	a.closers = append(a.closers, srv.Shutdown)

	return a, nil
}

// buildDMR constructs the peer master when the DMR listener is enabled.
//
// It returns a nil Master and a reason when the listener is disabled, which is
// the default: a freshly installed QSP must not start accepting connections
// before an operator has decided it should. A configuration that enables the
// listener but cannot supply a password is a fatal error rather than a silent
// downgrade, because running a master that authenticates nobody would be worse
// than not running one.
func buildDMR(cfg config.Config, log *slog.Logger, bus *events.Bus) (*peers.Master, string, error) {
	if !cfg.DMR.Enabled {
		return nil, "the DMR listener is disabled; set dmr.enabled to accept peers", nil
	}

	if err := checkPasswordFileMode(cfg.DMR.PasswordFile); err != nil {
		return nil, "", err
	}

	password, err := config.LoadPeerPassword(os.ReadFile, cfg.DMR.PasswordFile)
	if err != nil {
		return nil, "", err
	}

	// A member is removed by deleting one file, rather than by changing the
	// password every other member is using. See ADR-0035.
	peerPasswords := config.NewPeerPasswords(
		cfg.DMR.PeerPasswords, password, os.ReadFile, os.Stat)
	if peerPasswords.PerPeer() {
		log.Info("per-peer passwords enabled",
			slog.String("directory", cfg.DMR.PeerPasswords))
	}

	// Validate has already accepted these, so a parse failure here would mean
	// the two disagree. Reporting it is better than starting a master whose
	// access lists silently defaulted to permitting everything.
	lists, err := cfg.AccessLists()
	if err != nil {
		return nil, "", fmt.Errorf("cannot apply the access lists: %w", err)
	}
	for _, advisory := range cfg.AccessAdvisories() {
		log.Warn("access list advisory", slog.String("detail", advisory))
	}

	master, err := peers.NewMaster(log, peers.MasterConfig{
		// One shared password for every peer, which is how these networks are
		// operated in practice. Per-peer secrets would come from storage.
		// **Per-peer where a file exists, shared otherwise.** The signature
		// allowed this from the start and nothing supplied it, so removing one
		// member meant changing everybody's password. See ADR-0035.
		Password: func(id hbp.RepeaterID) ([]byte, bool) {
			secret, err := peerPasswords.For(uint32(id))
			if err != nil {
				// Refused rather than falling back: falling back on a
				// permissions mistake turns it into a silently weakened
				// network, and a peer whose file cannot be read is one an
				// administrator meant to control.
				log.Warn("refusing a peer whose password cannot be resolved",
					slog.Uint64("peer_id", uint64(id)), slog.String("error", err.Error()))
				return nil, false
			}
			return secret, true
		},
		PeerTimeout:  cfg.DMR.PeerTimeout.AsDuration(),
		LoginTimeout: cfg.DMR.LoginTimeout.AsDuration(),
		MaxPeers:     cfg.DMR.MaxPeers,
		Access:       lists,
		// Where radios are, learned from traffic; see ADR-0021.
		SubscriberTimeout: cfg.DMR.SubscriberTimeout.AsDuration(),
		// Which talkgroups each peer receives; see ADR-0023.
		Subscription: subscriptionFrom(cfg),
		// **What this server says about itself, to a server that registers as
		// a link.** ADR-0052 rule 3 requires a server to say what it is, and
		// registration only carried it one way: the side that dialled
		// announced a callsign, a network and a version, and got four bytes
		// back. Sent only to a peer whose package ID marks it a QSP link, so a
		// hotspot never receives one.
		Identity:  func() hbp.Identity { return serverIdentity(cfg) },
		IsQSPLink: func(c hbp.Config) bool { _, ok := LinkNameFromPackageID(c.PackageID); return ok },
	})
	if err != nil {
		return nil, "", err
	}
	return master, "", nil
}

// subscriptionFrom converts the configured subscription into the master's form.
//
// The timeslot is validated as 1 or 2 before it reaches here, so an unexpected
// value would be a bug rather than bad input; it is mapped defensively anyway,
// because a static attachment silently landing on the wrong slot is the kind of
// fault an operator would spend an evening on.
func subscriptionFrom(cfg config.Config) peers.SubscriptionConfig {
	out := peers.SubscriptionConfig{
		Enabled: cfg.DMR.Subscription.Enabled,
		Timeout: cfg.DMR.Subscription.Timeout.AsDuration(),
	}
	for _, a := range cfg.DMR.Subscription.Static {
		slot := hbp.Timeslot1
		if a.Timeslot == 2 {
			slot = hbp.Timeslot2
		}
		out.Static = append(out.Static, peers.Attachment{
			Peer:      hbp.RepeaterID(a.Peer),
			Talkgroup: a.Talkgroup,
			Timeslot:  slot,
		})
	}
	return out
}

// run starts the application and blocks until ctx is cancelled.
func (a *app) run(ctx context.Context) error {
	if err := a.srv.Start(); err != nil {
		return err
	}
	if a.dmr != nil {
		if err := a.dmr.Start(ctx); err != nil {
			return err
		}
	}
	if a.ipsc != nil {
		if err := a.ipsc.Start(ctx); err != nil {
			return err
		}
	}
	if a.p25 != nil {
		if err := a.p25.Start(ctx); err != nil {
			return err
		}
	}
	// Links start after the listener, because a frame arriving on one is
	// delivered through the listener's socket. Starting them first would open a
	// window in which a received frame had nowhere to go.
	if a.upstreams != nil {
		if err := a.upstreams.Start(ctx); err != nil {
			return err
		}
	}

	if err := a.audit.Record(ctx, audit.Event{
		OccurredAt: time.Now().UTC(),
		Actor:      audit.SystemActor,
		Action:     audit.ActionServiceStarted,
		Outcome:    audit.OutcomeSuccess,
		Detail:     map[string]string{"listen_address": a.srv.Address()},
	}); err != nil {
		a.log.Warn("cannot record startup in the audit trail", slog.String("error", err.Error()))
	}

	// Expired sessions are swept periodically. Service.Session already refuses
	// and deletes one it is shown, so this is about the rows nobody presents
	// again: without it the table grows by one row per login, for ever, on an
	// instance that may run for years.
	if a.auth != nil {
		go a.sweepSessions(ctx)
	}

	// The call history is trimmed on the same principle: retention is measured
	// in days, so nothing observes the boundary, and pruning on every write
	// would make each transmission pay for the policy.
	go a.pruneCalls(ctx)

	// The listener exists by now, so a save can reach the goroutine that owns
	// the routing core. Wired here rather than in build because the listener
	// is constructed after the server that will call it.
	if a.dmr != nil && a.configManager != nil {
		// The author travels with the change, so the line the listener logs
		// names the administrator rather than "console" — the version row
		// could attribute a live change and the log could not.
		a.configManager.apply = applyToListener(a.dmr, a.ipsc)
	}

	// Resolving names is background work by design: nothing waits on it, and a
	// registry that is slow costs a name rather than a transmission.
	if a.names != nil {
		go a.names.Run(ctx)
	}

	// Vocoder channels are opened in the background and retried, because the
	// thing at the other end is absent more often than anything else QSP
	// talks to: it is passed through a hypervisor, AMBEserver is started by
	// hand, and a wedged chip is cleared by pulling its power. A server that
	// refused to start without one would be a server that refused to start.
	if a.vocoders != nil {
		go a.vocoders.Run(ctx)
	}
	// The channels run beside it and ask for a chip at the start of each
	// call, so a vocoder that opens late is used from the next call on.
	if a.transcoding != nil {
		go a.transcoding.Run(ctx)
	}
	if a.zelloLogon != nil {
		go a.zelloLogon.Serve(ctx)
	}

	<-ctx.Done()
	return nil
}

// sessionSweepInterval is how often expired sessions are removed.
//
// Nothing depends on the sweep being prompt — an expired session is already
// refused on sight, so this only reclaims rows. Hourly costs one statement an
// hour and keeps the table proportional to the sessions that exist rather than
// to every login ever made.
const sessionSweepInterval = time.Hour

func (a *app) sweepSessions(ctx context.Context) {
	ticker := time.NewTicker(sessionSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := a.auth.SweepSessions(ctx)
			if err != nil {
				a.log.Warn("cannot sweep expired sessions", slog.String("error", err.Error()))
				continue
			}
			if n > 0 {
				a.log.Debug("swept expired sessions", slog.Int("removed", n))
			}
		}
	}
}

// callPruneInterval is how often the call history is trimmed.
//
// Six-hourly, because retention is measured in days and nothing observes the
// boundary. A row outliving its window by an afternoon costs a few kilobytes;
// pruning on every write would make each transmission pay for the policy.
const callPruneInterval = 6 * time.Hour

func (a *app) pruneCalls(ctx context.Context) {
	if a.callStore == nil || !a.callStore.Enabled() {
		return
	}
	ticker := time.NewTicker(callPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := a.callStore.Prune(ctx, time.Now().UTC())
			if err != nil {
				a.log.Warn("cannot prune the call history", slog.String("error", err.Error()))
				continue
			}
			if n > 0 {
				a.log.Debug("pruned the call history", slog.Int64("removed", n))
			}
		}
	}
}

// shutdown closes every subsystem in reverse construction order.
//
// It continues past a failure so that one stuck subsystem cannot prevent the
// others from releasing their resources, and reports every error it saw.
func (a *app) shutdown(ctx context.Context) error {
	if err := a.audit.Record(ctx, audit.Event{
		OccurredAt: time.Now().UTC(),
		Actor:      audit.SystemActor,
		Action:     audit.ActionServiceStopped,
		Outcome:    audit.OutcomeSuccess,
	}); err != nil {
		a.log.Warn("cannot record shutdown in the audit trail", slog.String("error", err.Error()))
	}

	var errs []error
	for i := len(a.closers) - 1; i >= 0; i-- {
		if err := a.closers[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// bridgeState merges the two mechanisms that can open a bridge.
//
// A bridge may be scheduled, triggered, both, or neither. Either mechanism
// opening it is enough: a net that starts early because somebody keyed up is
// the behaviour an operator wants, and so is a triggered link staying open
// through a scheduled window.
//
// Returns nil when neither mechanism is configured, so bridges keep their own
// enabled setting and nothing overrides it.
func bridgeState(s *scheduler.Schedule, t *routing.Triggers) func(time.Time) map[string]bool {
	if s == nil && t == nil {
		return nil
	}
	return func(now time.Time) map[string]bool {
		out := make(map[string]bool)
		for name, open := range s.ActiveAt(now) {
			if open {
				out[name] = true
			}
		}
		for name, open := range t.ActiveAt(now) {
			if open {
				out[name] = true
			}
		}
		return out
	}
}

// buildTriggers converts configured triggers into a trigger set.
func buildTriggers(cfg config.Config) (*routing.Triggers, error) {
	if len(cfg.DMR.Triggers) == 0 {
		return nil, nil
	}
	out := make([]routing.Trigger, 0, len(cfg.DMR.Triggers))
	for _, t := range cfg.DMR.Triggers {
		on := make([]routing.Endpoint, 0, len(t.On))
		for _, e := range t.On {
			on = append(on, routing.Endpoint{
				Peer: hbp.RepeaterID(e.Peer), Talkgroup: e.Talkgroup, Timeslot: timeslot(e.Timeslot),
			})
		}
		out = append(out, routing.Trigger{
			Bridge: t.Bridge, On: on, HangTime: t.HangTime.AsDuration(), Enabled: t.Enabled,
		})
	}
	return routing.NewTriggers(out)
}

func timeslot(n int) hbp.Timeslot {
	if n == 2 {
		return hbp.Timeslot2
	}
	return hbp.Timeslot1
}

// buildSchedule converts configured windows into a schedule.
func buildSchedule(cfg config.Config) (*scheduler.Schedule, error) {
	if len(cfg.DMR.Schedule) == 0 {
		return nil, nil
	}
	windows := make([]scheduler.Window, 0, len(cfg.DMR.Schedule))
	for _, w := range cfg.DMR.Schedule {
		start, err := scheduler.ParseLocalTime(w.Start)
		if err != nil {
			return nil, fmt.Errorf("schedule window for bridge %q: %w", w.Bridge, err)
		}
		days := make([]time.Weekday, 0, len(w.Days))
		for _, d := range w.Days {
			days = append(days, time.Weekday(d))
		}
		windows = append(windows, scheduler.Window{
			Bridge:   w.Bridge,
			Days:     days,
			Start:    start,
			Duration: w.Duration.AsDuration(),
			Timezone: w.Timezone,
			Enabled:  w.Enabled,
		})
	}
	return scheduler.NewSchedule(windows)
}

// buildTable converts configured bridges into a routing table for an instant.
//
// A bridge named by any schedule window is controlled entirely by the schedule;
// its own Enabled field is ignored. A bridge with no window uses that field.
// One mechanism decides each bridge, so an operator never has to work out which
// setting won.
func buildTable(cfg config.Config, sched *scheduler.Schedule, triggers *routing.Triggers, now time.Time) (*routing.Table, error) {
	controlled := make(map[string]bool)
	for _, name := range sched.Bridges() {
		controlled[name] = true
	}
	for _, name := range triggers.Bridges() {
		controlled[name] = true
	}

	active := make(map[string]bool)
	for name, open := range sched.ActiveAt(now) {
		active[name] = active[name] || open
	}
	for name, open := range triggers.ActiveAt(now) {
		active[name] = active[name] || open
	}

	bridges := make([]routing.Bridge, 0, len(cfg.DMR.Bridges))
	for _, b := range cfg.DMR.Bridges {
		endpoints := make([]routing.Endpoint, 0, len(b.Endpoints))
		for _, e := range b.Endpoints {
			endpoints = append(endpoints, routing.Endpoint{
				Peer:       hbp.RepeaterID(e.Peer),
				Upstream:   e.Upstream,
				Transcoder: e.Transcoder,
				Talkgroup:  e.Talkgroup,
				Timeslot:   timeslot(e.Timeslot),
			})
		}
		enabled := b.Enabled
		if controlled[b.Name] {
			enabled = active[b.Name]
		}
		bridges = append(bridges, routing.Bridge{Name: b.Name, Enabled: enabled, Endpoints: endpoints})
	}

	// **Who may receive transcoded audio, per vocoder.** Empty permits
	// nobody: ADR-0062 makes this opt-in per repeater because a transcoded
	// transmission may come from an unlicensed user and reaches RF. A
	// disabled transcoder contributes no permission, so turning one off
	// stops its audio rather than leaving a permission behind.
	permissions := make(map[string]routing.Permission, len(cfg.DMR.Transcoders))
	for _, t := range cfg.DMR.Transcoders {
		if !t.Enabled {
			continue
		}
		peers := make([]hbp.RepeaterID, 0, len(t.PermitPeers))
		for _, id := range t.PermitPeers {
			peers = append(peers, hbp.RepeaterID(id))
		}
		permissions[t.Name] = routing.Permission{Peers: peers, All: t.PermitAllPeers}
	}

	return routing.NewTable(bridges, routing.WithPermissions(permissions))
}

// readyPeers adapts the peer master to the routing core's narrow view of it.
//
// Both are owned by the listener goroutine, so these calls are made from that
// goroutine only; nothing here is safe to call from elsewhere.
type readyPeers struct{ master *peers.Master }

func (r readyPeers) Ready(id hbp.RepeaterID) bool {
	p, ok := r.master.Lookup(id)
	return ok && p.State.CanPassTraffic()
}

func (r readyPeers) ReadyPeers() []hbp.RepeaterID {
	var out []hbp.RepeaterID
	for _, p := range r.master.Peers() {
		if p.State.CanPassTraffic() {
			out = append(out, p.ID)
		}
	}
	return out
}

func (p peerViews) Traffic() server.Traffic {
	s := p.listener.Stats()
	return server.Traffic{
		DatagramsIn:     s.Received,
		DatagramsOut:    s.Sent,
		Dropped:         s.Dropped,
		Answered:        s.Refused,
		Ignored:         s.Ignored,
		FramesAccepted:  s.Frames,
		FramesForwarded: s.Forwarded,
		Collisions:      s.Collisions,
		RecentDrops:     p.listener.RecentDrops(),
	}
}

// displayAddr renders a peer address for the console.
//
// A socket bound to the IPv6 wildcard reports IPv4 clients as IPv4-mapped
// addresses, so a hotspot at 192.168.1.155 appears as
// "[::ffff:192.168.1.155]:46458". That is correct and unreadable, and it means
// the same peer could be shown two different ways depending on how the socket
// was bound. Unmapping gives one stable form.
func displayAddr(a netip.AddrPort) string {
	addr := a.Addr()
	if addr.Is4In6() {
		return netip.AddrPortFrom(addr.Unmap(), a.Port()).String()
	}
	return a.String()
}

// ipscPeerSource returns a console view of the IPSC listener, or nil when that
// listener is not running.
//
// **Nil rather than an empty adapter**, because the console distinguishes "no
// Motorola repeaters are connected" from "this instance does not accept them",
// and a source that always answers with an empty list collapses the two.
func ipscPeerSource(l *ipsclink.Listener, names *callsigns.Service) server.PeerSource {
	if l == nil {
		return nil
	}
	return ipscPeerViews{listener: l, names: names}
}

// ipscPeerViews adapts the IPSC listener to the console's view.
//
// # What a Motorola repeater does not announce
//
// Callsign, location, height and talkgroups are all absent, and that is the
// protocol rather than a gap in this adapter. An IPSC peer announces no
// subscriptions — it receives everything and filters by its own codeplug — so
// there is nothing QSP could put in an attachments list that would not be a
// guess about somebody else's programming. Protocol is set on every view so the
// console can say why the columns are empty.
type ipscPeerViews struct {
	listener *ipsclink.Listener
	names    *callsigns.Service
}

func (p ipscPeerViews) PeerViews(now time.Time) []server.PeerView {
	snap := p.listener.Peers()
	out := make([]server.PeerView, 0, len(snap))
	for _, peer := range snap {
		v := server.PeerView{
			ID:       peer.RadioID,
			Protocol: server.ProtocolIPSC,
			Address:  peer.Address,
			IdleFor:  now.Sub(peer.LastHeard).Truncate(time.Second).String(),
			// A registered IPSC peer is passing traffic: there is no login
			// sequence to be partway through. It registers or it retries.
			State: "registered",
			Ready: true,
		}
		// **How long it has been in contact, not how long since a handshake
		// this instance happened to witness.** A repeater that registered with
		// a previous process and kept sending keepalives is answered and
		// passing traffic, and reporting only what QSP saw a registration for
		// left a dash beside a repeater that had been carrying audio for
		// hours. The Homebrew column means the same thing, so the two now
		// answer the same question in the same units.
		if !peer.FirstHeard.IsZero() {
			v.ConnectedFor = now.Sub(peer.FirstHeard).Truncate(time.Second).String()
		}
		// Learned from the repeater's own frames rather than announced, so a
		// repeater that has never transmitted shows none. That is worth
		// seeing: it is exactly the case where ADR-0042's mirroring is falling
		// back to ipsc.colour_code.
		if peer.ColourCodeKnown {
			// Two digits, because the Homebrew side reports two and one
			// column showing "1" beside "01" reads as two different things
			// being measured.
			v.ColorCode = fmt.Sprintf("%02d", peer.ColourCode)
		}
		// Looked up rather than announced, and marked as such. IP Site Connect
		// carries no callsign, so the alternative to a registry lookup is a
		// column of radio IDs an operator has to translate in their head.
		//
		// **The registry is the subscriber database.** Many repeater IDs are
		// not in it and will simply come back unknown, which is why the console
		// still has to say "not sent" rather than assume a blank means a
		// failure.
		// **The operator's own label wins.** A repeater on a private radio ID
		// is not in the registry and never will be, and where both exist the
		// person running the repeater knows better than a public database
		// that may describe whoever registered the ID years ago.
		if name := p.listener.PeerName(peer.RadioID); name != "" {
			v.Callsign = name
			v.CallsignSource = server.CallsignFromOperator
		} else if name := resolve(peer.RadioID, nil, p.names); name != "" {
			v.Callsign = name
			v.CallsignSource = server.CallsignFromRegistry
		}
		out = append(out, v)
	}
	return out
}

// CallViews contributes nothing, deliberately.
//
// **Every IPSC transmission was appearing in Last heard twice.**
// DeliverFromIPSC observes each converted burst into the shared call tracker
// before routing it, so the tracker already holds every transmission that
// crosses the bridge — with a resolved callsign, merged data bursts, an end
// reason and history. This adapter then appended the IPSC listener's own view
// of the same transmission on top, and nothing deduplicated the two.
//
// A member saw one over as two, a fraction of a second apart, with different
// frame counts: 8 against 10, because the IPSC side counts the header and
// terminator that the converter folds into one of each, and different
// durations, because one truncates to whole seconds and the other does not.
// Two records of one event that disagree about how long it was.
//
// The tracker owns Last heard. **Peer.LastCall stays** — it is what the
// three-layer frame counters hang off and what the peer row reads — but it is
// peer state, not a second call history.
func (p ipscPeerViews) CallViews(time.Time) (active, recent []server.CallView) {
	return nil, nil
}

// Traffic reports the Motorola listener's own figures in their own object.
//
// The DMR listener's counters are left alone: summing two sockets into a
// documented figure for one would change what an existing number means without
// saying so. What this fixes is the opposite problem — a network whose only
// traffic was Motorola repeaters reported zero voice frames and advised the
// operator to check a hotspot that had nothing to do with it.
func (p ipscPeerViews) Traffic() server.Traffic {
	var frames uint64
	for _, peer := range p.listener.Peers() {
		frames += peer.VoiceFrames
	}
	ignored, unparsed := p.listener.Counters()
	return server.Traffic{IPSC: &server.IPSCTraffic{
		VoiceFrames: frames,
		Ignored:     ignored,
		Unparsed:    unparsed,
	}}
}

// p25GatewaySource adapts the P25 listener to the console's traffic view.
//
// **The projection lives here for the same reason ipscPeerSource does**, so
// internal/server does not depend on internal/p25link and the fields the
// console can see are chosen in one obvious place.
//
// It answers Traffic() and nothing else. A P25 gateway is not a DMR peer — it
// has no repeater ID, no timeslot and no login — so putting one in the peer
// table would make the console lie about what it is. PeerViews and CallViews
// return nothing deliberately, and that is a statement rather than a stub:
// once P25 reaches the call tracker (see docs/P25-PLANNING.md) CallViews is
// where it arrives.
type p25GatewaySource struct{ listener *p25link.Listener }

// p25Source returns nil when P25 is disabled, so the payload omits the object
// entirely rather than carrying zeroes. **A nil interface, not a nil
// pointer**: a typed nil inside an interface is non-nil at the call site, which
// would make the console show an empty P25 panel on a server that is not
// running P25 at all.
func p25Source(l *p25link.Listener) server.PeerSource {
	if l == nil {
		return nil
	}
	return p25GatewaySource{listener: l}
}

func (p p25GatewaySource) PeerViews(time.Time) []server.PeerView { return nil }

func (p p25GatewaySource) CallViews(time.Time) (active, recent []server.CallView) {
	return nil, nil
}

func (p p25GatewaySource) Traffic() server.Traffic {
	gateways := p.listener.Gateways()
	refused, refusedWho := p.listener.Refused()

	var polls, frames uint64
	rows := make([]server.P25GatewayView, 0, len(gateways))
	for _, g := range gateways {
		polls += g.Polls
		frames += g.Frames
		addr := ""
		if g.Address != nil {
			addr = g.Address.String()
		}
		rows = append(rows, server.P25GatewayView{
			Callsign:  g.Callsign,
			Address:   addr,
			Talkgroup: g.Talkgroup,
			SourceID:  g.SourceID,
			Polls:     g.Polls,
			Frames:    g.Frames,
			Sent:      g.Sent,
			// Rounded to whole seconds: a poll arrives every five and
			// sub-second precision would imply a measurement nobody wants.
			LastPollAgoSeconds: int(time.Since(g.LastPoll).Round(time.Second).Seconds()),
		})
	}
	// Sorted by callsign so the list does not reorder itself between polls:
	// the map it comes from has no order, and a table that reshuffles every
	// five seconds is unreadable.
	sort.Slice(rows, func(i, j int) bool { return rows[i].Callsign < rows[j].Callsign })

	return server.Traffic{P25: &server.P25Traffic{
		VoiceFrames: frames,
		Polls:       polls,
		Refused:     refused,
		RefusedLast: refusedWho,
		Unparsed:    p.listener.Unparsed(),
		Gateways:    rows,
	}}
}

// peerViews adapts the peer listener to the console's narrow view of it.
//
// The projection lives here rather than in either package so that
// internal/server does not depend on internal/peers, and so that the fields the
// console can see are chosen in one obvious place. Notably absent: a peer's
// outstanding challenge salt.
type peerViews struct {
	listener *peers.Listener
	// names resolves radio IDs the peer list cannot. Nil when lookups are off.
	names *callsigns.Service
}

func (p peerViews) PeerViews(now time.Time) []server.PeerView {
	snap := p.listener.Snapshot()
	out := make([]server.PeerView, 0, len(snap))
	for _, peer := range snap {
		v := server.PeerView{
			ID:       uint32(peer.ID),
			Protocol: server.ProtocolHomebrew,
			Callsign: peer.Callsign(),
			Address:  displayAddr(peer.Addr),
			State:    string(peer.State),
			Ready:    peer.State.CanPassTraffic(),
			IdleFor:  peer.Idle(now).Truncate(time.Second).String(),
		}
		// Counted per peer since 0294, so an inbound link can report what it is
		// carrying rather than a dash. Taken by address because the view
		// distinguishes an unmeasured protocol from a quiet one.
		received, sent, refused := peer.Received, peer.Sent, peer.Refused
		v.Received, v.Sent, v.Refused = &received, &sent, &refused
		if !peer.LastTraffic.IsZero() {
			idle := now.Sub(peer.LastTraffic).Seconds()
			v.TrafficIdle = &idle
		}
		if v.Callsign != "" {
			// A Homebrew peer states this at login, which is the strongest of
			// the three claims and the only one QSP does not construct.
			v.CallsignSource = server.CallsignFromPeer
		}
		if !peer.ConfiguredAt.IsZero() {
			v.ConnectedFor = now.Sub(peer.ConfiguredAt).Truncate(time.Second).String()
		}
		if peer.Config != nil {
			v.ColorCode = peer.Config.ColorCode
			// What it says it runs, verbatim. A linked QSP network and a
			// hotspot arrive by the same handshake on the same port, and this
			// is the only thing that tells them apart (ADR-0052).
			v.Software = peer.Config.SoftwareID
			if name, ok := LinkNameFromPackageID(peer.Config.PackageID); ok {
				v.LinkName = name
				v.Network = peer.Config.Description
				// **A server has no colour code, and "0" is a real one.** The
				// hotspots announce 01, 11 and 04; a QSP server has no radio
				// and sent 0, which the console displayed as though somebody
				// had chosen it. §7 forbids a field that means "not
				// applicable" and looks like "not set", and this is the same
				// shape as ipsc.colour_code, where 0 being legal meant an
				// unconfigured value validated and built every burst wrong.
				v.ColorCode = ""
			}
		}
		v.Attachments = attachmentViews(p.listener, peer.ID, now)
		// What the peer says about where it is. Unverified, and reported only
		// when it parses — a pin in the wrong place is believed, while a
		// missing one prompts somebody to ask.
		pos := peer.Position()
		v.Location = pos.Location
		v.Height = pos.Height
		v.PositionRefused = pos.Refused
		if pos.Located {
			lat, lon := pos.Latitude, pos.Longitude
			v.Latitude, v.Longitude = &lat, &lon
		}
		out = append(out, v)
	}
	return out
}

func (p peerViews) CallViews(now time.Time) (active, recent []server.CallView) {
	snap := p.listener.Calls()
	names := p.callsigns()

	for _, c := range snap.Active {
		active = append(active, p.callView(c, now, names))
	}
	for _, c := range snap.Recent {
		v := p.callView(c, now, names)
		v.Ago = now.Sub(c.Ended).Truncate(time.Second).String()
		v.EndedAt = c.Ended.UTC()
		recent = append(recent, v)
	}
	return active, recent
}

// callsigns maps radio IDs to callsigns QSP already knows.
//
// **Only an exact match counts.** A hotspot announces its own callsign when it
// registers, and on most hotspots the operator's radio carries the same DMR ID
// — so "3155413" is KB9TYC and QSP can say so without anybody's database.
//
// A radio behind a hotspot with a *different* ID is not resolved. QSP knows
// which hotspot it came through and nothing about whose radio it is, and
// labelling somebody else's transmission with the hotspot owner's callsign
// would be worse than a number. Resolving those needs a registry, which is a
// decision (§0) rather than a lookup.
func (p peerViews) callsigns() map[uint32]string {
	out := map[uint32]string{}
	for _, peer := range p.listener.Snapshot() {
		if peer.Callsign() != "" {
			out[uint32(peer.ID)] = peer.Callsign()
		}
	}
	return out
}

func (p peerViews) callView(c calls.Call, now time.Time, names map[uint32]string) server.CallView {
	return server.CallView{
		Source:     c.Source,
		SourceName: resolve(c.Source, names, p.names),
		TargetName: privateTargetName(c, names, p.names),
		Target:     c.Target,
		Group:      c.Group,
		Timeslot:   int(c.Key.Timeslot),
		// 10 ms granularity: DMR frames arrive 60 ms apart, so a coarser
		// truncation renders a short but real transmission as "0s", which reads
		// as nothing having happened.
		Duration: c.Duration(now).Truncate(10 * time.Millisecond).String(),
		Frames:   c.Frames,
		Voice:    c.Voice,
		Lost:     c.EndReason == calls.EndTimedOut,
	}
}

// processCheck reports that the process itself is running.
//
// It is trivially true, which is the point: it distinguishes "QSP answered and
// says it is unwell" from "nothing answered at all".
type processCheck struct{ started time.Time }

func (processCheck) Name() string { return "process" }

func (p processCheck) Check(context.Context) health.Result {
	res := health.Healthy("running")
	res.Detail = map[string]string{
		"uptime": time.Since(p.started).Truncate(time.Second).String(),
	}
	return res
}

// routingCheck reports whether traffic is being relayed.
//
// The earlier version of this said "the routing engine arrives in phase 2" long
// after it had arrived. A health summary that quotes a plan rather than the
// running instance is the same failure as fake data, just slower: it was true
// when written and nobody checked it again.
type routingCheck struct {
	enabled bool
	bridges int
}

func (routingCheck) Name() string { return "routing" }

func (c routingCheck) Check(context.Context) health.Result {
	if !c.enabled {
		return health.Unavailable(
			"forwarding is off, so peers on a talkgroup cannot hear each other; " +
				"set dmr.forwarding")
	}
	// **A master with no bridges is healthy, not degraded.**
	//
	// This reported degraded until now, which was correct while bridging was
	// the whole routing model and became wrong the day the master learned to
	// repeat. A club whose members all sit on one talkgroup configures no
	// bridges at all and is working exactly as intended; telling their operator
	// the instance is degraded sends them looking for a fault. See ADR-0019.
	if c.bridges == 0 {
		res := health.Healthy("peers on a talkgroup hear each other; no bridges configured")
		res.Detail = map[string]string{"bridges": "0"}
		return res
	}
	res := health.Healthy(fmt.Sprintf(
		"peers on a talkgroup hear each other, across %d bridge(s)", c.bridges))
	res.Detail = map[string]string{"bridges": strconv.Itoa(c.bridges)}
	return res
}

// schedulerCheck reports whether any bridge is scheduled.
type schedulerCheck struct {
	windows    int
	forwarding bool
}

func (schedulerCheck) Name() string { return "scheduler" }

func (c schedulerCheck) Check(context.Context) health.Result {
	if c.windows == 0 {
		return health.Unavailable("no schedule is configured; bridges follow their own enabled setting")
	}
	if !c.forwarding {
		return health.Degraded(
			fmt.Sprintf("%d scheduled window(s) configured, but forwarding is off so they relay nothing", c.windows),
			"set dmr.forwarding, or remove the schedule",
		)
	}
	res := health.Healthy(fmt.Sprintf("%d scheduled window(s)", c.windows))
	res.Detail = map[string]string{"windows": strconv.Itoa(c.windows)}
	return res
}

// transcoderEndpoint is the talkgroup and timeslot of a transcoder's endpoint
// in the first bridge that names it.
//
// **The first, and a transcoder named by two bridges is a choice**: routing
// matches a transcoder origin by name alone, so audio from it reaches every
// bridge that names it whichever talkgroup the frame carries. This value only
// decides what the frame says, which is what the ingress access check and the
// console read. The timeslot goes through timeslot(), the conversion the
// routing table uses, so the frame and the table cannot disagree about it.
func transcoderEndpoint(cfg config.Config, name string) (uint32, hbp.Timeslot) {
	key := strings.ToLower(strings.TrimSpace(name))
	for _, b := range cfg.DMR.Bridges {
		for _, e := range b.Endpoints {
			if strings.ToLower(strings.TrimSpace(e.Transcoder)) == key {
				return e.Talkgroup, timeslot(e.Timeslot)
			}
		}
	}
	return 0, hbp.Timeslot1
}

// unbuiltSubsystems have no implementation at all, only a place in the phase
// plan. Every other registered check describes something that exists, even when
// configuration has it switched off.
//
// That distinction is not carried by health.StatusUnavailable, which a disabled
// listener reports too. This list is the authoritative answer to "what has not
// been written yet", and docaccuracy_test.go checks the documentation against
// it. A subsystem leaves this list on the commit that implements it.
var unbuiltSubsystems = []struct{ name, arrives string }{

	// **The vocoder pool was removed from this list in 0310, not built.** QSP
	// does not decode audio and will not: it copies vocoder payloads and never
	// inspects them, which is why DMR-to-DMR needs no codec at all. Crossing
	// codecs — into AllStar, Zello or EchoLink, or between P25 Phase 1 and DMR
	// — needs an AMBE decoder, and in this hobby that is a hardware dongle.
	//
	// **What changed since, and these three entries said the old thing until
	// 0365.** They described the dongle as sitting behind an external
	// transcoder that QSP would hand audio to. ADR-0062 revised that: as much
	// as possible is built in QSP, and a thing leaves only when keeping it
	// would break a property QSP has decided to hold. So QSP now speaks the
	// AMBE-3000 packet format to an AMBEserver itself (ADR-0061,
	// internal/ambe), and real DMR audio has been decoded to speech on the
	// operator's bench — PROJECT_MEMORY §8r.
	//
	// The vocoder is still outside, because AMBEserver owns the serial port
	// and that keeps the operator's hardware available to the operator. What
	// is missing for each connector below is the connector, not the codec
	// path.
	{"allstar", "the AllStar connector arrives in phase 5; the vocoder link it needs is " +
		"built (internal/ambe, ADR-0061) and the connector is not"},
	{"zello", "the Zello connector arrives in phase 6; the vocoder link is built and " +
		"carries DMR and USRP both ways, and qsp-zello is built and has not made " +
		"its first connection — Opus stays outside QSP because every Go binding is cgo (ADR-0062)"},
	{"echolink", "the EchoLink connector arrives in phase 6; the vocoder link it needs is " +
		"built (internal/ambe, ADR-0061) and the connector is not"},
}

// unbuilt returns a check for a subsystem that does not exist yet.
//
// Constitution §3: an absent subsystem reports its absence. It is neither
// hidden from the report nor reported healthy.
func unbuilt(name, reason string) health.Checker {
	return health.CheckerFunc{
		CheckName: name,
		Fn:        func(context.Context) health.Result { return health.Unavailable(reason) },
	}
}

// joinSettings translates the configured join block into the shape the server
// serves at /api/join.
//
// The port comes from dmr.listen_address rather than being configured twice: a
// member must connect to the port QSP is actually listening on, and two places
// to state one fact is two places to get it wrong.
//
// The address is not derived. QSP could report the interface it bound, but
// 0.0.0.0 means nothing to a member and a container's address would be worse
// than silence — it looks authoritative and is wrong. An unset address leaves
// the page telling the member to ask their admin, which is true.
func joinSettings(cfg config.Config) server.JoinSettings {
	out := server.JoinSettings{
		NetworkName: cfg.DMR.Join.NetworkName,
		Address:     cfg.DMR.Join.Address,
		Port:        listenPort(cfg.DMR.ListenAddress),
		Talkgroups:  make([]server.JoinTalkgroup, 0, len(cfg.DMR.Join.Talkgroups)),
	}
	if out.Address == "" {
		out.AddressReason = "no address is configured; set dmr.join.address to the " +
			"host or IP members should point their hotspots at"
	}
	// Only when parrot is actually running. Naming a talkgroup that answers
	// nothing would produce a rule pointing at silence.
	if cfg.DMR.Parrot.Enabled {
		out.Parrot = cfg.DMR.Parrot.Talkgroup
	}
	for _, tg := range cfg.DMR.Join.Talkgroups {
		out.Talkgroups = append(out.Talkgroups, server.JoinTalkgroup{
			Name:     tg.Name,
			Dialled:  tg.Dialled,
			Arrives:  tg.Arrives,
			Timeslot: tg.Timeslot,
		})
	}
	return out
}

// listenPort extracts the UDP port from a listen address, falling back to the
// Homebrew Protocol's usual 62031 when it cannot be read. A wrong port on the
// join page is worse than a conventional one: the member would have no reason
// to doubt it.
func listenPort(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 62031
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return 62031
	}
	return port
}

// upstreamSender adapts a link set to the interface the listener wants, or
// returns nil when no links are configured.
//
// A typed nil in an interface is not a nil interface, and the listener checks
// for nil to decide whether an upstream delivery is a configuration error. This
// keeps that check working.
func upstreamSender(links *upstream.Set) peers.UpstreamSender {
	if links == nil || links.Len() == 0 {
		return nil
	}
	return links
}

// buildUpstreams creates a link for every enabled upstream.
//
// Disabled ones are skipped entirely rather than created and left closed: an
// administrator writes them down before the far end grants the bridge, and a
// link with no passphrase cannot be constructed. Returning nil when none are
// enabled keeps the common case free of machinery.
func buildUpstreams(log *slog.Logger, cfg config.Config, receive func(string, hbp.Data)) (*upstream.Set, error) {
	var enabled []config.Upstream
	for _, u := range cfg.DMR.Upstreams {
		if u.Enabled {
			enabled = append(enabled, u)
		}
	}
	if len(enabled) == 0 {
		return nil, nil
	}

	set := upstream.NewSet(log)
	for _, u := range enabled {
		var (
			link upstream.Connection
			err  error
		)
		if u.HomebrewProtocol() {
			link, err = buildPeerLink(log, u, cfg.DMR.Identity, cfg.DMR.Join.NetworkName, receive)
		} else {
			link, err = buildOpenBridgeLink(log, u, receive)
		}
		if err != nil {
			return nil, err
		}
		if err := set.Add(link); err != nil {
			return nil, err
		}
	}
	return set, nil
}

// buildOpenBridgeLink creates a bridge between two networks.
func buildOpenBridgeLink(log *slog.Logger, u config.Upstream, receive func(string, hbp.Data)) (upstream.Connection, error) {
	passphrase, err := config.LoadPeerPassword(os.ReadFile, u.PassphraseFile)
	if err != nil {
		return nil, fmt.Errorf("upstream %q: %w", u.Name, err)
	}
	if err := checkPasswordFileMode(u.PassphraseFile); err != nil {
		return nil, fmt.Errorf("upstream %q: %w", u.Name, err)
	}

	return upstream.New(log, upstream.Config{
		Name:          u.Name,
		ListenAddress: u.ListenAddress,
		TargetAddress: u.Address,
		NetworkID:     hbp.RepeaterID(u.NetworkID),
		Passphrase:    passphrase,
		StaleAfter:    time.Duration(u.StaleAfter),
		Receive:       receive,
	})
}

// softwareID is what this instance tells a far end it runs, within the 40
// bytes the field has.
//
// **A truncated commit is worse than none.** "QSP " plus the full build
// version is 44 characters, so the far end received
// `QSP 0.1.125 (v0.1.94-0.20260908175846-39` — a hash cut mid-word, which
// looks like a commit and matches nothing. Seen on production's peers API on
// 2026-09-08, and it would have been read as a real identifier by anybody
// checking which build a linked server was running.
//
// The release and a short hash fit and are both usable: the release is what an
// operator quotes, and seven characters of hash is what git itself abbreviates
// to.
func softwareID() string {
	const width = 40
	id := "QSP " + buildinfo.Version
	if commit := shortCommit(); commit != "" {
		id += " " + commit
	}
	if len(id) > width {
		id = id[:width]
	}
	return id
}

// shortCommit abbreviates the commit this binary was built from, or returns
// empty when it cannot be known — a container built without git, or a build
// from an unversioned tree.
func shortCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return ""
}

// qspLinkPackageID prefixes the link name a QSP server announces to the far
// end, so the far end can recognise the field as a name rather than a version.
//
// PackageID is 40 bytes on the wire and conventionally carries a build
// identifier, which QSP has no separate use for: SoftwareID already says what
// this is and which version. Reusing it needs no new field and no protocol
// change, and a station that does not understand it displays a harmless string
// on a dashboard.
const qspLinkPackageID = "QSP-LINK:"

// LinkNameFromPackageID recovers the name a QSP server announced, and reports
// whether the field carried one.
func LinkNameFromPackageID(pkg string) (string, bool) {
	if !strings.HasPrefix(pkg, qspLinkPackageID) {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(pkg, qspLinkPackageID))
	return name, name != ""
}

// linkDescription is what the far end shows beside an inbound link.
//
// The network's name rather than the station's, because the thing that dialled
// in is a network: an administrator reading "BCARA" learns something, and
// reading a repeater description learns nothing that is true. Falls back to the
// operator's own description when no network name is set.
func linkDescription(network string, ident config.UpstreamIdentity) string {
	if n := strings.TrimSpace(network); n != "" {
		return n
	}
	return ident.Description
}

// qspLinkNames lists the enabled links that reach another QSP server.
//
// **Derived from the configuration rather than asserted beside it.** A second
// list of link names kept by hand is a second thing to keep true, and this
// project's recurring defect is a value declared in one place and read from
// another that has drifted.
func qspLinkNames(cfg config.Config) []string {
	var out []string
	for _, u := range cfg.DMR.Upstreams {
		if u.Enabled && u.QSPLink() {
			out = append(out, u.Name)
		}
	}
	return out
}

// buildPeerLink creates an outbound link that logs into another master.
//
// **This must not be pointed at BrandMeister**, whose operators define peer
// bridging as prohibited. ADR-0018 records that and ADR-0024 records that
// building the capability did not change it. QSP does not detect the far end,
// because carrying one network's hostnames in the codebase is what §0 refused
// for talkgroup lists and for the same reasons.
func buildPeerLink(log *slog.Logger, u config.Upstream, id config.Identity, network string, receive func(string, hbp.Data)) (upstream.Connection, error) {
	password, err := config.LoadPeerPassword(os.ReadFile, u.PasswordFile)
	if err != nil {
		return nil, fmt.Errorf("upstream %q: %w", u.Name, err)
	}
	if err := checkPasswordFileMode(u.PasswordFile); err != nil {
		return nil, fmt.Errorf("upstream %q: %w", u.Name, err)
	}

	// Validate requires an identity for an enabled homebrew link, so a nil one
	// here means Validate and this disagree. Failing is better than sending a
	// blank callsign, which appears on the far end's dashboard as an
	// unidentified station.
	if u.Identity == nil {
		return nil, fmt.Errorf("upstream %q: no identity is configured; the far end shows "+
			"it to its own users", u.Name)
	}

	// **Filled from the instance's identity where the link says nothing.** A
	// station has one callsign and one position; stating them once per link was
	// three chances to disagree with itself.
	ident := id.Merge(*u.Identity)

	hb, err := homebrew.New(homebrew.Config{
		Name:       u.Name,
		RepeaterID: hbp.RepeaterID(u.RepeaterID),
		Password:   password,
		Identity: homebrew.Identity{
			Callsign:    ident.Callsign,
			RXFrequency: ident.RXFrequency,
			TXFrequency: ident.TXFrequency,
			ColourCode:  ident.ColourCode,
			Latitude:    ident.Latitude,
			Longitude:   ident.Longitude,
			Height:      ident.Height,
			Location:    ident.Location,
			Description: linkDescription(network, ident),
			URL:         ident.URL,
			Timeslots:   ident.Timeslots,
			SoftwareID:  softwareID(),
			// **The name travels with the link** (ADR-0052). The far end has
			// no name of its own for a link that dialled in — it receives a
			// registration, not a configuration — so without this the two
			// administrators of one link call it different things, which is a
			// support conversation nobody can have.
			PackageID: qspLinkPackageID + u.Name,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("upstream %q: %w", u.Name, err)
	}

	return upstream.NewPeer(log, upstream.PeerConfig{
		Name:          u.Name,
		TargetAddress: u.Address,
		Link:          hb,
		Receive:       receive,
	})
}

// mapSettings translates the configuration's map block for the console.
//
// A function rather than an inline literal because a saved configuration
// rebuilds it, and two copies of the translation would drift.
func mapSettings(cfg config.Config) server.MapSettings {
	return server.MapSettings{
		TileURL:     cfg.Server.Map.TileURL,
		Attribution: cfg.Server.Map.Attribution,
		MaxZoom:     cfg.Server.Map.MaxZoom,
	}
}

// resolve names a radio, preferring what a hotspot said about itself.
//
// **The exact match wins.** A hotspot's own registration is that station
// describing itself, and it is right more often than a registry for the case it
// covers — a reassigned or misregistered radio ID is somebody else's record,
// and the station in front of you is not.
func resolve(id uint32, known map[uint32]string, names *callsigns.Service) string {
	if name, ok := known[id]; ok {
		return name
	}
	if names == nil {
		return ""
	}
	if e, ok := names.Lookup(id); ok {
		return e.Display()
	}
	return ""
}

// privateTargetName resolves the called party, for a private call only.
//
// A group call's target is a talkgroup number and has no callsign; looking one
// up finds whichever radio happens to share the number, which is how a
// talkgroup ends up labelled with a stranger's call.
func privateTargetName(c calls.Call, known map[uint32]string, names *callsigns.Service) string {
	if c.Group {
		return ""
	}
	return resolve(c.Target, known, names)
}

// linkSource adapts the upstream set for the console.
//
// The configuration is carried alongside because a link knows its own state and
// not what it was configured from: the far end's address and the announced
// network ID live in the configuration, and an operator looking at a console
// needs both halves to tell a working link from a misaddressed one.
func linkSource(set *upstream.Set, cfg config.Config) server.LinkSource {
	if set == nil || len(cfg.DMR.Upstreams) == 0 {
		return nil
	}
	return &links{set: set, cfg: cfg}
}

type links struct {
	set *upstream.Set
	cfg config.Config
}

func (l *links) LinkStatuses() []server.LinkStatus {
	byName := map[string]config.Upstream{}
	for _, u := range l.cfg.DMR.Upstreams {
		byName[u.Name] = u
	}

	statuses := l.set.Statuses()
	out := make([]server.LinkStatus, 0, len(statuses))
	for _, st := range statuses {
		u := byName[st.Name]
		entry := server.LinkStatus{
			Name:      st.Name,
			Protocol:  u.Protocol,
			FarEnd:    u.Address,
			Listening: u.ListenAddress,
			NetworkID: u.NetworkID,
			Open:      st.Open,
			// What the far end announced about itself, which until 0297
			// nothing carried: an outbound link showed an address where the
			// other operator's console showed a name (ADR-0052 rule 3).
			Network:      st.FarEndNetwork,
			Software:     st.FarEndSoftware,
			Measured:     true,
			Sent:         st.Stats.Sent,
			Received:     st.Stats.Received,
			Rejected:     st.Stats.Rejected,
			EverReceived: st.EverReceived,
			Summary:      st.Summary,
			Advice:       st.Advice,
		}
		if st.EverReceived {
			entry.IdleSeconds = int(st.Since.Seconds())
		}
		out = append(out, entry)
	}
	return out
}

// callHistory adapts the call store for the console, keeping a nil store nil
// rather than handing the server an interface holding one.
//
// A typed nil in an interface is not nil, and the handler's check would pass on
// a store that cannot answer.
func callHistory(store *calls.Store) server.CallHistory {
	if store == nil {
		return nil
	}
	return store
}

// attachmentViews lists what a peer is receiving, static first then by
// talkgroup.
//
// Nil when subscription is off: every peer then receives everything, and a list
// would imply a limit that does not exist.
func attachmentViews(l *peers.Listener, peer hbp.RepeaterID, now time.Time) []server.AttachmentView {
	if l == nil || !l.SubscriptionEnabled() {
		return nil
	}
	var out []server.AttachmentView
	for _, a := range l.Attachments() {
		if a.Peer != peer {
			continue
		}
		view := server.AttachmentView{
			Talkgroup: a.Talkgroup,
			Timeslot:  int(a.Timeslot),
			Static:    a.Static,
		}
		if !a.LastUsed.IsZero() {
			view.IdleFor = now.Sub(a.LastUsed).Truncate(time.Second).String()
		}
		out = append(out, view)
	}
	return out
}

// serverIdentity is what this server announces to a QSP server that registers
// with it.
//
// **The display name first, then the callsign, and neither is invented.** A
// server with nothing configured announces empty fields, and the far end's
// console reports that it was not announced rather than guessing — an invented
// name is worse than an address, because an address is at least true.
func serverIdentity(cfg config.Config) hbp.Identity {
	return hbp.Identity{
		Network:     strings.TrimSpace(cfg.DMR.Join.NetworkName),
		Callsign:    strings.TrimSpace(cfg.DMR.Identity.Callsign),
		Software:    buildVersion(),
		Description: strings.TrimSpace(cfg.DMR.Identity.Description),
		ServerID:    strings.TrimSpace(cfg.Server.Identifier),
	}
}

// setupOrNil and accountsOrNil keep a nil *auth.Service out of a non-nil
// interface.
//
// **A typed nil in an interface is not nil**, so assigning one directly would
// make every `s.opts.Setup == nil` check false and the handlers would call
// methods on nothing. It is the oldest trap in the language and it is worth two
// functions to stay out of.
func setupOrNil(s *auth.Service) server.SetupAccounts {
	if s == nil {
		return nil
	}
	return s
}

func accountsOrNil(s *auth.Service) server.AccountAdmin {
	if s == nil {
		return nil
	}
	return s
}
