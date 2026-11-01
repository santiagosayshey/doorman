// Package proxy is Doorman's request handler. For each request it finds the
// client's key, checks the request against that key's allowlist and limits,
// and forwards it to the key's destination with the destination's own
// credentials. Everything it doesn't explicitly allow is refused.
package proxy

import (
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"golang.org/x/time/rate"

	"github.com/santiagosayshey/doorman/internal/config"
	"github.com/santiagosayshey/doorman/internal/logging"
)

// Handler is an http.Handler built from a config.
type Handler struct {
	sources []config.AuthSource
	keys    map[secretHash]*key
}

// key is a config key ready to serve: its rules compiled, its limits made
// into limiters, and its destination's forwarder attached.
type key struct {
	rules   []limitedRule
	limiter *rate.Limiter // the key's own cap; nil when it has none
	forward *httputil.ReverseProxy
	log     *slog.Logger // carries destination and key on every record
}

type limitedRule struct {
	rule
	limiter *rate.Limiter // nil for "none"
}

// New builds the handler. cfg must already be validated.
func New(cfg *config.Config, log *slog.Logger) (*Handler, error) {
	h := &Handler{sources: cfg.ClientAuth, keys: map[secretHash]*key{}}

	for destName, dest := range cfg.Destinations {
		target, err := url.Parse(dest.URL)
		if err != nil {
			return nil, fmt.Errorf("destination %s: %w", destName, err)
		}
		destLog := log.With(logging.DestinationKey, destName)
		forward := newForwarder(target, dest.Headers, cfg.ClientAuth, destLog)

		for keyName, k := range dest.Keys {
			compiled := &key{
				limiter: newLimiter(k.Limit),
				forward: forward,
				log:     destLog.With("key", keyName),
			}
			for _, r := range k.Allow {
				compiled.rules = append(
					compiled.rules,
					limitedRule{rule: compileRule(r), limiter: newLimiter(r.Limit)},
				)
			}
			h.keys[hashSecret(k.Secret)] = compiled
		}
	}
	return h, nil
}

// newForwarder sends allowed requests to target unchanged, except that the
// client's credentials are removed and the destination's headers are set.
func newForwarder(
	target *url.URL,
	headers map[string]string,
	sources []config.AuthSource,
	log *slog.Logger,
) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			stripAuth(pr.Out, sources)
			for name, value := range headers {
				pr.Out.Header.Set(name, value)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn(
				"destination unreachable",
				"event",
				"request",
				"method",
				r.Method,
				"path",
				r.URL.EscapedPath(),
				"error",
				err,
			)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// 1. Who is this? Missing, conflicting and unknown keys all get the same
	// answer, so a caller can't probe which keys exist.
	secret, err := clientKey(r, h.sources)
	if err != nil {
		refuse(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	k, ok := h.keys[hashSecret(secret)]
	if !ok {
		refuse(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// The path is logged as the client sent it, still encoded, so a %2F shows
	// up as %2F. The query string is never logged: it can hold secrets and
	// personal data.
	log := k.log.With("event", "request", "method", r.Method, "path", r.URL.EscapedPath())

	// 2. Is the path unambiguous?
	p, err := matchPath(r.URL)
	if err != nil {
		log.Warn("refused", "reason", "unclear path")
		refuse(w, http.StatusBadRequest, "unclear path")
		return
	}

	// 3. Does a rule allow it? Deny by default.
	var matched *limitedRule
	for i := range k.rules {
		if k.rules[i].matches(r.Method, p) {
			matched = &k.rules[i]
			break
		}
	}
	if matched == nil {
		log.Warn("denied", "reason", "no rule")
		refuse(w, http.StatusForbidden, "forbidden")
		return
	}
	log = log.With("rule", matched.method+" "+matched.source)

	// 4. Within its limits?
	if ok, wait := allow(start, matched.limiter, k.limiter); !ok {
		seconds := int(math.Ceil(wait.Seconds()))
		log.Warn("rate limited", "retry_after", seconds)
		w.Header().Set("Retry-After", fmt.Sprint(seconds))
		refuse(w, http.StatusTooManyRequests, "rate limited")
		return
	}

	// 5. Forward, and log the outcome once the response is done.
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	k.forward.ServeHTTP(rec, r)
	log.Info("allowed", "status", rec.status, "ms", time.Since(start).Milliseconds())
}

func refuse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "{\"error\":%q}\n", message)
}

// statusRecorder remembers the status code a response was sent with.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real ResponseWriter. The
// ReverseProxy uses it to flush streamed responses and to hand over the
// connection for WebSocket upgrades, which a plain wrapper would hide.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
