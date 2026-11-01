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

func TestBannerWithTextLogs(t *testing.T) {
	var out, logs bytes.Buffer
	announceStartup(&out, slog.New(slog.NewTextHandler(&logs, nil)), testConfig(config.FormatText), "doorman.yaml", ":8800")

	want := "▌doorman dev ▸ :8800 ▸ 2 destinations ▸ 3 keys\n"
	if out.String() != want {
		t.Errorf("banner = %q, want %q", out.String(), want)
	}
	if !strings.Contains(logs.String(), `msg="doorman started"`) {
		t.Errorf("the startup record is missing: %q", logs.String())
	}
}

func TestNoBannerWithJSONLogs(t *testing.T) {
	var out, logs bytes.Buffer
	announceStartup(&out, slog.New(slog.NewJSONHandler(&logs, nil)), testConfig(config.FormatJSON), "doorman.yaml", ":8800")

	if out.Len() != 0 {
		t.Errorf("JSON logs should get no banner, got %q", out.String())
	}
	if !strings.Contains(logs.String(), `"destinations":2`) {
		t.Errorf("the startup record should still be logged: %q", logs.String())
	}
}

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "0 keys", 1: "1 key", 5: "5 keys"} {
		if got := plural(n, "key"); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
}
