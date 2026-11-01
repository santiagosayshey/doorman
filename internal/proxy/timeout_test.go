package proxy

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/santiagosayshey/doorman/internal/config"
)

// setupTimeouts puts Doorman in front of upstream with the given destination
// response timeout and, if not empty, a rule timeout on GET /slow.
func setupTimeouts(t *testing.T, upstream, response, rule string) (*Handler, *bytes.Buffer) {
	t.Helper()
	ruleTimeout := ""
	if rule != "" {
		ruleTimeout = "\n            timeout: " + rule
	}
	yaml := `
version: 1
destinations:
  radarr:
    url: ` + upstream + `
    timeout:
      response: ` + response + `
    keys:
      tester:
        secret: test-secret
        allow:
          - method: GET
            path: /slow
            limit: none` + ruleTimeout + `
`
	cfg, err := config.Parse([]byte(yaml), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h, err := New(cfg, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return h, &logs
}

// slowServer waits before sending anything at all.
func slowServer(t *testing.T, wait time.Duration) string {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(wait):
			_, _ = w.Write([]byte("done"))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(s.Close)
	return s.URL
}

func TestResponseTimeout(t *testing.T) {
	h, logs := setupTimeouts(t, slowServer(t, time.Second), "100ms", "")

	began := time.Now()
	w := send(h, http.MethodGet, "/slow", "test-secret")
	took := time.Since(began)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
	if took > 500*time.Millisecond {
		t.Errorf("took %v; the 100ms timeout should have cut it short", took)
	}
	if !strings.Contains(logs.String(), "no response within 100ms") {
		t.Errorf("the log should say why:\n%s", logs)
	}
}

func TestRuleTimeoutOverridesTheDestination(t *testing.T) {
	h, _ := setupTimeouts(t, slowServer(t, 300*time.Millisecond), "100ms", "2s")

	if w := send(h, http.MethodGet, "/slow", "test-secret"); w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 with the rule's longer timeout", w.Code)
	}
}

// The timeout only covers the wait for the response to start. A body that
// keeps arriving after that is never cut off.
func TestTimeoutDoesNotCutOffASlowBody(t *testing.T) {
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush() // headers arrive at once
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("the rest of the body"))
	}))
	t.Cleanup(stream.Close)

	h, _ := setupTimeouts(t, stream.URL, "100ms", "")
	w := send(h, http.MethodGet, "/slow", "test-secret")

	if w.Code != http.StatusOK || w.Body.String() != "the rest of the body" {
		t.Errorf("got %d %q, want the whole body", w.Code, w.Body.String())
	}
}

func TestClientDisconnectIsNotAnUnreachableDestination(t *testing.T) {
	h, logs := setupTimeouts(t, slowServer(t, time.Second), "5s", "")

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/slow", nil)
	r.Header.Set("X-Api-Key", "test-secret")
	time.AfterFunc(50*time.Millisecond, cancel) // the client gives up
	h.ServeHTTP(httptest.NewRecorder(), r)

	out := logs.String()
	if !strings.Contains(out, `msg="client disconnected"`) || strings.Contains(out, "unreachable") {
		t.Errorf("a client giving up should be logged as such:\n%s", out)
	}
}
