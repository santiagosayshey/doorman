package proxy

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/santiagosayshey/doorman/internal/config"
)

// upstream is a fake destination that records the last request it got.
type upstream struct {
	server *httptest.Server
	last   *http.Request
	calls  int
}

func newUpstream(t *testing.T) *upstream {
	u := &upstream{}
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.last = r
		u.calls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(u.server.Close)
	return u
}

// setup builds a Doorman handler in front of one fake Radarr. The homepage
// key may GET movies (2 per minute) and GET one movie by id.
func setup(t *testing.T, destinationURL string) (*Handler, *bytes.Buffer) {
	t.Helper()
	yaml := `
version: 1
client_auth:
  - header: X-Api-Key
  - query: apikey
destinations:
  radarr:
    url: ` + destinationURL + `
    headers:
      X-Api-Key: real-radarr-key
    keys:
      homepage:
        secret: homepage-secret
        allow:
          - method: GET
            path: /api/v3/movie
            limit: 2/m
          - method: GET
            path: /api/v3/movie/{id}
            limit: none
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

func send(h http.Handler, method, target, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	if key != "" {
		r.Header.Set("X-Api-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAllowedRequestIsForwardedWithTheDestinationsKey(t *testing.T) {
	up := newUpstream(t)
	h, _ := setup(t, up.server.URL)

	w := send(h, http.MethodGet, "/api/v3/movie/5?apikey=homepage-secret&include=files", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := up.last.Header.Get("X-Api-Key"); got != "real-radarr-key" {
		t.Errorf("Radarr got X-Api-Key %q, want the real key", got)
	}
	if up.last.URL.Path != "/api/v3/movie/5" {
		t.Errorf("path = %q, want it unchanged", up.last.URL.Path)
	}
	if up.last.URL.RawQuery != "include=files" {
		t.Errorf("query = %q, want the client's key removed and the rest kept", up.last.URL.RawQuery)
	}
}

func TestRefusals(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		key    string
		status int
	}{
		{"no key", http.MethodGet, "/api/v3/movie", "", http.StatusUnauthorized},
		{"unknown key", http.MethodGet, "/api/v3/movie", "guess", http.StatusUnauthorized},
		{"conflicting keys", http.MethodGet, "/api/v3/movie?apikey=other", "homepage-secret", http.StatusUnauthorized},
		{"method not allowed", http.MethodDelete, "/api/v3/movie/5", "homepage-secret", http.StatusForbidden},
		{"path not allowed", http.MethodGet, "/api/v3/system/backup", "homepage-secret", http.StatusForbidden},
		{"unclear path", http.MethodGet, "/api/v3/movie/../system/backup", "homepage-secret", http.StatusBadRequest},
		{"encoded slash", http.MethodGet, "/api/v3/movie%2F5", "homepage-secret", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t)
			h, _ := setup(t, up.server.URL)

			w := send(h, tc.method, tc.target, tc.key)
			if w.Code != tc.status {
				t.Errorf("status = %d, want %d", w.Code, tc.status)
			}
			if up.calls != 0 {
				t.Errorf("a refused request reached the destination")
			}
		})
	}
}

func TestRateLimit(t *testing.T) {
	up := newUpstream(t)
	h, _ := setup(t, up.server.URL)

	for range 2 {
		if w := send(h, http.MethodGet, "/api/v3/movie", "homepage-secret"); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 within the limit", w.Code)
		}
	}
	w := send(h, http.MethodGet, "/api/v3/movie", "homepage-secret")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 over the limit", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("a 429 should say when to retry")
	}
	if up.calls != 2 {
		t.Errorf("the destination got %d calls, want 2", up.calls)
	}
}

func TestUnreachableDestination(t *testing.T) {
	up := newUpstream(t)
	url := up.server.URL
	up.server.Close() // nothing is listening any more

	h, _ := setup(t, url)
	if w := send(h, http.MethodGet, "/api/v3/movie/5", "homepage-secret"); w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

func TestDecisionsAreLoggedWithoutTheQuery(t *testing.T) {
	up := newUpstream(t)
	h, logs := setup(t, up.server.URL)

	send(h, http.MethodGet, "/api/v3/movie/5?apikey=homepage-secret&term=private", "")
	send(h, http.MethodDelete, "/api/v3/movie/5", "homepage-secret")

	out := logs.String()
	for _, want := range []string{
		"msg=allowed", "destination=radarr", "key=homepage", `rule="GET /api/v3/movie/{id}"`, "status=200",
		"msg=denied", `reason="no rule"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("logs are missing %q:\n%s", want, out)
		}
	}
	for _, leak := range []string{"homepage-secret", "term=private", "real-radarr-key"} {
		if strings.Contains(out, leak) {
			t.Errorf("logs leaked %q:\n%s", leak, out)
		}
	}
}
