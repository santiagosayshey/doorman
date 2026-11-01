package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/santiagosayshey/doorman/internal/config"
)

func testConfig(format config.Format) *config.Config {
	return &config.Config{
		Logging: config.Logging{Format: format},
		Destinations: map[string]config.Destination{
			"radarr": {Keys: map[string]config.Key{"homepage": {}, "autobrr": {}}},
			"sonarr": {Keys: map[string]config.Key{"autobrr": {}}},
		},
	}
}

func TestStartupRecord(t *testing.T) {
	var logs bytes.Buffer
	cfg := testConfig(config.FormatJSON)
	cfg.Listen = ":8800"
	announceStartup(slog.New(slog.NewJSONHandler(&logs, nil)), cfg, "doorman.yaml")

	for _, want := range []string{`"msg":"🚪 doorman started"`, `"listen":":8800"`, `"destinations":2`, `"keys":3`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the startup record is missing %s: %q", want, logs.String())
		}
	}
}

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "0 keys", 1: "1 key", 5: "5 keys"} {
		if got := plural(n, "key"); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
}
