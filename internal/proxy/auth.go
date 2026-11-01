package proxy

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/santiagosayshey/doorman/internal/config"
)

var (
	errNoKey       = errors.New("no key")
	errConflicting = errors.New("conflicting keys")
)

// secretHash is how keys are looked up. A map keyed by the SHA-256 of each
// secret never compares a guess against a real secret byte by byte, so the
// time a lookup takes reveals nothing about how close a guess was.
type secretHash [sha256.Size]byte

func hashSecret(secret string) secretHash {
	return sha256.Sum256([]byte(secret))
}

// clientKey finds the client's key in the client_auth sources. A request may
// repeat its key in several sources, but every value must be the same: two
// different keys in one request is refused rather than guessed between.
func clientKey(r *http.Request, sources []config.AuthSource) (string, error) {
	var found string
	query := r.URL.Query()
	for _, source := range sources {
		var values []string
		if source.Header != "" {
			values = r.Header.Values(source.Header)
		} else {
			values = query[source.Query]
		}
		for _, v := range values {
			switch {
			case v == "":
				continue
			case found == "":
				found = v
			case v != found:
				return "", errConflicting
			}
		}
	}
	if found == "" {
		return "", errNoKey
	}
	return found, nil
}

// stripAuth removes every client_auth source from the outgoing request, so
// neither the client's key nor a second, smuggled one reaches the destination.
func stripAuth(out *http.Request, sources []config.AuthSource) {
	var params []string
	for _, source := range sources {
		if source.Header != "" {
			out.Header.Del(source.Header)
		} else {
			params = append(params, source.Query)
		}
	}
	if len(params) > 0 {
		out.URL.RawQuery = dropQueryParams(out.URL.RawQuery, params)
	}
}

// dropQueryParams removes the named parameters from a raw query and keeps
// everything else byte for byte: order, repeated keys and encoding. Parsing
// into url.Values and encoding again would sort the keys and could change
// how values are escaped.
func dropQueryParams(raw string, names []string) string {
	if raw == "" {
		return raw
	}
	var kept []string
	for _, part := range strings.Split(raw, "&") {
		name, _, _ := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(name)
		if err != nil {
			decoded = name
		}
		drop := false
		for _, n := range names {
			if decoded == n {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "&")
}
