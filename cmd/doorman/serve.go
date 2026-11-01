package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/santiagosayshey/doorman/internal/config"
	"github.com/santiagosayshey/doorman/internal/proxy"
)

// shutdownGrace is how long in-flight requests get to finish on shutdown.
// It stays under Docker's default 10 second stop timeout, after which Docker
// kills the process.
const shutdownGrace = 8 * time.Second

// app holds the running config and swaps in a new one on reload.
type app struct {
	path    string
	log     *slog.Logger
	cfg     *config.Config // the config in use; reload replaces it
	handler atomic.Pointer[proxy.Handler]
}

// ServeHTTP sends each request to the handler that's current when it
// arrives. A request that started before a reload finishes on the old one.
func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.handler.Load().ServeHTTP(w, r)
}

// reload loads the config file again and switches to it if it's valid. An
// invalid file is reported and the current config keeps running: a typo must
// never take the proxy down. Listen and logging need a restart to change,
// because the port and the open log files belong to the running process.
func (a *app) reload() {
	next, err := config.Load(a.path)
	if err != nil {
		var invalid *config.ValidationError
		if errors.As(err, &invalid) {
			a.log.Error(
				"reload failed, keeping the current config",
				"config",
				a.path,
				"problems",
				len(invalid.Problems),
			)
			for _, p := range invalid.Problems {
				a.log.Error("reload problem", "problem", p)
			}
		} else {
			a.log.Error("reload failed, keeping the current config", "config", a.path, "error", err)
		}
		return
	}

	handler, err := proxy.New(next, a.log)
	if err != nil {
		a.log.Error("reload failed, keeping the current config", "error", err)
		return
	}

	if next.Admin.Listen != a.cfg.Admin.Listen {
		a.log.Warn("admin.listen changed; restart to use it",
			"current", a.cfg.Admin.Listen, "new", next.Admin.Listen)
	}
	if next.Listen != a.cfg.Listen {
		a.log.Warn("listen changed; restart to use it", "current", a.cfg.Listen, "new", next.Listen)
	}
	if !reflect.DeepEqual(loggingSettings(next), loggingSettings(a.cfg)) {
		a.log.Warn("logging settings changed; restart to use them")
	}

	a.handler.Store(handler)
	a.cfg = next
	destinations, keys := count(next)
	a.log.Info("🔄 config reloaded", "destinations", destinations, "keys", keys)
}

// loggingSettings collects everything that decides where and how Doorman
// logs, so a reload can tell whether any of it changed.
func loggingSettings(cfg *config.Config) any {
	files := map[string]config.DestinationLog{}
	for name, dest := range cfg.Destinations {
		if dest.Log != nil {
			files[name] = *dest.Log
		}
	}
	return struct {
		Logging config.Logging
		Files   map[string]config.DestinationLog
	}{cfg.Logging, files}
}

// serve runs the server on ln until ctx is cancelled, reloading the config
// whenever reload receives. On cancel it stops accepting connections and
// gives in-flight requests shutdownGrace to finish.
func (a *app) serve(ctx context.Context, ln, adminLn net.Listener, reload <-chan struct{}) error {
	admin := &http.Server{Handler: adminHandler(), ReadHeaderTimeout: 10 * time.Second}
	server := &http.Server{
		Handler: a,
		// A client gets 10 seconds to send its headers. Without a limit, slow
		// clients could hold connections open forever.
		ReadHeaderTimeout: 10 * time.Second,
	}

	failed := make(chan error, 2)
	go func() { failed <- server.Serve(ln) }()
	go func() { failed <- admin.Serve(adminLn) }()

	for {
		select {
		case <-reload:
			a.reload()
		case err := <-failed:
			return err
		case <-ctx.Done():
			a.log.Info(
				"shutting down, letting in-flight requests finish",
				"grace",
				shutdownGrace.String(),
			)
			// ctx is already cancelled, so the grace period starts from a copy
			// of it that can't be cancelled.
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				a.log.Warn("in-flight requests were cut off", "error", err)
			}
			_ = admin.Shutdown(shutdownCtx)
			a.log.Info("stopped")
			return nil
		}
	}
}
