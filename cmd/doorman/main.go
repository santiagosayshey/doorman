// Command doorman is a reverse proxy that gives each client its own API key
// with an allowlist of what it may do on one destination.
//
//	doorman -c doorman.yaml            serve
//	doorman validate -c doorman.yaml   check a config and list every problem
//	doorman health -c doorman.yaml     ask a running doorman whether it's up
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/santiagosayshey/doorman/internal/config"
	"github.com/santiagosayshey/doorman/internal/logging"
	"github.com/santiagosayshey/doorman/internal/proxy"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "validate":
			os.Exit(runValidate(os.Args[2:]))
		case "health":
			os.Exit(runHealth(os.Args[2:]))
		}
	}
	os.Exit(runProxy(os.Args[1:]))
}

// runValidate checks a config file and lists every problem in it.
// It returns the exit code: 0 when the file is valid, 1 when it isn't.
func runValidate(args []string) int {
	flags := flag.NewFlagSet("validate", flag.ExitOnError)
	path := flags.String("c", "doorman.yaml", "config file to check")
	_ = flags.Parse(args) // ExitOnError: a bad flag exits before this returns

	cfg, ok := load(*path)
	if !ok {
		return 1
	}
	destinations, keys := count(cfg)
	fmt.Printf(
		"✓ %s is valid: %s, %s\n",
		*path,
		plural(destinations, "destination"),
		plural(keys, "key"),
	)
	return 0
}

// runProxy loads the config and serves until the server stops.
func runProxy(args []string) int {
	flags := flag.NewFlagSet("doorman", flag.ExitOnError)
	path := flags.String("c", "doorman.yaml", "config file")
	watch := flags.Bool("watch", true, "reload the config when the file changes")
	_ = flags.Parse(args)

	// Fingerprint the file before loading it, so the watcher can't miss a
	// save that lands in between.
	loaded, _ := fingerprint(*path)
	cfg, ok := load(*path)
	if !ok {
		return 1
	}

	log, closeLogs, err := logging.Setup(cfg, os.Stdout)
	if err != nil {
		fmt.Printf("✗ opening log files: %v\n", err)
		return 1
	}
	defer func() {
		if err := closeLogs(); err != nil {
			log.Warn("closing log files failed", "error", err)
		}
	}()
	slog.SetDefault(log)

	handler, err := proxy.New(cfg, log)
	if err != nil {
		log.Error("building the proxy failed", "error", err)
		return 1
	}
	a := &app{path: *path, log: log, cfg: cfg}
	a.handler.Store(handler)

	// Ctrl+C and docker stop shut down gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		log.Error("can't listen", "listen", cfg.Listen, "error", err)
		return 1
	}
	adminLn, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Admin.Listen)
	if err != nil {
		log.Error("can't listen", "admin_listen", cfg.Admin.Listen, "error", err)
		return 1
	}

	reload := reloadTriggers(ctx, *path, loaded, *watch)

	announceStartup(log, cfg, *path)
	if err := a.serve(ctx, ln, adminLn, reload); err != nil {
		log.Error("server stopped", "error", err)
		return 1
	}
	return 0
}

// load reads the config, printing every problem when it isn't valid.
func load(path string) (*config.Config, bool) {
	cfg, err := config.Load(path)

	var invalid *config.ValidationError
	switch {
	case errors.As(err, &invalid):
		fmt.Printf("✗ %d problem(s) in %s\n", len(invalid.Problems), path)
		for _, p := range invalid.Problems {
			fmt.Printf("  %s\n", p)
		}
		return nil, false
	case err != nil: // the file couldn't be read at all
		fmt.Printf("✗ %v\n", err)
		return nil, false
	}
	return cfg, true
}

// reloadTriggers returns a channel that receives whenever the config should
// be reloaded: on SIGHUP, and on a saved change to the file unless watch is
// off.
func reloadTriggers(ctx context.Context, path string, loaded [32]byte, watch bool) <-chan struct{} {
	reload := make(chan struct{}, 1)

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			reload <- struct{}{}
		}
	}()

	if watch {
		go watchFile(ctx, path, loaded, time.Second, reload)
	}
	return reload
}
