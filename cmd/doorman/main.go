package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"

	"github.com/santiagosayshey/doorman/internal/config"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "validate" {
		os.Exit(runValidate(os.Args[2:]))
	}
	runProxy()
}

// runValidate checks a config file and lists every problem in it.
// It returns the exit code: 0 when the file is valid, 1 when it isn't.
func runValidate(args []string) int {
	flags := flag.NewFlagSet("validate", flag.ExitOnError)
	path := flags.String("c", "doorman.yaml", "config file to check")
	_ = flags.Parse(args) // ExitOnError: a bad flag exits before this returns

	cfg, err := config.Load(*path)

	var problems config.Problems
	switch {
	case errors.As(err, &problems):
		fmt.Printf("✗ %d problem(s) in %s\n", len(problems), *path)
		for _, p := range problems {
			fmt.Printf("  %s\n", p)
		}
		return 1
	case err != nil: // the file couldn't be read at all
		fmt.Printf("✗ %v\n", err)
		return 1
	}

	destinations, keys := count(cfg)
	fmt.Printf("✓ %s is valid: %s, %s\n", *path, plural(destinations, "destination"), plural(keys, "key"))
	return 0
}

// runProxy forwards everything to one Radarr. Stage 4 replaces it with the
// config-driven proxy.
func runProxy() {
	log.Println("DOORMAN STARTING!")

	radarr_url := os.Getenv("RADARR_URL")

	// envs
	if radarr_url == "" {
		log.Fatal("RADARR_URL is not set")
	}

	radarr_api_key := os.Getenv("RADARR_API_KEY")

	if radarr_api_key == "" {
		log.Fatal("RADARR_API_KEY is not set")
	}

	// parse the url
	target, err := url.Parse(radarr_url)
	if err != nil {
		log.Fatalf("RADARR_URL is invalid: %v", err)
	}

	if target.Scheme == "" || target.Host == "" {
		log.Fatal("RADARR_URL must be a full URL like http://localhost:7878")
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Header.Set("X-Api-Key", radarr_api_key)
		},
	}
	log.Fatal(http.ListenAndServe(":8800", proxy))
}
