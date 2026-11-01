// Package config loads and validates a Doorman config file.
//
// Loading never stops at the first problem it can recover from: every issue in
// the file is collected and returned together as Problems, so one run of
// `doorman verify` shows everything that needs fixing.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Config is a whole Doorman config file.
type Config struct {
	Version      int                    `yaml:"version"`
	Logging      Logging                `yaml:"logging"`
	ClientAuth   []AuthSource           `yaml:"client_auth"`
	Destinations map[string]Destination `yaml:"destinations"`
}

// AuthSource is one place to look for the client's key: a header or a query
// parameter. Exactly one of the two is set.
type AuthSource struct {
	Header string `yaml:"header"`
	Query  string `yaml:"query"`
}

// Destination is a service Doorman forwards to, and the keys allowed to reach it.
type Destination struct {
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
	// Log is optional: nil means this destination logs to stdout only.
	Log  *DestinationLog `yaml:"log"`
	Keys map[string]Key  `yaml:"keys"`
}

// Key is one client's grant on its destination: a secret and what it may do.
type Key struct {
	Secret string `yaml:"secret"`
	// Limit caps all of the key's rules together. Optional: a zero Count
	// means the key has no cap of its own.
	Limit Limit  `yaml:"limit"`
	Allow []Rule `yaml:"allow"`
}

// Rule allows one method on one path, at a rate.
type Rule struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	// Limit is required: "N/s", "N/m", "N/h", "N/d" or "none".
	Limit Limit `yaml:"limit"`
}

// DefaultClientAuth is used when the file has no client_auth section.
var DefaultClientAuth = []AuthSource{{Header: "X-Api-Key"}}

// Problems is every issue found in a config file, returned as one error.
type Problems []string

func (p Problems) Error() string {
	return strings.Join(p, "\n")
}

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, os.LookupEnv)
}

// Parse decodes and validates a config. lookupEnv resolves ${VAR} references;
// pass os.LookupEnv outside of tests. The error, when not nil, is Problems.
func Parse(data []byte, lookupEnv func(string) (string, bool)) (*Config, error) {
	var cfg Config

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a misspelled field is an error, not silently ignored

	var problems Problems

	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, Problems{"the file is empty"}
		}
		// Type and unknown-field errors arrive together, and the decoder still
		// fills in everything else, so keep them and validate the rest too.
		var typeErr *yaml.TypeError
		if !errors.As(err, &typeErr) {
			// A syntax error stops the parser, so it can only be reported alone.
			return nil, Problems{fmt.Sprintf("yaml: %v", err)}
		}
		problems = append(problems, typeErr.Errors...)
	}

	if cfg.ClientAuth == nil {
		cfg.ClientAuth = DefaultClientAuth
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = FormatText
	}

	validateTop(&cfg, &problems)
	unset := expandEnv(&cfg, lookupEnv, &problems)
	validateDestinations(&cfg, unset, &problems)

	if len(problems) > 0 {
		return nil, problems
	}
	return &cfg, nil
}
