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

// realMain exists so that every deferred function runs before the process
// exits; os.Exit inside main would skip them.
func realMain() error {
	var (
		configPath  = flag.String("config", "", "path to a configuration file (default: built-in defaults)")
		printConfig = flag.Bool("print-config", false, "write the effective configuration to stdout and exit")
		showVersion = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(buildVersion())
		return nil
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
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

	// Signal handling is established before any subsystem starts so that an
	// interrupt during startup is honoured rather than killing the process
	// mid-construction.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := build(ctx, cfg, log)
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

// buildVersion reports the version, preferring the linker-injected value and
// falling back to module build information.
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "development build"
}
