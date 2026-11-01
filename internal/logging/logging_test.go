package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santiagosayshey/doorman/internal/config"
)

// newTestRouter returns a logger whose stdout logs at info and whose radarr
// destination logs at debug, plus the buffers each one writes to.
func newTestRouter() (*slog.Logger, *bytes.Buffer, *bytes.Buffer) {
	var stdout, radarr bytes.Buffer
	router := NewRouter(
		slog.NewTextHandler(&stdout, &slog.HandlerOptions{Level: slog.LevelInfo}),
		map[string]slog.Handler{
			"radarr": slog.NewTextHandler(&radarr, &slog.HandlerOptions{Level: slog.LevelDebug}),
		},
	)
	return slog.New(router), &stdout, &radarr
}

func TestAppEventsGoToStdoutOnly(t *testing.T) {
	log, stdout, radarr := newTestRouter()
	log.Info("config loaded")

	if !strings.Contains(stdout.String(), "config loaded") {
		t.Errorf("stdout is missing the app event: %q", stdout)
	}
	if radarr.Len() != 0 {
		t.Errorf("a destination file got an app event: %q", radarr)
	}
}

func TestDestinationRecordsGoToBoth(t *testing.T) {
	log, stdout, radarr := newTestRouter()
	log.With(DestinationKey, "radarr").Info("allowed", "path", "/api/v3/movie")

	for name, buf := range map[string]*bytes.Buffer{"stdout": stdout, "radarr": radarr} {
		if !strings.Contains(buf.String(), "allowed") {
			t.Errorf("%s is missing the request record: %q", name, buf)
		}
	}
}

func TestDestinationAttributeOnTheRecordAlsoRoutes(t *testing.T) {
	log, _, radarr := newTestRouter()
	log.Info("allowed", DestinationKey, "radarr")

	if !strings.Contains(radarr.String(), "allowed") {
		t.Errorf("a destination attribute on the record itself should route: %q", radarr)
	}
}

func TestLevelsAreIndependent(t *testing.T) {
	log, stdout, radarr := newTestRouter()
	log.With(DestinationKey, "radarr").Debug("rule matched")

	if stdout.Len() != 0 {
		t.Errorf("stdout is at info and should drop debug: %q", stdout)
	}
	if !strings.Contains(radarr.String(), "rule matched") {
		t.Errorf("radarr is at debug and should keep it: %q", radarr)
	}
}

func TestUnknownDestinationGoesToStdoutOnly(t *testing.T) {
	log, stdout, radarr := newTestRouter()
	log.With(DestinationKey, "sonarr").Info("allowed")

	if !strings.Contains(stdout.String(), "allowed") || radarr.Len() != 0 {
		t.Errorf(
			"a destination without a file should log to stdout only; stdout=%q radarr=%q",
			stdout,
			radarr,
		)
	}
}

func TestSetupOpensDestinationFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "radarr.log")
	cfg := &config.Config{
		Logging: config.Logging{Format: config.FormatJSON},
		Destinations: map[string]config.Destination{
			"radarr": {Log: &config.DestinationLog{Path: path}},
			"sonarr": {}, // no log section: stdout only
		},
	}

	var stdout bytes.Buffer
	log, closeFiles, err := Setup(cfg, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	log.With(DestinationKey, "radarr").Info("allowed")
	if err := closeFiles(); err != nil {
		t.Fatal(err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `"msg":"allowed"`) {
		t.Errorf("the radarr file should hold a JSON record, got %q", written)
	}
	if !strings.Contains(stdout.String(), `"msg":"allowed"`) {
		t.Errorf("stdout should also get it as JSON, got %q", stdout.String())
	}
}
