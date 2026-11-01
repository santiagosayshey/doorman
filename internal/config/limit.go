package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Unlimited is the Count of a limit written as "none".
const Unlimited = -1

// Limit is a rate: Count requests per Per.
//
// Count is either more than 0 (that many requests per Per) or Unlimited
// ("none"). It is 0 only when no limit was written, which validation allows
// on keys (no cap of their own) and rejects on rules.
type Limit struct {
	Count int
	Per   time.Duration // zero when Count is Unlimited

	// written records that the file had a limit field here, even one that
	// failed to parse. It keeps "missing" apart from "invalid", so a bad
	// limit is reported once, not again as "required".
	written bool
}

// Written reports whether the file set this limit.
func (l Limit) Written() bool { return l.written }

// IsUnlimited reports whether the limit was written as "none".
func (l Limit) IsUnlimited() bool { return l.Count == Unlimited }

var limitUnits = map[string]time.Duration{
	"s": time.Second,
	"m": time.Minute,
	"h": time.Hour,
	"d": 24 * time.Hour,
}

// ParseLimit reads "N/s", "N/m", "N/h", "N/d" or "none". N is a positive
// whole number.
func ParseLimit(s string) (Limit, error) {
	if s == "none" {
		return Limit{Count: Unlimited}, nil
	}
	count, unit, ok := strings.Cut(s, "/")
	per, known := limitUnits[unit]
	if !ok || !known {
		return Limit{}, fmt.Errorf("%q is not N/s, N/m, N/h, N/d or none", s)
	}
	n, err := strconv.Atoi(count)
	if err != nil || n <= 0 {
		return Limit{}, fmt.Errorf("%q needs a positive whole number before the /", s)
	}
	return Limit{Count: n, Per: per}, nil
}

// UnmarshalYAML decodes "30/m" or "none" straight into a Limit, so the rest
// of Doorman never parses limit strings.
//
// It returns a *yaml.TypeError rather than a plain error: the decoder adds a
// TypeError to its list and keeps going, while any other error stops it, and
// one bad limit shouldn't hide the rest of the file's problems.
func (l *Limit) UnmarshalYAML(node *yaml.Node) error {
	l.written = true
	if node.Kind != yaml.ScalarNode {
		return &yaml.TypeError{Errors: []string{
			fmt.Sprintf("line %d: limit must be a value like 30/m or none", node.Line),
		}}
	}
	parsed, err := ParseLimit(node.Value)
	if err != nil {
		return &yaml.TypeError{Errors: []string{
			fmt.Sprintf("line %d: limit %v", node.Line, err),
		}}
	}
	parsed.written = true
	*l = parsed
	return nil
}
