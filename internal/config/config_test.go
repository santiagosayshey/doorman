package config

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// valid is a complete, correct config. Cases change one thing about it.
const valid = `
version: 1
destinations:
  radarr:
    url: http://radarr:7878
    headers:
      X-Api-Key: ${RADARR_API_KEY}
    keys:
      homepage:
        secret: ${HOMEPAGE_KEY}
        limit: 100/h
        allow:
          - method: GET
            path: /api/v3/movie/{id}
            limit: 10/m
`

var env = map[string]string{
	"RADARR_API_KEY": "real-radarr-key",
	"HOMEPAGE_KEY":   "hp-secret",
	"AUTOBRR_KEY":    "ab-secret",
}

func lookup(name string) (string, bool) {
	v, ok := env[name]
	return v, ok
}

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr []string // substrings every one of which must appear; empty = valid
	}{
		{"valid config", valid, nil},

		// version
		{
			"version missing",
			strings.Replace(valid, "version: 1\n", "", 1),
			[]string{"version: required"},
		},
		{
			"version 2",
			strings.Replace(valid, "version: 1", "version: 2", 1),
			[]string{"2 is not supported"},
		},
		{
			"version quoted",
			strings.Replace(valid, "version: 1", `version: "1"`, 1),
			[]string{"cannot unmarshal"},
		},

		// unknown fields
		{
			"misspelled field",
			strings.Replace(valid, "allow:", "alow:", 1),
			[]string{"field alow not found"},
		},

		// listen
		{"listen port only", "listen: :9000\n" + valid, nil},
		{"listen host and port", "listen: 127.0.0.1:9000\n" + valid, nil},
		{
			"listen without port",
			"listen: localhost\n" + valid,
			[]string{`listen: "localhost" is not host:port`},
		},

		// logging
		{"logging json", "logging:\n  level: debug\n  format: json\n" + valid, nil},
		{
			"logging format unknown",
			"logging:\n  format: xml\n" + valid,
			[]string{`logging.format: "xml" is not text or json`},
		},
		{
			"logging level unknown",
			"logging:\n  level: verbose\n" + valid,
			[]string{`level "verbose" is not debug`},
		},
		{
			"logging level upper case",
			"logging:\n  level: INFO\n" + valid,
			[]string{`level "INFO" is not debug`},
		},
		{
			"destination log",
			strings.Replace(
				valid,
				"    keys:",
				"    log:\n      path: /logs/radarr.log\n      level: warn\n    keys:",
				1,
			),
			nil,
		},
		{
			"destination log without path",
			strings.Replace(valid, "    keys:", "    log:\n      level: warn\n    keys:", 1),
			[]string{"destinations.radarr.log.path: required"},
		},
		{
			"destination log bad level",
			strings.Replace(
				valid,
				"    keys:",
				"    log:\n      path: /l\n      level: loud\n    keys:",
				1,
			),
			[]string{`level "loud" is not`},
		},
		{
			"destination log path shared",
			strings.Replace(
				valid,
				"    keys:",
				"    log:\n      path: /logs/shared.log\n    keys:",
				1,
			) + `
  sonarr:
    url: http://sonarr:8989
    log:
      path: /logs/shared.log
    keys:
      tv:
        secret: ${AUTOBRR_KEY}
        allow:
          - method: GET
            path: /api/v3/series
            limit: none
`,
			[]string{"destinations.sonarr.log.path: same file as radarr"},
		},

		// client_auth
		{
			"client_auth header and query",
			"client_auth:\n  - header: X-Api-Key\n    query: apikey\n" + valid,
			[]string{"client_auth[0]: set either header or query, not both"},
		},
		{
			"client_auth entry empty",
			"client_auth:\n  - {}\n" + valid,
			[]string{"client_auth[0]: set header or query"},
		},
		{"client_auth empty list", "client_auth: []\n" + valid, []string{"client_auth: must list"}},

		// destinations
		{"no destinations", "version: 1\ndestinations: {}\n", []string{"at least one destination"}},
		{
			"url without scheme", strings.Replace(valid, "http://radarr:7878", "radarr:7878", 1),
			[]string{"must start with http:// or https://"},
		},
		{
			"url without host",
			strings.Replace(valid, "http://radarr:7878", "http://", 1),
			[]string{"missing host"},
		},

		// env vars
		{
			"env var not set", strings.Replace(valid, "${HOMEPAGE_KEY}", "${NOT_SET}", 1),
			[]string{"destinations.radarr.keys.homepage.secret: ${NOT_SET} is not set"},
		},

		// keys
		{
			"secret missing", strings.Replace(valid, "        secret: ${HOMEPAGE_KEY}\n", "", 1),
			[]string{"homepage.secret: required"},
		},
		{"secret reused across destinations", valid + `
  sonarr:
    url: http://sonarr:8989
    keys:
      homepage:
        secret: ${HOMEPAGE_KEY}
        allow:
          - method: GET
            path: /api/v3/series
            limit: none
`, []string{"sonarr.keys.homepage.secret: same secret as radarr/homepage"}},
		{"allow empty", strings.Replace(valid, `allow:
          - method: GET
            path: /api/v3/movie/{id}
            limit: 10/m`, "allow: []", 1), []string{"homepage.allow: at least one rule"}},

		// rules
		{
			"method unknown",
			strings.Replace(valid, "method: GET", "method: FETCH", 1),
			[]string{`"FETCH" is not one of`},
		},
		{
			"method lower case",
			strings.Replace(valid, "method: GET", "method: get", 1),
			[]string{`"get" is not one of`},
		},
		{
			"path without slash",
			strings.Replace(valid, "/api/v3/movie/{id}", "api/v3/movie", 1),
			[]string{"must start with /"},
		},
		{
			"path not clean",
			strings.Replace(valid, "/api/v3/movie/{id}", "/api/v3/../movie", 1),
			[]string{"must be a clean path"},
		},
		{
			"path trailing slash",
			strings.Replace(valid, "/api/v3/movie/{id}", "/api/v3/movie/", 1),
			[]string{"must be a clean path"},
		},
		{
			"path partial placeholder",
			strings.Replace(valid, "/api/v3/movie/{id}", "/api/v3/movie-{id}", 1),
			[]string{"must be a whole segment"},
		},
		{
			"rule limit missing",
			strings.Replace(valid, "            limit: 10/m\n", "", 1),
			[]string{"limit: required; use none"},
		},
		{"rule limit none", strings.Replace(valid, "limit: 10/m", "limit: none", 1), nil},
		{
			"rule limit zero",
			strings.Replace(valid, "limit: 10/m", "limit: 0/m", 1),
			[]string{"positive whole number"},
		},
		{
			"rule limit bad unit",
			strings.Replace(valid, "limit: 10/m", "limit: 10/min", 1),
			[]string{"is not N/s"},
		},
		{
			"rule limit decimal",
			strings.Replace(valid, "limit: 10/m", "limit: 1.5/h", 1),
			[]string{"positive whole number"},
		},
		{
			"bad limit reported once",
			strings.Replace(valid, "limit: 10/m", "limit: 30/min", 1),
			[]string{"is not N/s"},
		},
		{
			"key limit bad",
			strings.Replace(valid, "limit: 100/h", "limit: lots", 1),
			[]string{`limit "lots" is not`},
		},
		{
			"limit as a list",
			strings.Replace(valid, "limit: 10/m", "limit: [10, m]", 1),
			[]string{"limit must be a value"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml), lookup)

			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("expected a valid config, got:\n%v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got none", tc.wantErr)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error is missing %q; got:\n%v", want, err)
				}
			}
		})
	}
}

// One pass reports every problem, not just the first.
func TestParseCollectsEveryProblem(t *testing.T) {
	bad := `
version: 2
destinations:
  radarr:
    url: radarr:7878
    keys:
      homepage:
        secret: ${NOT_SET}
        allow:
          - method: FETCH
            path: api/v3/movie
            limit: 10/min
`
	_, err := Parse([]byte(bad), lookup)

	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected a *ValidationError, got %T: %v", err, err)
	}
	problems := invalid.Problems
	// version, url, unset env var, method, path, limit. The unset secret
	// isn't reported a second time as "required".
	if len(problems) != 6 {
		t.Fatalf("expected 6 problems, got %d:\n%v", len(problems), err)
	}
	// Decode problems come first, then top-level, then destinations.
	version, url := -1, -1
	for i, p := range problems {
		switch {
		case strings.HasPrefix(p, "version:"):
			version = i
		case strings.HasPrefix(p, "destinations.radarr.url"):
			url = i
		}
	}
	if version == -1 || url == -1 || version > url {
		t.Errorf("version should be listed before the destinations, got:\n%v", err)
	}
}

// A misspelled field doesn't hide the other problems in the file.
func TestParseValidatesPastDecodeErrors(t *testing.T) {
	bad := strings.Replace(valid, "version: 1", "version: 2", 1)
	bad = strings.Replace(bad, "limit: 100/h", "limt: 100/h", 1)

	_, err := Parse([]byte(bad), lookup)
	for _, want := range []string{"field limt not found", "2 is not supported"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q; got:\n%v", want, err)
		}
	}
}

func TestDefaultClientAuth(t *testing.T) {
	cfg, err := Parse([]byte(valid), lookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ClientAuth) != 1 || cfg.ClientAuth[0].Header != "X-Api-Key" {
		t.Fatalf("expected the default [header: X-Api-Key], got %+v", cfg.ClientAuth)
	}
}

func TestEnvExpansion(t *testing.T) {
	cfg, err := Parse([]byte(valid), lookup)
	if err != nil {
		t.Fatal(err)
	}
	radarr := cfg.Destinations["radarr"]
	if got := radarr.Headers["X-Api-Key"]; got != "real-radarr-key" {
		t.Errorf("header not expanded: %q", got)
	}
	if got := radarr.Keys["homepage"].Secret; got != "hp-secret" {
		t.Errorf("secret not expanded: %q", got)
	}
}

func TestParseLimit(t *testing.T) {
	cases := []struct {
		in   string
		want Limit
	}{
		{"30/s", Limit{Count: 30, Per: time.Second}},
		{"30/m", Limit{Count: 30, Per: time.Minute}},
		{"100/h", Limit{Count: 100, Per: time.Hour}},
		{"5/d", Limit{Count: 5, Per: 24 * time.Hour}},
		{"none", Limit{Count: Unlimited}},
	}
	for _, tc := range cases {
		got, err := ParseLimit(tc.in)
		if err != nil || got.Count != tc.want.Count || got.Per != tc.want.Per {
			t.Errorf("ParseLimit(%q) = %+v, %v; want %+v", tc.in, got, err, tc.want)
		}
	}

	for _, bad := range []string{"", "30", "/m", "0/m", "-1/m", "1.5/h", "30/min", "30/M", "None", "-1"} {
		if _, err := ParseLimit(bad); err == nil {
			t.Errorf("ParseLimit(%q) should fail", bad)
		}
	}
}

func TestDecodedLimits(t *testing.T) {
	cfg, err := Parse([]byte(strings.Replace(valid, "limit: 10/m", "limit: none", 1)), lookup)
	if err != nil {
		t.Fatal(err)
	}
	key := cfg.Destinations["radarr"].Keys["homepage"]
	if key.Limit.Count != 100 || key.Limit.Per != time.Hour {
		t.Errorf("key limit = %+v, want 100/h", key.Limit)
	}
	if !key.Allow[0].Limit.IsUnlimited() {
		t.Errorf("rule limit = %+v, want unlimited", key.Allow[0].Limit)
	}
}

func TestKeyLimitIsOptional(t *testing.T) {
	cfg, err := Parse([]byte(strings.Replace(valid, "        limit: 100/h\n", "", 1)), lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Destinations["radarr"].Keys["homepage"].Limit; got.Count != 0 || got.Written() {
		t.Errorf("a key without a limit should be unwritten with Count 0, got %+v", got)
	}
}

// A limit that fails to parse is one problem, not also "required".
func TestBadLimitReportedOnce(t *testing.T) {
	_, err := Parse([]byte(strings.Replace(valid, "limit: 10/m", "limit: 30/min", 1)), lookup)
	var invalid *ValidationError
	if !errors.As(err, &invalid) || len(invalid.Problems) != 1 {
		t.Fatalf("expected exactly 1 problem, got:\n%v", err)
	}
}

func TestLoggingDefaults(t *testing.T) {
	cfg, err := Parse([]byte(valid), lookup)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8800" {
		t.Errorf("listen default = %q, want :8800", cfg.Listen)
	}
	if cfg.Logging.Format != FormatText || cfg.Logging.Level.Slog() != slog.LevelInfo {
		t.Errorf("logging defaults = %+v, want text at info", cfg.Logging)
	}
	if cfg.Destinations["radarr"].Log != nil {
		t.Errorf("a destination without log should have a nil Log")
	}
}
