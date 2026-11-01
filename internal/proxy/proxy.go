// Package proxy is Doorman's request handler. For each request it finds the
// client's key, checks the request against that key's allowlist and limits,
// and forwards it to the key's destination with the destination's own
// credentials. Everything it doesn't explicitly allow is refused.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
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
	timeout time.Duration // how long to wait for the start of a response
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
		forward := newForwarder(target, dest.Headers, cfg.ClientAuth, dest.Timeout.Connect.Std())

		for keyName, k := range dest.Keys {
			compiled := &key{
				limiter: newLimiter(k.Limit),
				forward: forward,
				log:     destLog.With("key", keyName),
			}
			for _, r := range k.Allow {
				timeout := dest.Timeout.Response.Std()
				if r.Timeout != 0 {
					timeout = r.Timeout.Std()
				}
				compiled.rules = append(compiled.rules, limitedRule{
					rule:    compileRule(r),
					limiter: newLimiter(r.Limit),
					timeout: timeout,
				})
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
	connect time.Duration,
) *httputil.ReverseProxy {
	transport, _ := http.DefaultTransport.(*http.Transport)
	transport = transport.Clone()
	transport.DialContext = (&net.Dialer{Timeout: connect, KeepAlive: 30 * time.Second}).DialContext

	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			stripAuth(pr.Out, sources)
			for name, value := range headers {
				pr.Out.Header.Set(name, value)
			}
		},
		// The response has started: from here the body may take as long as it
		// needs, so a slow download or a stream isn't cut off.
		ModifyResponse: func(resp *http.Response) error {
			if o, ok := resp.Request.Context().Value(outcomeKey{}).(*outcome); ok {
				o.headersArrived()
			}
			return nil
		},
		// Record the failure on the request instead of logging it here, so
		// ServeHTTP logs one line per request, with the key.
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if o, ok := r.Context().Value(outcomeKey{}).(*outcome); ok {
				switch cause := context.Cause(r.Context()); {
				case errors.Is(cause, errNoResponse):
					o.err = fmt.Errorf("no response within %s", o.timeout)
				case errors.Is(cause, context.Canceled):
					o.clientGone = true
				default:
					o.err = err
				}
			}
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}

// errNoResponse is why a request is cancelled when the destination hasn't
// started answering within the rule's timeout.
var errNoResponse = errors.New("no response in time")

// outcome carries what happened to a forwarded request back to ServeHTTP.
// The ReverseProxy hands its hooks a request derived from ours, so a pointer
// in the context is how results get back.
type outcome struct {
	err        error
	clientGone bool
	timeout    time.Duration

	mu      sync.Mutex
	started bool // the response headers arrived
	cancel  context.CancelCauseFunc
}

type outcomeKey struct{}

// headersArrived stops the timeout: only the wait for the first byte of a
// response is limited.
func (o *outcome) headersArrived() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started = true
}

// timeUp cancels the request unless the response has already started. The
// lock makes the check and the cancel one step, so a response that arrives
// at the last moment is never cut off.
func (o *outcome) timeUp() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.started {
		o.cancel(errNoResponse)
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

	// 5. Forward.
	k.forwardAndLog(w, r, matched.timeout, log, start)
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

// forwardAndLog sends an allowed request to the destination, cancelling it
// if the response hasn't started within timeout, and logs the outcome once
// the response is done.
func (k *key) forwardAndLog(
	w http.ResponseWriter,
	r *http.Request,
	timeout time.Duration,
	log *slog.Logger,
	start time.Time,
) {
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	result := &outcome{timeout: timeout, cancel: cancel}
	timer := time.AfterFunc(timeout, result.timeUp)
	defer timer.Stop()

	r = r.WithContext(context.WithValue(ctx, outcomeKey{}, result))
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	k.forward.ServeHTTP(rec, r)

	ms := time.Since(start).Milliseconds()
	switch {
	case result.clientGone:
		log.Info("client disconnected", "ms", ms)
	case result.err != nil:
		log.Warn("destination unreachable", "status", rec.status, "ms", ms, "error", result.err)
	default:
		log.Info("allowed", "status", rec.status, "ms", ms)
	}
}
