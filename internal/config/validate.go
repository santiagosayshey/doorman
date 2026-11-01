package config

import (
	"fmt"
	"maps"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Methods a rule may allow. Upper case only, so a file means exactly one thing.
var validMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

var (
	envRef      = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	placeholder = regexp.MustCompile(`^\{[A-Za-z0-9_]+\}$`)
)

// sorted returns a map's keys in order, so problems are listed the same way
// on every run.
func sorted[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// expandEnv replaces ${VAR} in the fields that allow it: destination URLs,
// destination header values and key secrets. It runs after decoding, so a
// value can never change the YAML's structure. Unset variables are problems,
// never empty strings. It returns the fields that had one, so validation
// doesn't report them a second time.
func expandEnv(
	cfg *Config,
	lookupEnv func(string) (string, bool),
	problems *Problems,
) map[string]bool {
	unset := map[string]bool{}
	expand := func(field, value string) string {
		return envRef.ReplaceAllStringFunc(value, func(ref string) string {
			name := envRef.FindStringSubmatch(ref)[1]
			v, ok := lookupEnv(name)
			if !ok {
				*problems = append(*problems, fmt.Sprintf("%s: ${%s} is not set", field, name))
				unset[field] = true
			}
			return v
		})
	}

	for _, destName := range sorted(cfg.Destinations) {
		dest := cfg.Destinations[destName]
		at := "destinations." + destName

		dest.URL = expand(at+".url", dest.URL)
		for _, header := range sorted(dest.Headers) {
			dest.Headers[header] = expand(at+".headers."+header, dest.Headers[header])
		}
		for _, keyName := range sorted(dest.Keys) {
			key := dest.Keys[keyName]
			key.Secret = expand(at+".keys."+keyName+".secret", key.Secret)
			dest.Keys[keyName] = key
		}
		cfg.Destinations[destName] = dest
	}
	return unset
}

// adder returns a function that appends one formatted problem.
func adder(problems *Problems) func(string, ...any) {
	return func(format string, args ...any) {
		*problems = append(*problems, fmt.Sprintf(format, args...))
	}
}

// validateTop checks the top-level fields, so they're listed first.
func validateTop(cfg *Config, problems *Problems) {
	add := adder(problems)

	if cfg.Version != 1 {
		if cfg.Version == 0 {
			add("version: required, and 1 is the only supported version")
		} else {
			add("version: %d is not supported; 1 is the only supported version", cfg.Version)
		}
	}

	if _, port, err := net.SplitHostPort(cfg.Listen); err != nil || port == "" {
		add("listen: %q is not host:port or :port", cfg.Listen)
	}

	if cfg.Logging.Format != FormatText && cfg.Logging.Format != FormatJSON {
		add("logging.format: %q is not text or json", cfg.Logging.Format)
	}

	if len(cfg.ClientAuth) == 0 {
		add(
			"client_auth: must list at least one place to read the key from, or be left out for the default",
		)
	}
	for i, source := range cfg.ClientAuth {
		at := fmt.Sprintf("client_auth[%d]", i)
		switch {
		case source.Header != "" && source.Query != "":
			add("%s: set either header or query, not both", at)
		case source.Header == "" && source.Query == "":
			add("%s: set header or query", at)
		}
	}

	if len(cfg.Destinations) == 0 {
		add("destinations: at least one destination is required")
	}
}

// validateDestinations appends every problem in the destinations and their
// keys instead of stopping at the first one. Fields in unset already have a
// problem from expandEnv and aren't reported again.
func validateDestinations(cfg *Config, unset map[string]bool, problems *Problems) {
	add := adder(problems)

	// secret -> "destination/key" that first used it, to catch duplicates.
	owners := map[string]string{}
	// log path -> destination that first used it; two writers on one file
	// would interleave and corrupt each other's lines.
	logPaths := map[string]string{}

	for _, destName := range sorted(cfg.Destinations) {
		dest := cfg.Destinations[destName]
		at := "destinations." + destName

		if !unset[at+".url"] {
			validateURL(at+".url", dest.URL, add)
		}

		if dest.Log != nil {
			switch owner, taken := logPaths[dest.Log.Path]; {
			case dest.Log.Path == "":
				add("%s.log.path: required", at)
			case taken:
				add("%s.log.path: same file as %s; every destination needs its own", at, owner)
			default:
				logPaths[dest.Log.Path] = destName
			}
		}

		for _, header := range sorted(dest.Headers) {
			if strings.TrimSpace(header) == "" {
				add("%s.headers: a header name is empty", at)
			}
		}

		for _, keyName := range sorted(dest.Keys) {
			key := dest.Keys[keyName]
			keyAt := at + ".keys." + keyName
			id := destName + "/" + keyName

			// Never print a secret: name the key that owns it instead.
			if unset[keyAt+".secret"] {
				// already reported as an unset variable
			} else if key.Secret == "" {
				add("%s.secret: required", keyAt)
			} else if owner, taken := owners[key.Secret]; taken {
				add("%s.secret: same secret as %s; every secret must be unique", keyAt, owner)
			} else {
				owners[key.Secret] = id
			}

			if len(key.Allow) == 0 {
				add(
					"%s.allow: at least one rule is required; a key that allows nothing is a mistake",
					keyAt,
				)
			}
			for i, rule := range key.Allow {
				validateRule(fmt.Sprintf("%s.allow[%d]", keyAt, i), rule, add)
			}
		}
	}
}

func validateURL(at, raw string, add func(string, ...any)) {
	if raw == "" {
		add("%s: required", at)
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		add("%s: %v", at, err)
		return
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		add("%s: must start with http:// or https:// (got %q)", at, raw)
		return
	}
	if u.Host == "" {
		add("%s: missing host (got %q)", at, raw)
	}
}

func validateRule(at string, rule Rule, add func(string, ...any)) {
	switch {
	case rule.Method == "":
		add("%s.method: required", at)
	case !validMethods[rule.Method]:
		add(
			"%s.method: %q is not one of GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS (upper case)",
			at,
			rule.Method,
		)
	}

	validateRulePath(at+".path", rule.Path, add)

	// A limit that failed to parse is already a decode problem; only a missing
	// one is reported here.
	if !rule.Limit.Written() {
		add("%s.limit: required; use none for no limit", at)
	}
}

// validateRulePath accepts clean absolute paths whose {name} placeholders are
// whole segments, such as /api/v3/movie/{id}.
func validateRulePath(at, p string, add func(string, ...any)) {
	if p == "" {
		add("%s: required", at)
		return
	}
	if !strings.HasPrefix(p, "/") {
		add("%s: must start with / (got %q)", at, p)
		return
	}
	// Reject anything a request could only reach after normalization:
	// "//", "/./", "/../" and trailing slashes.
	if path.Clean(p) != p {
		add("%s: must be a clean path (got %q, expected %q)", at, p, path.Clean(p))
		return
	}
	for _, segment := range strings.Split(p, "/")[1:] {
		hasBrace := strings.ContainsAny(segment, "{}")
		switch {
		case hasBrace && !placeholder.MatchString(segment):
			add("%s: %q must be a whole segment like {id}", at, segment)
		case strings.Contains(segment, "*"):
			add("%s: * is not supported; use {name} to match one segment", at)
		}
	}
}
