//go:build zello

// Command qsp-zello connects one Zello channel to a QSP transcoder.
//
// It is a separate binary because Opus needs cgo and QSP does not link it
// (ADR-0009), and it holds no credential of its own: it asks QSP for a logon
// over a local socket every time it connects (ADR-0066).
//
//	QSP transcoder  <-- USRP, 8 kHz PCM -->  qsp-zello  <-- WebSocket, Opus -->  Zello
//
// Build with `CGO_ENABLED=1 go build -tags zello ./cmd/qsp-zello`, which needs
// libopus development headers. A plain QSP build never compiles this.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/k9mls/qsp/internal/buildinfo"
	"github.com/k9mls/qsp/internal/opus"
)

func main() {
	configPath := flag.String("config", "/var/lib/qsp/qsp-zello.json", "path to the connector's configuration")
	version := flag.Bool("version", false, "print the version and exit")
	check := flag.Bool("check", false, "validate the configuration and exit")
	flag.Parse()

	if *version {
		fmt.Printf("qsp-zello %s (%s)\n", buildinfo.Version, opus.Version())
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qsp-zello: %v\n", err)
		os.Exit(2)
	}
	if *check {
		fmt.Printf("%s is valid\n", *configPath)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("starting", slog.String("version", buildinfo.Version),
		slog.String("channel", cfg.Channel), slog.String("logon_socket", cfg.LogonSocket))

	if err := run(ctx, cfg, log); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
