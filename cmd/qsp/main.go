// Command qsp runs the QSP linking server.
//
// This build implements the DMR Homebrew Protocol, the peer lifecycle, call
// observation, and routing with scheduled and PTT-triggered bridging, behind a
// web console. P25, the vocoder pool and the analog connectors (AllStar, Zello,
// EchoLink) are later phases and are not present; the health endpoint reports
// each of them as unavailable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/k9mls/qsp/internal/buildinfo"
	"syscall"

	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/logging"
)

// version is set at build time with
// -ldflags "-X main.version=vX.Y.Z". It falls back to the module's build info.
var version = ""

func main() {
	if err := realMain(); err != nil {
		fmt.Fprintf(os.Stderr, "qsp: %v\n", err)
		os.Exit(1)
	}
}

// errCheckFailed reports an invalid configuration to -check without printing
// the error twice: loadConfig has already said what is wrong and where.
var errCheckFailed = errors.New("configuration is not usable")

// configName describes what was checked, for a message an operator reads while
// deciding whether to restart anything.
func configName(path string) string {
	if path == "" {
		return "the built-in defaults"
	}
	return path
}

// realMain exists so that every deferred function runs before the process
// exits; os.Exit inside main would skip them.
func realMain() error {
	var (
		configPath  = flag.String("config", "", "path to a configuration file (default: built-in defaults)")
		printConfig = flag.Bool("print-config", false, "write the effective configuration to stdout and exit")
		showVersion = flag.Bool("version", false, "print the version and exit")
		check       = flag.Bool("check", false,
			"validate the configuration and exit, without starting anything")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(buildVersion())
		return nil
	}

	// **Before loading, because there may be nothing to load.** A first run in
	// a container has an empty volume: no configuration, no password file. See
	// bootstrap.go for why the one thing asked for is a peer password rather
	// than a radio ID.
	//
	// Only when -config names a path. Without one QSP runs on built-in
	// defaults and writing a file somebody did not ask for would be a
	// surprise, not a convenience.
	wroteConfig, err := bootstrapConfig(*configPath, os.Getenv)
	if err != nil {
		if errors.Is(err, errNoPeerPassword) || errors.Is(err, errNoAllowedPeers) {
			explainFirstRun(os.Stderr, *configPath)
			return errCheckFailed
		}
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		if *check {
			// The point of -check is to be run before a restart, so it says
			// what is wrong rather than only that something is.
			fmt.Fprintln(os.Stderr, err)
			return errCheckFailed
		}
		return err
	}

	// **-check exists because a configuration edit was verified by restarting
	// the service.** An invalid file then takes the network down and reports
	// itself in a journal, and systemd gives up after five attempts. Validation
	// happens here already; the only thing missing was a way to ask for it
	// without binding a socket, opening a database, or dropping a member.
	if *check {
		fmt.Printf("%s is valid\n", configName(*configPath))
		return nil
	}

	// Subcommands come after the flags so that -config is honoured: adduser
	// must write to the database the server reads, and an account created
	// somewhere else is one the server will never see.
	if args := flag.Args(); len(args) > 0 {
		switch args[0] {
		case "adduser":
			if len(args) != 2 {
				return errors.New("usage: qsp [-config path] adduser <username>")
			}
			return adduser(context.Background(), cfg, args[1])
		case "unlock":
			if len(args) != 2 {
				return errors.New("usage: qsp [-config path] unlock <username>")
			}
			return unlock(context.Background(), cfg, args[1])
		default:
			return fmt.Errorf("unknown command %q; the commands are \"adduser\" and \"unlock\"", args[0])
		}
	}

	if *printConfig {
		return config.Save(os.Stdout, cfg)
	}

	level, err := logging.ParseLevel(cfg.Logging.Level)
	if err != nil {
		return err
	}
	format, err := logging.ParseFormat(cfg.Logging.Format)
	if err != nil {
		return err
	}
	log := logging.New(os.Stderr, logging.Options{
		Level:     level,
		Format:    format,
		AddSource: cfg.Logging.IncludeSource,
	})

	log.Info("starting", "version", buildVersion())

	// **A file appearing in a mounted volume without a word is a surprise the
	// next operator has to work out for themselves.** Said once, at info, so a
	// first run is distinguishable from every run after it.
	if wroteConfig {
		log.Info("wrote a starting configuration", "path", *configPath,
			"note", "nothing can register until its repeater ID is listed in dmr.access")
	}

	// Signal handling is established before any subsystem starts so that an
	// interrupt during startup is honoured rather than killing the process
	// mid-construction.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The path travels with the configuration so that a save writes the file
	// this instance was started from, and not one it guessed at.
	a, err := build(ctx, cfg, *configPath, log)
	if err != nil {
		return err
	}

	runErr := a.run(ctx)

	// Shutdown must not inherit the cancelled context, or every bounded
	// operation would abort immediately.
	shutdownErr := a.shutdown(context.Background())

	if runErr != nil {
		return runErr
	}
	if shutdownErr != nil {
		return fmt.Errorf("shutdown was not clean: %w", shutdownErr)
	}
	log.Info("stopped")
	return nil
}

func loadConfig(path string) (config.Config, error) {
	if path == "" {
		return config.Default(), nil
	}
	f, err := os.Open(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("cannot open configuration file %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	cfg, err := config.Load(f)
	if err != nil {
		return config.Config{}, fmt.Errorf("configuration file %q is not usable: %w", path, err)
	}
	return cfg, nil
}

// buildVersion reports the release number and the commit it was built from.
//
// **Both, because they answer different questions.** The release number is what
// an operator puts in a release note or tells a member; the commit is how they
// check that the binary running on a server is the one they just built, which
// §7 requires because systemctl reports that something started and not what.
//
// A linker-injected value still wins, so a release pipeline can override the
// constant. Everything else comes from the ordinary build.
func buildVersion() string {
	release := version
	if release == "" {
		release = buildinfo.Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return release + " (" + info.Main.Version + ")"
	}
	return release + " (development build)"
}
