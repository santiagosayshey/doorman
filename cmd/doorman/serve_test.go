package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santiagosayshey/doorman/internal/config"
	"github.com/santiagosayshey/doorman/internal/proxy"
)

// configAllowing returns a config whose key may GET one path on upstream.
func configAllowing(upstream, path, extra string) string {
	return `
version: 1
` + extra + `
destinations:
  radarr:
    url: ` + upstream + `
    keys:
      tester:
        secret: test-secret
        allow:
          - method: GET
            path: ` + path + `
            limit: none
`
}

// newApp writes the config to a temp file and builds the app around it.
func newApp(t *testing.T, yaml string) (*app, string, *bytes.Buffer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doorman.yaml")
	writeFile(t, path, yaml)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	h, err := proxy.New(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{path: path, log: log, cfg: cfg}
	a.handler.Store(h)
	return a, path, &logs
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func get(h http.Handler, path string) int {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("X-Api-Key", "test-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func okUpstream(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(s.Close)
	return s
}

func TestReloadSwitchesToTheNewRules(t *testing.T) {
	up := okUpstream(t)
	a, path, logs := newApp(t, configAllowing(up.URL, "/old", ""))

	writeFile(t, path, configAllowing(up.URL, "/new", ""))
	a.reload()

	if got := get(a, "/new"); got != http.StatusOK {
		t.Errorf("/new = %d after reload, want 200", got)
	}
	if got := get(a, "/old"); got != http.StatusForbidden {
		t.Errorf("/old = %d after reload, want 403", got)
	}
	if !strings.Contains(logs.String(), "config reloaded") {
		t.Errorf("the reload should be logged:\n%s", logs)
	}
}

func TestInvalidReloadKeepsTheCurrentConfig(t *testing.T) {
	up := okUpstream(t)
	a, path, logs := newApp(t, configAllowing(up.URL, "/old", ""))

	writeFile(t, path, "version: 2\n")
	a.reload()

	if got := get(a, "/old"); got != http.StatusOK {
		t.Errorf("/old = %d after a failed reload, want the old config still serving", got)
	}
	for _, want := range []string{"reload failed, keeping the current config", "2 is not supported"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs are missing %q:\n%s", want, logs)
		}
	}
}

func TestReloadWarnsWhenRestartIsNeeded(t *testing.T) {
	up := okUpstream(t)
	a, path, logs := newApp(t, configAllowing(up.URL, "/a", "listen: :8800"))

	writeFile(t, path, configAllowing(up.URL, "/a", "listen: :9000\nlogging:\n  level: debug"))
	a.reload()

	for _, want := range []string{"listen changed; restart to use it", "logging settings changed; restart to use them"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs are missing %q:\n%s", want, logs)
		}
	}
}

func TestSIGHUPReloadsWhileServing(t *testing.T) {
	up := okUpstream(t)
	a, path, logs := newApp(t, configAllowing(up.URL, "/old", ""))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	reload := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- a.serve(ctx, ln, reload) }()

	writeFile(t, path, configAllowing(up.URL, "/new", ""))
	reload <- struct{}{} // what main sends on SIGHUP or a saved file

	// Poll the handler, not the logs: the server goroutine is still writing
	// to the log buffer, and reading it here at the same time is a data race.
	deadline := time.Now().Add(2 * time.Second)
	for get(a, "/new") != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("/new never became allowed after SIGHUP")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("serve returned %v, want nil after shutdown", err)
	}
	if !strings.Contains(logs.String(), "config reloaded") { // safe: serve has returned
		t.Errorf("the reload should be logged:\n%s", logs)
	}
}

func TestShutdownLetsInFlightRequestsFinish(t *testing.T) {
	// The destination takes 300ms to answer.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("done"))
	}))
	t.Cleanup(slow.Close)

	a, _, _ := newApp(t, configAllowing(slow.URL, "/slow", ""))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.serve(ctx, ln, nil) }()

	result := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+"/slow", nil)
		req.Header.Set("X-Api-Key", "test-secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			result <- 0
			return
		}
		resp.Body.Close()
		result <- resp.StatusCode
	}()

	time.Sleep(100 * time.Millisecond) // the request is now waiting on the destination
	cancel()

	if got := <-result; got != http.StatusOK {
		t.Errorf("the in-flight request got %d, want it to finish with 200", got)
	}
	if err := <-done; err != nil {
		t.Errorf("serve returned %v, want nil", err)
	}
}
