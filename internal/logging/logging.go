// Package logging turns the config's logging settings into a slog logger.
//
// Every record goes to stdout at logging.level. A record that belongs to a
// destination (it carries a "destination" attribute) also goes to that
// destination's file, at the destination's own level. The two levels are
// independent: a destination can log at debug while stdout stays at info.
package logging

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"

	"github.com/santiagosayshey/doorman/internal/config"
)

// DestinationKey is the attribute that ties a record to a destination.
const DestinationKey = "destination"

// Setup builds the logger described by cfg. It writes stdout records to
// stdout, opens one file per destination with a log section, and returns a
// close function for those files.
func Setup(cfg *config.Config, stdout io.Writer) (*slog.Logger, func() error, error) {
	files := map[string]*os.File{}
	closeAll := func() error {
		var errs []error
		for _, f := range files {
			errs = append(errs, f.Close())
		}
		return errors.Join(errs...)
	}

	destinations := map[string]slog.Handler{}
	for name, dest := range cfg.Destinations {
		if dest.Log == nil {
			continue
		}
		// Append, create if missing, readable by the owner and group only.
		f, err := os.OpenFile(dest.Log.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			_ = closeAll()
			return nil, nil, err
		}
		files[name] = f
		destinations[name] = newHandler(f, cfg.Logging.Format, dest.Log.Level.Slog())
	}

	router := NewRouter(newHandler(stdout, cfg.Logging.Format, cfg.Logging.Level.Slog()), destinations)
	return slog.New(router), closeAll, nil
}

func newHandler(w io.Writer, format config.Format, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	if format == config.FormatJSON {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

// Router is a slog.Handler that sends every record to stdout and records
// with a destination attribute to that destination's handler too.
type Router struct {
	stdout       slog.Handler
	destinations map[string]slog.Handler
	// destination is set when a logger was made with
	// .With("destination", name), so its records route without repeating it.
	destination string
}

// NewRouter builds a Router from a stdout handler and one handler per
// destination name. Each handler applies its own level.
func NewRouter(stdout slog.Handler, destinations map[string]slog.Handler) *Router {
	return &Router{stdout: stdout, destinations: destinations}
}

// Enabled reports whether any target could want a record at this level. slog
// calls it before building a record, so it must not be stricter than the
// most verbose target; Handle then applies each target's real level.
func (r *Router) Enabled(ctx context.Context, level slog.Level) bool {
	if r.stdout.Enabled(ctx, level) {
		return true
	}
	if r.destination != "" {
		h, ok := r.destinations[r.destination]
		return ok && h.Enabled(ctx, level)
	}
	for _, h := range r.destinations {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (r *Router) Handle(ctx context.Context, record slog.Record) error {
	var errs []error
	if r.stdout.Enabled(ctx, record.Level) {
		errs = append(errs, r.stdout.Handle(ctx, record))
	}

	// The destination comes from .With(...) or from the record itself.
	destination := r.destination
	if destination == "" {
		record.Attrs(func(a slog.Attr) bool {
			if a.Key == DestinationKey {
				destination = a.Value.String()
				return false
			}
			return true
		})
	}

	if h, ok := r.destinations[destination]; ok && h.Enabled(ctx, record.Level) {
		errs = append(errs, h.Handle(ctx, record))
	}
	return errors.Join(errs...)
}

// WithAttrs passes the attributes on to every target and remembers a
// destination attribute, so records from the derived logger route correctly.
func (r *Router) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &Router{
		stdout:       r.stdout.WithAttrs(attrs),
		destinations: make(map[string]slog.Handler, len(r.destinations)),
		destination:  r.destination,
	}
	for name, h := range r.destinations {
		next.destinations[name] = h.WithAttrs(attrs)
	}
	for _, a := range attrs {
		if a.Key == DestinationKey {
			next.destination = a.Value.String()
		}
	}
	return next
}

func (r *Router) WithGroup(name string) slog.Handler {
	next := &Router{
		stdout:       r.stdout.WithGroup(name),
		destinations: make(map[string]slog.Handler, len(r.destinations)),
		destination:  r.destination,
	}
	for dest, h := range r.destinations {
		next.destinations[dest] = h.WithGroup(name)
	}
	return next
}
