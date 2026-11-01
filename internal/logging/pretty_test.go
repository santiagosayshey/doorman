package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func prettyLogger(color bool) (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(NewPretty(&buf, slog.LevelDebug, color, 0)), &buf
}

func TestPrettyRequestColumns(t *testing.T) {
	log, buf := prettyLogger(false)
	req := log.With("destination", "radarr", "key", "tester", "event", "request")

	req.Info("allowed", "method", "GET", "path", "/api/v3/system/status", "status", 200, "ms", 27)
	req.Warn("denied", "method", "DELETE", "path", "/api/v3/movie/1", "reason", "no rule")
	req.Warn("rate limited", "method", "GET", "path", "/api/v3/system/status", "retry_after", 20)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	wants := []string{
		"│ INFO  │ radarr/tester │ GET    /api/v3/system/status            200 · 27ms",
		"│ WARN  │ radarr/tester │ DELETE /api/v3/movie/1                  denied · no rule",
		"│ WARN  │ radarr/tester │ GET    /api/v3/system/status            rate limited · retry in 20s",
	}
	for i, want := range wants {
		if !strings.HasSuffix(lines[i], want) {
			t.Errorf("line %d:\n got %q\nwant …%q", i+1, lines[i], want)
		}
	}
}

func TestPrettyGenericRecord(t *testing.T) {
	log, buf := prettyLogger(false)
	log.Info("doorman started", "listen", "127.0.0.1:8800", "keys", 1)

	if !strings.HasSuffix(
		strings.TrimSpace(buf.String()),
		"│ INFO  │ doorman started  listen=127.0.0.1:8800  keys=1",
	) {
		t.Errorf("got %q", buf.String())
	}
}

func TestPrettyCutsLongPaths(t *testing.T) {
	log, buf := prettyLogger(false)
	log.Info(
		"allowed",
		"event",
		"request",
		"method",
		"GET",
		"path",
		"/api/v3/"+strings.Repeat("x", 60),
		"status",
		200,
		"ms",
		1,
	)

	if !strings.Contains(buf.String(), "x… 200") {
		t.Errorf("a long path should be cut with …, got %q", buf.String())
	}
}

func TestPrettyColorsOnlyWhenAsked(t *testing.T) {
	plain, plainBuf := prettyLogger(false)
	plain.Warn("denied", "event", "request", "reason", "no rule")
	if strings.Contains(plainBuf.String(), "\x1b[") {
		t.Errorf("no color codes expected, got %q", plainBuf.String())
	}

	colored, colorBuf := prettyLogger(true)
	colored.Warn("denied", "event", "request", "reason", "no rule")
	if !strings.Contains(colorBuf.String(), "\x1b[33mWARN") {
		t.Errorf("WARN should be yellow, got %q", colorBuf.String())
	}
}

func TestUseColorIsOffForBuffersAndNoColor(t *testing.T) {
	if UseColor(&bytes.Buffer{}) {
		t.Error("a buffer is not a terminal")
	}
	t.Setenv("NO_COLOR", "1")
	if UseColor(nil) {
		t.Error("NO_COLOR should turn colors off")
	}
}

func TestPrettyPadsTheWhoColumn(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewPretty(&buf, slog.LevelInfo, false, len("radarr/homepage")))
	for _, who := range [][2]string{{"radarr", "homepage"}, {"radarr", "tv"}} {
		log.Info("allowed", "event", "request", "destination", who[0], "key", who[1],
			"method", "GET", "path", "/api", "status", 200, "ms", 1)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if strings.Index(lines[0], "GET") != strings.Index(lines[1], "GET") {
		t.Errorf("the method column should line up:\n%s", buf.String())
	}
}
