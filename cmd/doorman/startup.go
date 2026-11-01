package main

import (
	"fmt"
	"log/slog"

	"github.com/santiagosayshey/doorman/internal/config"
)

// version is "dev" for local builds. Releases set it at build time:
//
//	go build -ldflags "-X main.version=0.1.0" ./cmd/doorman
var version = "dev"

// announceStartup logs that Doorman is up and what it loaded.
func announceStartup(log *slog.Logger, cfg *config.Config, configPath string) {
	destinations, keys := count(cfg)
	log.Info("🚪 doorman started",
		"version", version,
		"listen", cfg.Listen,
		"config", configPath,
		"destinations", destinations,
		"keys", keys,
	)
}

// count returns how many destinations and keys the config has.
func count(cfg *config.Config) (destinations, keys int) {
	for _, dest := range cfg.Destinations {
		keys += len(dest.Keys)
	}
	return len(cfg.Destinations), keys
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
