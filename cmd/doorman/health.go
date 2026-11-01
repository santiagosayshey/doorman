package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/santiagosayshey/doorman/internal/config"
)

// adminHandler serves Doorman's own endpoints on the admin port.
func adminHandler() http.Handler {
	mux := http.NewServeMux()
	// Liveness only: the process is up and serving. Whether a destination is
	// reachable is a different question, for the dashboard or a monitor
	// pointed at the destination itself.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": version})
	})
	return mux
}

// runHealth asks the running Doorman's admin port whether it's up. It
// exits 0 when it is and 1 when it isn't, for Docker's HEALTHCHECK: the
// image has no curl, so Doorman checks itself.
func runHealth(args []string) int {
	flags := flag.NewFlagSet("health", flag.ExitOnError)
	path := flags.String("c", "doorman.yaml", "config file, read for admin.listen only")
	_ = flags.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	url := "http://" + adminAddress(*path) + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Printf("✗ %v\n", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("✗ %v\n", err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("✗ %s answered %d\n", url, resp.StatusCode)
		return 1
	}
	fmt.Println("✓ healthy")
	return 0
}

// adminAddress reads only admin.listen from the config file, with no
// validation and no env vars. A broken edit that Doorman refused to reload
// must not fail the health check while the last good config keeps serving,
// so anything unreadable falls back to the default. A listen address that
// covers every interface is reached on localhost.
func adminAddress(path string) string {
	listen := config.DefaultAdminListen
	var partial struct {
		Admin struct {
			Listen string `yaml:"listen"`
		} `yaml:"admin"`
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own -c flag
	if err == nil && yaml.Unmarshal(data, &partial) == nil && partial.Admin.Listen != "" {
		listen = partial.Admin.Listen
	}

	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
