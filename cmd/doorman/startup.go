package main

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/santiagosayshey/doorman/internal/config"
)

// version is "dev" for local builds. Releases set it at build time:
//
//	go build -ldflags "-X main.version=0.1.0" ./cmd/doorman
var version = "dev"

// announceStartup prints the one-line banner, then logs the same details as
// a structured record. The banner is skipped for JSON logs, where a
// non-JSON line would break the tools that parse them.
func announceStartup(w io.Writer, log *slog.Logger, cfg *config.Config, configPath, listen string) {
	destinations, keys := count(cfg)

	if cfg.Logging.Format == config.FormatText {
		fmt.Fprintf(w, "▌doorman %s ▸ %s ▸ %s ▸ %s\n",
			version, listen, plural(destinations, "destination"), plural(keys, "key"))
	}

	log.Info("doorman started",
		"version", version,
		"listen", listen,
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
