package config

import (
	"fmt"
	"log/slog"

	"go.yaml.in/yaml/v3"
)

// Logging configures the app log, which always goes to stdout.
type Logging struct {
	Level  Level  `yaml:"level"`
	Format Format `yaml:"format"`
}

// DestinationLog sends one destination's request records to a file of its own.
type DestinationLog struct {
	Path  string `yaml:"path"`
	Level Level  `yaml:"level"`
}

// Format is how log records are written.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Level is a log level written as debug, info, warn or error. Its zero value is
// info, so a missing level means info.
type Level slog.Level

var levelNames = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// Slog returns the level as the standard library's slog.Level.
func (l Level) Slog() slog.Level { return slog.Level(l) }

// UnmarshalYAML accepts only the four lower-case names. slog's own parser also
// takes "INFO+2" and any case; a config file should mean exactly one thing.
func (l *Level) UnmarshalYAML(node *yaml.Node) error {
	level, ok := levelNames[node.Value]
	if node.Kind != yaml.ScalarNode || !ok {
		return &yaml.TypeError{Errors: []string{
			fmt.Sprintf("line %d: level %q is not debug, info, warn or error", node.Line, node.Value),
		}}
	}
	*l = Level(level)
	return nil
}
