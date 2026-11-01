package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthEndpoint(t *testing.T) {
	w := httptest.NewRecorder()
	adminHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Errorf("GET /health = %d %q, want 200 with status ok", w.Code, w.Body.String())
	}
}

func TestAdminAddress(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, yaml, want string
	}{
		{"default", "version: 1\n", "127.0.0.1:8801"},
		{"set", "admin:\n  listen: 127.0.0.1:9100\n", "127.0.0.1:9100"},
		{"all interfaces", "admin:\n  listen: :9100\n", "127.0.0.1:9100"},
		{"0.0.0.0", "admin:\n  listen: 0.0.0.0:9100\n", "127.0.0.1:9100"},
		// A config Doorman would reject still yields the admin address.
		{
			"invalid elsewhere",
			"version: 7\nadmin:\n  listen: :9100\nbogus: true\n",
			"127.0.0.1:9100",
		},
		{"broken yaml", "admin: [\n", "127.0.0.1:8801"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".yaml")
			writeFile(t, path, tc.yaml)
			if got := adminAddress(path); got != tc.want {
				t.Errorf("adminAddress = %q, want %q", got, tc.want)
			}
		})
	}
	if got := adminAddress(filepath.Join(dir, "missing.yaml")); got != "127.0.0.1:8801" {
		t.Errorf("a missing file should fall back to the default, got %q", got)
	}
}

func TestHealthCommand(t *testing.T) {
	up := okUpstream(t)
	a, path, _ := newApp(t, configAllowing(up.URL, "/a", ""))
	ln, adminLn := listeners(t)
	writeFile(t, path, configAllowing(up.URL, "/a", "admin:\n  listen: "+adminLn.Addr().String()))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.serve(ctx, ln, adminLn, nil) }()

	if code := runHealth([]string{"-c", path}); code != 0 {
		t.Errorf("health = %d while serving, want 0", code)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serve returned %v", err)
	}
	if code := runHealth([]string{"-c", path}); code != 1 {
		t.Errorf("health = %d after shutdown, want 1", code)
	}
}

func TestProxyKeysCannotReachHealth(t *testing.T) {
	up := okUpstream(t)
	a, _, _ := newApp(t, configAllowing(up.URL, "/health", ""))
	// Even a key allowed to GET /health reaches the destination, not Doorman.
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.Header.Set("X-Api-Key", "test-secret")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Error("the proxy port must never answer Doorman's own health check")
	}
}
