package config

import (
	"fmt"
	"time"

	"go.yaml.in/yaml/v3"
)

// Duration is a time written the Go way: 500ms, 5s, 2m, 1h30m. It must be
// more than zero, so a zero Duration only ever means "not written".
type Duration time.Duration

// Std returns the duration as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// UnmarshalYAML parses and checks the duration. Like Limit, it returns a
// *yaml.TypeError so one bad value doesn't hide the rest of the file's
// problems.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	parsed, err := time.ParseDuration(node.Value)
	switch {
	case node.Kind != yaml.ScalarNode || err != nil:
		return &yaml.TypeError{Errors: []string{
			fmt.Sprintf(
				"line %d: %q is not a duration like 5s, 2m or 1h30m",
				node.Line,
				node.Value,
			),
		}}
	case parsed <= 0:
		return &yaml.TypeError{Errors: []string{
			fmt.Sprintf("line %d: duration %q must be more than zero", node.Line, node.Value),
		}}
	}
	*d = Duration(parsed)
	return nil
}

// Timeout limits how long Doorman waits on a destination.
type Timeout struct {
	// Connect is how long to wait for the destination to accept a connection.
	Connect Duration `yaml:"connect"`
	// Response is how long to wait for the start of a response. Once headers
	// arrive, the body may take as long as it needs, so streams aren't cut off.
	Response Duration `yaml:"response"`
}

// Defaults used when a destination leaves a timeout out.
const (
	DefaultConnectTimeout  = 5 * time.Second
	DefaultResponseTimeout = 30 * time.Second
)
