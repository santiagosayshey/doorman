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
	if len(os.Args) > 1 && os.Args[1] == "validate" {
		os.Exit(runValidate(os.Args[2:]))
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
	fmt.Printf("✓ %s is valid: %s, %s\n", *path, plural(destinations, "destination"), plural(keys, "key"))
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
	defer closeLogs()
	slog.SetDefault(log)

	handler, err := proxy.New(cfg, log)
	if err != nil {
		log.Error("building the proxy failed", "error", err)
		return 1
	}
	a := &app{path: *path, log: log, cfg: cfg}
	a.handler.Store(handler)

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Error("can't listen", "listen", cfg.Listen, "error", err)
		return 1
	}

	// Ctrl+C and docker stop shut down gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// SIGHUP and, unless -watch=false, saving the file both reload the config.
	reload := make(chan struct{}, 1)
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			reload <- struct{}{}
		}
	}()
	if *watch {
		go watchFile(ctx, *path, loaded, time.Second, reload)
	}

	announceStartup(log, cfg, *path)
	if err := a.serve(ctx, ln, reload); err != nil {
		log.Error("server stopped", "error", err)
		return 1
	}
	return 0
}

// load reads the config, printing every problem when it isn't valid.
func load(path string) (*config.Config, bool) {
	cfg, err := config.Load(path)

	var problems config.Problems
	switch {
	case errors.As(err, &problems):
		fmt.Printf("✗ %d problem(s) in %s\n", len(problems), path)
		for _, p := range problems {
			fmt.Printf("  %s\n", p)
		}
		return nil, false
	case err != nil: // the file couldn't be read at all
		fmt.Printf("✗ %v\n", err)
		return nil, false
	}
	return cfg, true
}
