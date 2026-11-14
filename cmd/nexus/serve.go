package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/ntsrihari/knowhere-nexus/internal/app"
	"github.com/ntsrihari/knowhere-nexus/internal/buildinfo"
	"github.com/ntsrihari/knowhere-nexus/internal/config"
)

const defaultConfigPath = "/etc/nexus/config.yaml"

func serveCmd(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", defaultConfigPath, "path to config.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// Files Nexus creates (database, WAL) must never be group/other readable.
	syscall.Umask(0o077)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	log := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	log.Info("starting", "build", buildinfo.String())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg, log, app.Options{})
	if err != nil {
		log.Error("startup failed", "error", err.Error())
		return 1
	}
	defer a.Close()
	if err := a.Serve(ctx); err != nil {
		log.Error("server failed", "error", err.Error())
		return 1
	}
	return 0
}
