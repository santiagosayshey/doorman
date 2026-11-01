package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
)

// Pretty is a slog.Handler for people reading logs in a terminal. Request
// records (event=request) become fixed columns, like an access log:
//
//	12:35:31.514 │ INFO │ radarr/tester │ GET    /api/v3/system/status      200 · 27ms
//	12:35:33.029 │ WARN │ radarr/tester │ DELETE /api/v3/movie/1            denied · no rule
//
// Any other record is its message followed by its fields.
type Pretty struct {
	w     io.Writer
	mu    *sync.Mutex // shared by every handler derived from this one
	level slog.Leveler
	color bool
	attrs []slog.Attr // from WithAttrs
	group string      // from WithGroup, as a "name." prefix
}

// NewPretty returns a Pretty handler. With color, levels and outcomes are
// colored; leave it off for files and pipes.
func NewPretty(w io.Writer, level slog.Leveler, color bool) *Pretty {
	return &Pretty{w: w, mu: &sync.Mutex{}, level: level, color: color}
}

// UseColor reports whether w is a terminal that should get colors: not a
// file or a pipe, and NO_COLOR isn't set (https://no-color.org).
func UseColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

const (
	methodWidth = 6  // DELETE
	pathWidth   = 32 // longer paths are cut with …
)

// ANSI color codes. Only written when color is on.
const (
	reset  = "\x1b[0m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	blue   = "\x1b[34m"
)

func (h *Pretty) paint(code, s string) string {
	if !h.color {
		return s
	}
	return code + s + reset
}

// Enabled reports whether the level is at or above the handler's level.
func (h *Pretty) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

// Handle formats one record and writes it as a single line.
func (h *Pretty) Handle(_ context.Context, r slog.Record) error {
	attrs := slices.Clone(h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, h.grouped(a))
		return true
	})
	fields := map[string]string{}
	for _, a := range attrs {
		fields[a.Key] = a.Value.String()
	}

	var b bytes.Buffer
	sep := h.paint(dim, " │ ")
	b.WriteString(h.paint(dim, r.Time.Format("15:04:05.000")))
	b.WriteString(sep)
	b.WriteString(h.level5(r.Level))
	b.WriteString(sep)

	if fields["event"] == "request" {
		h.writeRequest(&b, r.Message, fields, sep)
	} else {
		h.writeGeneric(&b, r.Message, attrs)
	}
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(b.Bytes())
	return err
}

// writeRequest lays a request record out in columns: who, what, outcome.
func (h *Pretty) writeRequest(b *bytes.Buffer, msg string, f map[string]string, sep string) {
	who := f["destination"]
	if f["key"] != "" {
		who += "/" + f["key"]
	}
	b.WriteString(who)
	b.WriteString(sep)
	fmt.Fprintf(b, "%-*s %-*s ", methodWidth, f["method"], pathWidth, cut(f["path"], pathWidth))

	switch msg {
	case "allowed":
		b.WriteString(h.status(f["status"]))
		b.WriteString(h.paint(dim, " · "+f["ms"]+"ms"))
	case "rate limited":
		b.WriteString(h.paint(yellow, "rate limited"))
		b.WriteString(h.paint(dim, " · retry in "+f["retry_after"]+"s"))
	case "denied", "refused":
		b.WriteString(h.paint(yellow, msg))
		b.WriteString(h.paint(dim, " · "+f["reason"]))
	default:
		b.WriteString(h.paint(red, msg))
		if f["error"] != "" {
			b.WriteString(h.paint(dim, " · "+f["error"]))
		}
	}
}

// writeGeneric writes the message, then every field as key=value.
func (h *Pretty) writeGeneric(b *bytes.Buffer, msg string, attrs []slog.Attr) {
	b.WriteString(msg)
	for _, a := range attrs {
		value := a.Value.String()
		if strings.ContainsAny(value, " \"=") {
			value = fmt.Sprintf("%q", value)
		}
		b.WriteString("  ")
		b.WriteString(h.paint(dim, a.Key+"="))
		b.WriteString(value)
	}
}

// level5 pads the level to five characters, so the columns after it line up.
func (h *Pretty) level5(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return h.paint(red, "ERROR")
	case l >= slog.LevelWarn:
		return h.paint(yellow, "WARN ")
	case l >= slog.LevelInfo:
		return h.paint(blue, "INFO ")
	default:
		return h.paint(dim, "DEBUG")
	}
}

// status colors an HTTP status: 2xx and 3xx green, 4xx yellow, 5xx red.
func (h *Pretty) status(s string) string {
	switch {
	case strings.HasPrefix(s, "5"):
		return h.paint(red, s)
	case strings.HasPrefix(s, "4"):
		return h.paint(yellow, s)
	default:
		return h.paint(green, s)
	}
}

// cut shortens s to width runes, ending with … when it had to cut.
func cut(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:width-1]) + "…"
}

func (h *Pretty) grouped(a slog.Attr) slog.Attr {
	if h.group != "" {
		a.Key = h.group + a.Key
	}
	return a
}

// WithAttrs returns a handler that adds attrs to every record.
func (h *Pretty) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = slices.Clone(h.attrs)
	for _, a := range attrs {
		next.attrs = append(next.attrs, h.grouped(a))
	}
	return &next
}

// WithGroup returns a handler that prefixes later keys with "name.".
func (h *Pretty) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	next.group = h.group + name + "."
	return &next
}

// Compile-time check that Pretty is a slog.Handler.
var _ slog.Handler = (*Pretty)(nil)
