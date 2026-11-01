package proxy

import (
	"errors"
	"net/url"
	"path"
	"strings"

	"github.com/santiagosayshey/doorman/internal/config"
)

// errUnclearPath means a request path could be read more than one way, so
// Doorman refuses it rather than guessing what the destination would see.
var errUnclearPath = errors.New("unclear path")

// matchPath returns the path to match rules against. It refuses paths that
// normalization would change ("//", "/./", "/../") and encoded slashes or
// backslashes, which Doorman and the destination might read differently. One
// trailing slash is tolerated: /api/v3/movie/ matches /api/v3/movie.
func matchPath(u *url.URL) (string, error) {
	raw := u.EscapedPath()
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") ||
		strings.Contains(u.Path, `\`) {
		return "", errUnclearPath
	}

	p := u.Path
	if !strings.HasPrefix(p, "/") {
		return "", errUnclearPath
	}
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	if path.Clean(p) != p {
		return "", errUnclearPath
	}
	return p, nil
}

// rule is a config rule split into segments, ready to match requests.
type rule struct {
	method   string
	segments []segment
	source   string // the rule's path as written, for logs
}

type segment struct {
	literal     string
	placeholder bool // {name}: any one non-empty segment
}

func compileRule(r config.Rule) rule {
	parts := strings.Split(r.Path, "/")[1:]
	if r.Path == "/" {
		parts = nil
	}
	segments := make([]segment, len(parts))
	for i, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			segments[i] = segment{placeholder: true}
		} else {
			segments[i] = segment{literal: part}
		}
	}
	return rule{method: r.Method, segments: segments, source: r.Path}
}

// matches reports whether a request with this method and matchPath result
// is allowed by the rule. Literal segments compare case-sensitively.
func (r rule) matches(method, p string) bool {
	if method != r.method {
		return false
	}
	parts := strings.Split(p, "/")[1:]
	if p == "/" {
		parts = nil
	}
	if len(parts) != len(r.segments) {
		return false
	}
	for i, seg := range r.segments {
		switch {
		case seg.placeholder:
			if parts[i] == "" {
				return false
			}
		case parts[i] != seg.literal:
			return false
		}
	}
	return true
}
